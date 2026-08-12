import crypto from "node:crypto";
import fs from "node:fs";
import { discoverGraph, findCachedSignerEntry } from "./oklink_signer_discovery.mjs";
import { WorkerEncryptor } from "./oklink_signer_executor.mjs";

const DEFAULT_PAGE = "https://www.oklink.com/zh-hans/bsc";

export class SignerRuntime {
  constructor(options = {}) {
    this.assetDir = options.assetDir;
    this.entryOverride = options.entry;
    this.pageURL = options.pageURL || DEFAULT_PAGE;
    this.fetchImpl = options.fetchImpl || fetch;
    this.loaded = null;
    this.loading = null;
    this.onlineRefreshTried = false;
  }

  async load(options = {}) {
    if (this.loading && !options.refresh) return this.loading;
    const loading = this.loadFresh(options);
    this.loading = loading;
    try {
      return await loading;
    } finally {
      if (this.loading === loading) this.loading = null;
    }
  }

  async loadFresh(options = {}) {
    // A reload always re-discovers the live OKLink build.  A first load may
    // start from the local asset cache so cold starts avoid the slow public
    // discovery round-trips (the cache is refreshed on demand and on 50113).
    const graph = options.refresh ? await discoverGraph(this) : await this.resolveGraph();
    this.discovered = graph;
    let activeGraph = graph;
    let encryptor;
    try {
      encryptor = await createEncryptor(graph, this.pageURL, this.assetDir, this.deviceId);
    } catch (error) {
      if (this.entryOverride || options.refresh) throw error;
      // A cached entry that no longer matches the live OKLink build fails to
      // boot the encrypt sandbox.  Try the next cached candidate first, then
      // fall back to a fresh online discovery so a build update heals without
      // operator action (at most one online refresh per runtime).
      const fallbackEntry = await findCachedSignerEntry(this.assetDir, graph.entry);
      if (fallbackEntry) {
        try {
          activeGraph = await discoverGraph({ ...this, entryOverride: fallbackEntry });
          encryptor = await createEncryptor(activeGraph, this.pageURL, this.assetDir, this.deviceId);
        } catch {
          // fall through to the online refresh below
        }
      }
      if (encryptor === undefined && !this.onlineRefreshTried) {
        this.onlineRefreshTried = true;
        try {
          activeGraph = await discoverGraph(this);
          encryptor = await createEncryptor(activeGraph, this.pageURL, this.assetDir, this.deviceId);
          this.onlineRefreshTried = false;
        } catch {
          // keep the original failure below
        }
      }
      if (encryptor === undefined) throw error;
    }
    const previous = this.loaded?.encryptor;
    this.loaded = { ...activeGraph, encryptor, pageBuildFingerprint: graph.buildFingerprint };
    if (previous) await previous.close();
    return this.version();
  }

  async resolveGraph() {
    if (this.entryOverride) return discoverGraph(this);
    // Cold starts (e.g. a fresh user cache after an upgrade) may not have the
    // asset directory yet.  Ensure it exists before probing for a cached
    // entry so the runtime falls back to online discovery instead of
    // crashing with ENOENT.
    fs.mkdirSync(this.assetDir, { recursive: true });
    const cachedEntry = await findCachedSignerEntry(this.assetDir, "");
    if (cachedEntry) {
      try {
        return await discoverGraph({ ...this, entryOverride: cachedEntry });
      } catch {
        // A stale or incomplete cache is not fatal: fall back to online discovery.
      }
    }
    return discoverGraph(this);
  }

  async reload() {
    return this.load({ refresh: true });
  }

  version() {
    if (!this.loaded) throw new Error("signer runtime is not loaded");
    return { buildFingerprint: this.loaded.buildFingerprint, entryModule: this.loaded.entry, pageBuildFingerprint: this.loaded.pageBuildFingerprint };
  }

  async sign(input) {
    // The device id must be known before the worker sandbox boots: the ont SDK
    // caches the id it first sees during its own initialization (Jn in Qn).
    this.deviceId = String(input.deviceId || "").trim();
    if (!this.loaded) await this.load();
    this.loaded.encryptor.setDeviceId(this.deviceId);
    return createHeaders((value, timestamp) => this.loaded.encryptor.encrypt(value, timestamp), this.loaded.encryptor.api, input);
  }

  async close() {
    const encryptor = this.loaded?.encryptor;
    this.loaded = null;
    if (encryptor) await encryptor.close();
  }
}

async function createEncryptor(graph, targetURL, assetDir, deviceId) {
  const encryptor = new WorkerEncryptor(graph, targetURL, { assetDir, deviceId });
  await encryptor.init();
  return encryptor;
}

