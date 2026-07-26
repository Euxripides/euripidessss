import crypto from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { SignerRuntime } from "./oklink_signer_runtime.mjs";

const PROTOCOL_VERSION = "1";
const MAX_LINE_BYTES = 1_000_000;
const MAX_ACTIVE = 32;
const SIGN_OPERATION_TIMEOUT_MS = 6_000;
const serviceInstance = crypto.randomUUID();

function runtimeOptions() {
  return {
    assetDir: process.env.OKLINK_SIGNER_ASSETS_DIR || path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "outputs", "oklink_assets"),
    entry: process.env.OKLINK_SIGNER_ENTRY || "",
    pageURL: process.env.OKLINK_SIGNER_PAGE_URL || "https://www.oklink.com/zh-hans/bsc",
  };
}

function success(id, runtime, extra = {}) {
  let version = {};
  try { version = runtime.version(); } catch {}
  return { id, ok: true, protocolVersion: PROTOCOL_VERSION, serviceInstance, ...version, ...extra };
}

function failure(id, code, message, runtime = null, retryable = false) {
  let version = {};
  try {
    if (runtime) version = runtime.version();
  } catch {}
  return {
    id: id ?? null,
    ok: false,
    protocolVersion: PROTOCOL_VERSION,
    serviceInstance,
    ...version,
    error: { code, message, retryable },
  };
}

function parseRequest(line) {
  if (Buffer.byteLength(line) > MAX_LINE_BYTES) return { error: failure(null, "invalid_oversized", "NDJSON frame exceeds size limit") };
  let value;
  try {
    value = JSON.parse(line);
  } catch {
    return { error: failure(null, "invalid_json", "NDJSON frame is not valid JSON") };
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) return { error: failure(null, "invalid_request", "request must be an object") };
  if (typeof value.id !== "string" || value.id.length === 0 || value.id.length > 128) return { error: failure(null, "invalid_id", "request id is required") };
  if (value.op !== "sign" && value.op !== "reload" && value.op !== "status" && value.op !== "shutdown") {
    return { error: failure(value.id, "invalid_op", "request operation is unsupported") };
  }
  if (value.op === "sign" && (!value.payload || typeof value.payload !== "object" || Array.isArray(value.payload))) {
    return { error: failure(value.id, "invalid_payload", "sign payload is required") };
  }
  return { value };
}

async function handle(runtime, request) {
  try {
    switch (request.op) {
      case "sign": {
        const headers = await withSignerDeadline(() => runtime.sign(request.payload));
        return success(request.id, runtime, { headers, headerNames: normalizedHeaderNames(headers) });
      }
      case "reload":
        await runtime.reload();
        return success(request.id, runtime, { reloaded: true });
      case "status":
        return success(request.id, runtime);
      case "shutdown":
        return success(request.id, runtime, { shuttingDown: true });
      default:
        return failure(request.id, "invalid_op", "request operation is unsupported", runtime);
    }
  } catch (error) {
    const code = error?.code === "signer_timeout" ? "signer_timeout" : "signer_runtime";
    return failure(request.id, code, "signer operation failed", runtime, request.op === "reload" || code === "signer_timeout");
  }
}

function normalizedHeaderNames(headers) {
  return Object.keys(headers).map((name) => name.toLowerCase()).sort();
}

async function serviceMain() {
  const runtime = new SignerRuntime(runtimeOptions());
  const active = new Set();
  let shuttingDown = false;
  for await (const frame of boundedFrames(process.stdin)) {
    if (shuttingDown) break;
    if (frame.oversized) {
      writeResponse(failure(null, "invalid_oversized", "NDJSON frame exceeds size limit"));
      continue;
    }
    const line = frame.line;
    const parsed = parseRequest(line);
    if (parsed.error) {
      writeResponse(parsed.error);
      continue;
    }
    if (active.size >= MAX_ACTIVE) {
      writeResponse(failure(parsed.value.id, "queue_full", "signer concurrency limit reached", runtime, true));
      continue;
    }
    const task = handle(runtime, parsed.value).then((response) => {
      writeResponse(response);
      if (parsed.value.op === "shutdown") {
        shuttingDown = true;
        process.stdin.destroy();
      }
    }).finally(() => active.delete(task));
    active.add(task);
  }
  await Promise.allSettled(active);
  await runtime.close();
}

async function withSignerDeadline(operation) {
  let timer;
  try {
    return await Promise.race([
      operation(),
      new Promise((_, reject) => {
        timer = setTimeout(() => {
          const error = new Error("signer operation exceeded discovery deadline");
          error.code = "signer_timeout";
          reject(error);
        }, SIGN_OPERATION_TIMEOUT_MS);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

async function oneShotMain() {
  const chunks = [];
  let length = 0;
  for await (const chunk of process.stdin) {
    length += chunk.length;
    if (length > MAX_LINE_BYTES) throw new Error("one-shot input exceeds size limit");
    chunks.push(chunk);
  }
  const payload = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  const runtime = new SignerRuntime(runtimeOptions());
  try {
    await runtime.load();
    const headers = await runtime.sign(payload);
    process.stdout.write(`${JSON.stringify({ headers, ...runtime.version(), protocolVersion: PROTOCOL_VERSION })}\n`);
  } finally {
    await runtime.close();
  }
}

async function* boundedFrames(input) {
  let chunks = [];
  let length = 0;
  let discarding = false;
  for await (const rawChunk of input) {
    const chunk = Buffer.from(rawChunk);
    let offset = 0;
    while (offset < chunk.length) {
      const newline = chunk.indexOf(10, offset);
      const end = newline === -1 ? chunk.length : newline;
      const segment = chunk.subarray(offset, end);
      if (!discarding && length + segment.length > MAX_LINE_BYTES) {
        chunks = [];
        length = 0;
        discarding = true;
        yield { oversized: true };
      }
      if (!discarding && segment.length > 0) {
        chunks.push(Buffer.from(segment));
        length += segment.length;
      }
      if (newline === -1) break;
      if (!discarding) {
        const line = Buffer.concat(chunks, length).toString("utf8").replace(/\r$/, "");
        yield { line };
      }
      chunks = [];
      length = 0;
      discarding = false;
      offset = newline + 1;
    }
  }
  if (!discarding && length > 0) yield { line: Buffer.concat(chunks, length).toString("utf8").replace(/\r$/, "") };
}

function writeResponse(response) {
  process.stdout.write(`${JSON.stringify(response)}\n`);
}

const mode = process.argv[2] || "--oneshot";
const main = mode === "--service" ? serviceMain : oneShotMain;
main().catch(() => {
  process.stderr.write("signer failed [details redacted]\n");
  process.exitCode = 1;
});
