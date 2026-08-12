import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import vm from "node:vm";
import { spawn, spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { findCachedSignerEntry } from "./oklink_signer_discovery.mjs";
import { SignerRuntime } from "./oklink_signer_runtime.mjs";

function mockResponse(body) {
  return { ok: true, status: 200, headers: { get: () => null }, text: async () => body };
}

test("signer discovery tolerates a missing asset cache directory", async () => {
  // Given: the asset cache directory has never been created (fresh user cache).
  const missingDir = path.join(os.tmpdir(), `oklink-missing-assets-${crypto.randomUUID()}`);

  // When: discovery probes for a cached signer entry in that directory.
  const entry = await findCachedSignerEntry(missingDir, "");

  // Then: it reports no cache instead of crashing on ENOENT.
  assert.equal(entry, "");
});

test("signer matches the current OKLink browser fingerprint payload", async (t) => {
  // Given: a deterministic encryption seam and an async request whose page URL differs from its API URL.
  const assetDir = await fs.mkdtemp(path.join(os.tmpdir(), "oklink-signer-test-"));
  t.after(async () => fs.rm(assetDir, { recursive: true, force: true }));
  await fs.writeFile(path.join(assetDir, "package.json"), '{"type":"module"}\n');
  await fs.writeFile(
    path.join(assetDir, "async-shared-DtkYVZG1.js"),
    'export function O() {}\nexport async function D(value) { return { data: btoa(value) }; }\n',
  );
  const pageURL = "https://www.oklink.com/zh-hans/bsc/address/0xabc";
  const apiURL = "https://www.oklink.com/download/explorer/v1/bsc/normalTransaction/download/async?t=1783885000000";
  const body = JSON.stringify({ address: "0xabc", email: "test@example.com", nonzeroValue: false, url: pageURL });
  const deviceID = "11111111-2222-4333-8444-555555555555";
  const input = JSON.stringify({ method: "POST", url: apiURL, body, chain: "bsc", address: "0xabc", deviceId: deviceID });
  const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "oklink_csv_signer.mjs");

  // When: the production signer creates the browser security headers.
  const result = spawnSync(process.execPath, ["--experimental-vm-modules", script, "--oneshot"], {
    input,
    encoding: "utf8",
    env: { ...process.env, OKLINK_SIGNER_ASSETS_DIR: assetDir, OKLINK_SIGNER_ENTRY: "async-shared-DtkYVZG1.js" },
  });
  assert.equal(result.status, 0, result.stderr);
  const response = JSON.parse(result.stdout);
  const fingerprint = JSON.parse(Buffer.from(response.headers["x-sec-token"].split(".")[1], "base64url").toString("utf8"));

  // Then: the fingerprint exactly follows the current official JS fields and page context.
  assert.equal("address" in fingerprint, false, "x-sec-token contains a non-official address field");
  assert.equal(fingerprint.m4, crypto.createHash("sha256").update(new URL(pageURL).pathname).digest("hex"));
  assert.equal(fingerprint.d1, "Chrome/150.0.0.0");
  assert.equal(fingerprint.d0, deviceID);
  assert.equal(response.headers.Devid, deviceID);
  assert.equal(response.headers["User-Agent"].includes("Chrome/150.0.0.0"), true);
});

test("service correlates concurrent requests and reloads changed builds", async (t) => {
  // Given: one signer process and a trusted, deterministic local module fixture.
  const fixture = await signerFixture(t);
  const service = startSignerService(fixture.env);
  t.after(() => service.close());
  const requests = Array.from({ length: 20 }, (_, index) => ({
    id: `request-${index}`,
    op: "sign",
    payload: signerInput(`0x${index.toString(16)}`),
  }));

  // When: requests are sent concurrently over one NDJSON stream.
  const responses = await Promise.all(requests.reverse().map((request) => service.request(request)));

  // Then: each response keeps its ID and all responses came from one service instance.
  assert.deepEqual(new Set(responses.map((response) => response.id)), new Set(requests.map((request) => request.id)));
  assert.equal(new Set(responses.map((response) => response.serviceInstance)).size, 1);
  assert.equal(responses.every((response) => response.ok && response.protocolVersion === "1"), true);
  const firstFingerprint = responses[0].buildFingerprint;

  await fs.appendFile(fixture.entryPath, "\n// changed-build\n");
  const reloaded = await service.request({ id: "reload-1", op: "reload" });
  assert.equal(reloaded.ok, true);
  assert.notEqual(reloaded.buildFingerprint, firstFingerprint);
});