function createHeaders(encrypt, api, input) {
  return Promise.resolve().then(async () => {
    const method = String(input.method || "POST").toUpperCase();
    const targetURL = String(input.url || "");
    const parsed = new URL(targetURL);
    const body = String(input.body || "");
    const pageURL = JSON.parse(body || "{}").url || targetURL;
    const deviceID = String(input.deviceId || "").trim();
    if (!deviceID) throw new Error("deviceId is required");
    const verify = okVerify(method, parsed.pathname + parsed.search, body);
    const headers = {
      "App-Type": "web", Devid: deviceID, "Ok-Verify-Token": verify.token, "Ok-Timestamp": verify.timestamp,
      "Ok-Verify-Sign": verify.sign, Platform: "web", "User-Agent": userAgent(), "x-apiKey": xAPIKey(),
      "x-cdn": "https://static.oklink.com", "x-id-group": `${Date.now()}-c-${Math.floor(Math.random() * 90) + 10}`,
      "x-locale": "zh_CN",
      "x-simulated-trading": "undefined", "x-site-info": base64JSON({ t: 1, l: "zh-CN", c: "CNY", ch: String(input.chain || "").toLowerCase() }), "x-utc": "8", "x-zkdex-env": "0",
    };
    if (api === "sec-token") {
      // Current OKLink build: the module generates the whole x-sec-token from
      // { method, url } (a hostless path) and derives the device id from the
      // sandbox localStorage["devId"], which we seed with the request device id.
      headers["x-sec-token"] = await encrypt({ method, url: parsed.pathname }, String(Date.now()));
      return headers;
    }
    headers["x-sec-token"] = await secToken(encrypt, method, new URL(pageURL).pathname, deviceID);
    return headers;
  });
}

function okVerify(method, pathWithQuery, body) {
  const token = crypto.randomUUID();
  const now = Date.now();
  const tokenHash = hash(token);
  const seconds = Math.floor(now / 1000);
  const offsetA = Math.floor((seconds / 600) % 32);
  const offsetB = Math.floor((seconds / 3600) % 32);
  let key = "";
  for (let index = 0; index < 32; index += 1) key += tokenHash[(offsetA + (offsetB + index) * index) % 32];
  const value = method === "POST" || method === "PUT" ? pathWithQuery.split("?")[0] + body : pathWithQuery.replace("?", "");
  return { token, timestamp: String(now), sign: crypto.createHmac("sha256", Buffer.from(key)).update(value).digest("base64") };
}

async function secToken(encrypt, method, pathname, deviceID) {
  const timestamp = String(Date.now());
  const ua = userAgent();
  const chromeVersion = (ua.match(/Chrome\/([\d.]+)/) || [null, "150.0.0.0"])[1];
  const fingerprint = { m0: ".web", m1: 1, m2: timestamp, m3: crypto.randomUUID().replaceAll("-", ""), m4: hash(pathname), d0: deviceID, d1: `Chrome/${chromeVersion}`, d2: 0, d3: null, d4: null, d5: 0 };
  for (let index = 0; index <= 12; index += 1) fingerprint[`e${index}`] = 0;
  const digest = hash(method + pathname + timestamp);
  const [encryptedFingerprint, encryptedDigest] = await Promise.all([encrypt(JSON.stringify(fingerprint), timestamp), encrypt(digest, timestamp)]);
  return `${base64JSON({ rid: crypto.randomUUID(), did: deviceID, st: timestamp, v: 1 })}.${encryptedFingerprint.data}.${encryptedDigest.data}.web`;
}

function xAPIKey() {
  const value = clientIdentity();
  return Buffer.from(`${value.slice(8)}${value.slice(0, 8)}|${Date.now() + 1111111111111}${String(Math.floor(Math.random() * 1000)).padStart(3, "0")}`).toString("base64");
}

function clientIdentity() {
  const override = (process.env.OKLINK_CSV_SIGNER_CLIENT_ID || "").trim();
  if (override) return override;
  const statePath = (process.env.TEMP || process.env.TMP || "/tmp") + "\\oklink_signer_client_id.txt";
  try {
    const existing = fs.readFileSync(statePath, "utf8").trim();
    if (existing.length === 36) return existing;
  } catch {}
  const fresh = crypto.randomUUID();
  try {
    fs.writeFileSync(statePath, fresh, "utf8");
  } catch {}
  return fresh;
}

function base64JSON(value) { return Buffer.from(JSON.stringify(value)).toString("base64"); }
function hash(value) { return crypto.createHash("sha256").update(value).digest("hex"); }
function userAgent() {
  return process.env.OKLINK_CSV_SIGNER_UA ||
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36";
}
