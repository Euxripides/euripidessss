import crypto from "node:crypto";
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
  }

  async load() {
    if (this.loading) return this.loading;
    const loading = this.loadFresh();
    this.loading = loading;
    try {
      return await loading;
    } finally {
      if (this.loading === loading) this.loading = null;
    }
  }

  async loadFresh() {
    const graph = await discoverGraph(this);
    this.discovered = graph;
    let activeGraph = graph;
    let encryptor;
    try {
      encryptor = await createEncryptor(graph, this.pageURL);
    } catch (error) {
      if (this.entryOverride) throw error;
      const fallbackEntry = await findCachedSignerEntry(this.assetDir, graph.entry);
      if (!fallbackEntry) throw error;
      activeGraph = await discoverGraph({ ...this, entryOverride: fallbackEntry });
      encryptor = await createEncryptor(activeGraph, this.pageURL);
    }
    const previous = this.loaded?.encryptor;
    this.loaded = { ...activeGraph, encryptor, pageBuildFingerprint: graph.buildFingerprint };
    if (previous) await previous.close();
    return this.version();
  }

  async reload() {
    return this.load();
  }

  version() {
    if (!this.loaded) throw new Error("signer runtime is not loaded");
    return { buildFingerprint: this.loaded.buildFingerprint, entryModule: this.loaded.entry, pageBuildFingerprint: this.loaded.pageBuildFingerprint };
  }

  async sign(input) {
    if (!this.loaded) await this.load();
    return createHeaders((value, timestamp) => this.loaded.encryptor.encrypt(value, timestamp), input);
  }

  async close() {
    const encryptor = this.loaded?.encryptor;
    this.loaded = null;
    if (encryptor) await encryptor.close();
  }
}

async function createEncryptor(graph, targetURL) {
  const encryptor = new WorkerEncryptor(graph, targetURL);
  await encryptor.init();
  return encryptor;
}

function createHeaders(encrypt, input) {
  return Promise.resolve().then(async () => {
    const method = String(input.method || "POST").toUpperCase();
    const targetURL = String(input.url || "");
    const parsed = new URL(targetURL);
    const body = String(input.body || "");
    const pageURL = JSON.parse(body || "{}").url || targetURL;
    const deviceID = String(input.deviceId || "").trim();
    if (!deviceID) throw new Error("deviceId is required");
    const verify = okVerify(method, parsed.pathname + parsed.search, body);
    return {
      "App-Type": "web", Devid: deviceID, "Ok-Verify-Token": verify.token, "Ok-Timestamp": verify.timestamp,
      "Ok-Verify-Sign": verify.sign, Platform: "web", "User-Agent": userAgent(), "x-apiKey": xAPIKey(),
      "x-cdn": "https://static.oklink.com", "x-id-group": `${Date.now()}-c-${Math.floor(Math.random() * 90) + 10}`,
      "x-locale": "zh_CN", "x-sec-token": await secToken(encrypt, method, new URL(pageURL).pathname, deviceID),
      "x-simulated-trading": "undefined", "x-site-info": base64JSON({ t: 1, l: "zh-CN", c: "CNY", ch: String(input.chain || "").toLowerCase() }), "x-utc": "8", "x-zkdex-env": "0",
    };
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
  const fingerprint = { m0: ".web", m1: 1, m2: timestamp, m3: crypto.randomUUID().replaceAll("-", ""), m4: hash(pathname), d0: deviceID, d1: "Chrome/150.0.0.0", d2: 0, d3: null, d4: null, d5: 0 };
  for (let index = 0; index <= 12; index += 1) fingerprint[`e${index}`] = 0;
  const digest = hash(method + pathname + timestamp);
  const [encryptedFingerprint, encryptedDigest] = await Promise.all([encrypt(JSON.stringify(fingerprint), timestamp), encrypt(digest, timestamp)]);
  return `${base64JSON({ rid: crypto.randomUUID(), did: deviceID, st: timestamp, v: 1 })}.${encryptedFingerprint.data}.${encryptedDigest.data}.web`;
}

function xAPIKey() {
  const value = "a2c903cc-b31e-4547-9299-b6d07b7631ab";
  return Buffer.from(`${value.slice(8)}${value.slice(0, 8)}|${Date.now() + 1111111111111}${String(Math.floor(Math.random() * 1000)).padStart(3, "0")}`).toString("base64");
}

function base64JSON(value) { return Buffer.from(JSON.stringify(value)).toString("base64"); }
function hash(value) { return crypto.createHash("sha256").update(value).digest("hex"); }
function userAgent() { return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"; }