test("service rejects malformed and oversized NDJSON without leaking secrets", async (t) => {
  // Given: a signer service with a local trusted module.
  const fixture = await signerFixture(t);
  const service = startSignerService(fixture.env);
  t.after(() => service.close());

  // When: malformed, missing-ID, and oversized frames cross the boundary.
  const malformed = await service.raw("{bad-json}");
  const missingID = await service.raw(JSON.stringify({ op: "sign", payload: signerInput("0xabc") }));
  const oversized = await service.raw(JSON.stringify({ id: "huge", op: "sign", padding: "x".repeat(1_100_000) }));

  // Then: errors are structured, bounded, and contain no credential-like input.
  assert.equal(malformed.ok, false);
  assert.equal(missingID.ok, false);
  assert.equal(oversized.ok, false);
  assert.match(malformed.error.code, /^invalid_/);
  assert.equal(JSON.stringify([malformed, missingID, oversized]).includes("test-cookie-secret"), false);
});

test("service terminates a hostile encrypt worker and serves the next request", async (t) => {
  const fixture = await signerFixture(t, { hostile: true });
  const service = startSignerService(fixture.env);
  t.after(() => service.close());

  const hostileInput = signerInput("0xhostile");
  hostileInput.deviceId = "hang-loop";
  const timedOut = await service.request({ id: "hostile", op: "sign", payload: hostileInput });
  const recovered = await service.request({ id: "recovered", op: "sign", payload: signerInput("0xabc") });

  assert.equal(timedOut.ok, false);
  assert.equal(timedOut.error.code, "signer_timeout");
  assert.equal(recovered.ok, true);
  assert.equal(recovered.id, "recovered");
  assert.equal(service.running(), true);
});

test("service rejects a multi-megabyte unterminated frame before buffering it all", async (t) => {
  const fixture = await signerFixture(t);
  const service = startSignerService(fixture.env);
  t.after(() => service.close());

  const oversized = await service.unterminated("x".repeat(4_000_000));
  service.write("\n");
  const status = await service.request({ id: "after-oversized", op: "status" });

  assert.equal(oversized.error.code, "invalid_oversized");
  assert.equal(status.ok, true);
  assert.equal(service.running(), true);
});

test("service signs with the current generateSecToken entry and seeds the device id", async (t) => {
  // Given: a module shaped like the current OKLink build, where the entry
  // exports generateSecToken({method,url}) and derives the device id from the
  // sandbox localStorage["devId"] seeded per request.
  const assetDir = await fs.mkdtemp(path.join(os.tmpdir(), "oklink-signer-sectoken-test-"));
  t.after(async () => fs.rm(assetDir, { recursive: true, force: true }));
  const entry = "17203-fixture.js";
  await fs.writeFile(path.join(assetDir, "package.json"), '{"type":"module"}\n');
  await fs.writeFile(path.join(assetDir, entry), 'export function O() {}\nexport async function generateSecToken(input) {\n  const devId = localStorage.getItem("devId") || "";\n  return `${devId}.${input.method}.${input.url}.web`;\n}\n');
  const service = startSignerService({
    ...process.env,
    OKLINK_SIGNER_ASSETS_DIR: assetDir,
    OKLINK_SIGNER_ENTRY: entry,
  });
  t.after(() => service.close());

  // When: a signing request carries the per-process browser device id.
  const deviceId = "22222222-3333-4444-8555-666666666666";
  const input = signerInput("0xabc");
  input.deviceId = deviceId;
  const response = await service.request({ id: "sec-token-1", op: "sign", payload: input });

  // Then: the runtime picked the sec-token API and forwarded the device id
  // into the sandbox, and the token covers the API path without its host.
  assert.equal(response.ok, true, JSON.stringify(response.error));
  assert.equal(response.headers["x-sec-token"], `${deviceId}.POST./download/explorer/v1/bsc/normalTransaction/download/async.web`);
  assert.equal(response.headers.Devid, deviceId);
});

test("service heals a stale cached entry with an automatic online refresh", async (t) => {
  if (typeof vm.SourceTextModule !== "function") {
    // The worker sandbox needs --experimental-vm-modules; run the suite with
    // `node --experimental-vm-modules --test tools/oklink_csv_signer_test.mjs`
    // to enable this test (production always starts node with the flag).
    t.skip("requires --experimental-vm-modules");
    return;
  }
  // Given: a cache whose entry no longer boots the encrypt sandbox (the live
  // OKLink build moved on), and a network that serves the new build.
  const assetDir = await fs.mkdtemp(path.join(os.tmpdir(), "oklink-signer-stale-test-"));
  t.after(async () => fs.rm(assetDir, { recursive: true, force: true }));
  await fs.writeFile(path.join(assetDir, "package.json"), '{"type":"module"}\n');
  await fs.writeFile(path.join(assetDir, "async-shared-OLD.js"),
    'export function O() {}\nexport async function Z() { return "short"; }\nexport{Z as generateSecToken};\n');
  const newEntry = "async-shared-NEW.js";
  const newEntryCode = 'export function O() {}\nexport async function Z(input) {\n  const devId = localStorage.getItem("devId") || "";\n  return `${devId}.${input.method}.${input.url}.web`;\n}\nexport{Z as generateSecToken};\n';
  const pageHTML = `<html><head><script src="https://static.oklink.com/cdn/assets/okfe/all-block-chain/assets/${newEntry}"></script></head></html>`;
  const mockFetch = async (input) => {
    const href = String(input);
    if (href.includes(".js")) {
      if (href.endsWith(newEntry)) return mockResponse(newEntryCode);
      return { ok: false, status: 404, headers: { get: () => null }, text: async () => "not found" };
    }
    return mockResponse(pageHTML);
  };
  const runtime = new SignerRuntime({ assetDir, pageURL: "https://www.oklink.com/zh-hans/bsc", fetchImpl: mockFetch });
  const input = signerInput("0xabc");
  input.deviceId = "33333333-4444-4555-8666-777777777777";

  // When: the first sign must boot the encrypt sandbox from the stale cache,
  // fail, and transparently re-discover the new build online.
  const headers = await runtime.sign(input);
  t.after(() => runtime.close());

  // Then: the online refresh produced a healthy sec-token from the new entry.
  assert.equal(runtime.version().entryModule, newEntry);
  assert.equal(headers["x-sec-token"], `${input.deviceId}.POST./download/explorer/v1/bsc/normalTransaction/download/async.web`);
  assert.equal(headers.Devid, input.deviceId);
});

test("service answers status before an unreachable public discovery can complete", async (t) => {  // Given: an allowlisted but unreachable public endpoint, which makes discovery wait for its network deadline.
  const service = startSignerService({
    ...process.env,
    OKLINK_SIGNER_PAGE_URL: "https://www.oklink.com:444/zh-hans/bsc",
  });
  t.after(() => service.close());

  // When: Go starts the persistent signer and asks for its first protocol response.
  const status = await service.request({ id: "startup-status", op: "status" });

  // Then: startup is responsive; discovery is deferred until a signing operation actually needs it.
  assert.equal(status.ok, true);
  assert.equal(status.id, "startup-status");
});

function signerInput(address) {
  const pageURL = `https://www.oklink.com/zh-hans/bsc/address/${address}`;
  return {
    method: "POST",
    url: "https://www.oklink.com/download/explorer/v1/bsc/normalTransaction/download/async?t=1783885000000",
    body: JSON.stringify({ address, email: "redacted@example.invalid", url: pageURL }),
    chain: "bsc",
    address,
    deviceId: "11111111-2222-4333-8444-555555555555",
  };
}

async function signerFixture(t, options = {}) {
  const assetDir = await fs.mkdtemp(path.join(os.tmpdir(), "oklink-signer-service-test-"));
  t.after(async () => fs.rm(assetDir, { recursive: true, force: true }));
  const entry = "async-shared-fixture.js";
  const entryPath = path.join(assetDir, entry);
  await fs.writeFile(path.join(assetDir, "package.json"), '{"type":"module"}\n');
  const hostile = options.hostile ? 'if (value.includes("hang-loop")) while (true) {}\n' : "";
  await fs.writeFile(entryPath, 'if (typeof process !== "undefined" || typeof fetch !== "undefined") throw new Error("sandbox escape");\n' +
    `export function O() {}\nexport async function D(value) { ${hostile}return { data: btoa(value) }; }\n`);
  return {
    entryPath,
    env: { ...process.env, OKLINK_SIGNER_ASSETS_DIR: assetDir, OKLINK_SIGNER_ENTRY: entry },
  };
}

function startSignerService(env) {
  const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "oklink_csv_signer.mjs");
  const child = spawn(process.execPath, ["--experimental-vm-modules", script, "--service"], {
    env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  const pending = [];
  let stdout = "";
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    stdout += chunk;
    for (;;) {
      const newline = stdout.indexOf("\n");
      if (newline < 0) break;
      const line = stdout.slice(0, newline);
      stdout = stdout.slice(newline + 1);
      pending.shift()?.resolve(JSON.parse(line));
    }
  });
  const send = (line) => new Promise((resolve, reject) => {
    pending.push({ resolve, reject });
    child.stdin.write(`${line}\n`);
    // The production encrypt deadline is 8s; keep the fixture response
    // timeout above it so slow-but-healthy signers are not misread as dead.
    const timer = setTimeout(() => reject(new Error("signer service response timeout")), 15_000);
    timer.unref();
    pending[pending.length - 1].resolve = (value) => {
      clearTimeout(timer);
      resolve(value);
    };
  });
  child.once("exit", (code) => {
    for (const waiter of pending.splice(0)) waiter.reject(new Error(`signer exited ${code}`));
  });
  return {
    request: (request) => send(JSON.stringify(request)),
    raw: send,
    unterminated: (value) => new Promise((resolve, reject) => {
      pending.push({ resolve, reject });
      child.stdin.write(value);
      const timer = setTimeout(() => reject(new Error("signer oversized response timeout")), 3_000);
      timer.unref();
      pending[pending.length - 1].resolve = (response) => {
        clearTimeout(timer);
        resolve(response);
      };
    }),
    write: (value) => child.stdin.write(value),
    running: () => child.exitCode === null && !child.killed,
    close: () => {
      child.stdin.end();
      child.kill();
    },
  };
}
