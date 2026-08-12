import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { parentPort, workerData } from "node:worker_threads";
import { assertAssetName } from "./oklink_signer_discovery.mjs";

// The module-level sandbox localStorage/document are seeded with the
// per-request browser device id right before each signing operation.
let sandboxLocalStorage = null;
let sandboxDocument = null;

// Background ont SDK initialization may fire requests that the sandbox cannot
// serve; swallow those rejections instead of killing the worker.
process.on("unhandledRejection", () => {});

const graph = { entry: workerData.graph.entry, files: new Map(workerData.graph.files) };
const selected = await loadEncrypt(graph, workerData.targetURL, workerData.assetDir, workerData.deviceId);
parentPort.postMessage({ type: "ready", api: selected.api });
parentPort.on("message", async (message) => {
  if (message?.type !== "encrypt") return;
  if (typeof message.deviceId === "string" && message.deviceId) {
    seedDeviceId(message.deviceId);
  }
  try {
    const value = await selected.encrypt(message.value, message.timestamp);
    parentPort.postMessage({ type: "result", id: message.id, ok: true, value });
  } catch {
    parentPort.postMessage({ type: "result", id: message.id, ok: false });
  }
});

// The ont SDK caches parsed document.cookie contents and prefers the cookie
// over localStorage when both hold a devId.  Seed both so a device id that was
// generated during module evaluation cannot shadow the per-request identity.
function seedDeviceId(deviceId) {
  if (sandboxLocalStorage) sandboxLocalStorage.setItem("devId", deviceId);
  if (sandboxDocument) sandboxDocument.cookie = `devId=${deviceId}; path=/`;
}

async function loadEncrypt(moduleGraph, targetURL, assetDir, deviceId) {
  const context = vm.createContext(browserSandbox(targetURL, deviceId), {
    name: "oklink-signer",
    codeGeneration: { strings: true, wasm: false },
  });
  loadOntSDK(context, assetDir);
  const modules = new Map();
  const load = (name) => {
    assertAssetName(name);
    if (modules.has(name)) return modules.get(name);
    const file = moduleGraph.files.get(name);
    if (!file || hash(file.code) !== file.hash) throw new Error("module graph hash verification failed");
    const module = new vm.SourceTextModule(file.code, {
      context,
      identifier: name,
      importModuleDynamically: (specifier) => {
        if (!specifier.startsWith("./")) return Promise.reject(new Error("dynamic imports are disabled in signer sandbox"));
        const target = load(specifier.slice(2));
        return Promise.resolve(target.link((nested) => {
          if (!nested.startsWith("./")) throw new Error("bare imports are disabled in signer sandbox");
          return load(nested.slice(2));
        })).then(() => target.evaluate({ timeout: 15_000 })).then(() => target);
      },
    });
    modules.set(name, module);
    return module;
  };
  const entry = load(moduleGraph.entry);
  await entry.link((specifier) => {
    if (!specifier.startsWith("./")) throw new Error("bare imports are disabled in signer sandbox");
    return load(specifier.slice(2));
  });
  await entry.evaluate({ timeout: 15_000 });
  if (typeof entry.namespace.O === "function") entry.namespace.O();
  const selected = await selectEncrypt(entry.namespace, deviceId);
  if (!selected) throw new Error("OKLink encrypt export is unavailable");
  return selected;
}

async function selectEncrypt(namespace, deviceId) {
  // Current OKLink build: the entry module exports generateSecToken(input) and
  // returns the whole x-sec-token value.
  for (const name of Object.keys(namespace).slice(0, 96)) {
    if (!name.includes("generateSecToken")) continue;
    const fn = namespace[name];
    if (typeof fn !== "function") continue;
    try {
      const token = await fn({ method: "POST", url: "/download/explorer/v1/bsc/normalTransaction/download/async" });
      if (typeof token === "string" && token.length > 64 && token.endsWith(".web") && tokenDidMatches(token, deviceId)) {
        return { api: "sec-token", encrypt: (input) => fn(input) };
      }
    } catch {}
  }
  // Legacy build: probe exported functions with (value, timestamp) and pick
  // one that returns a stable { data } payload.
  const functions = Object.keys(namespace).slice(0, 96).flatMap((name) => {
    try {
      return typeof namespace[name] === "function" ? [{ fn: namespace[name] }] : [];
    } catch {
      return [];
    }
  });
  const probe = JSON.stringify({ probe: "x".repeat(128) });
  const plausible = [];
  for (const candidate of functions) {
    try {
      const result = await candidate.fn(probe, "1783885000000");
      if (result && typeof result.data === "string" && result.data.length > 32) plausible.push({ ...candidate, data: result.data });
    } catch {}
  }
  for (const candidate of plausible) {
    try {
      const result = await candidate.fn(`${probe}y`, "1783885000001");
      if (result && typeof result.data === "string" && result.data.length > 32 && result.data !== candidate.data) {
        return { api: "legacy", encrypt: (value, timestamp) => candidate.fn(value, timestamp) };
      }
    } catch {}
  }
  return null;
}

// The token's device part must match the seeded identity whenever one was
// provided and the token follows the standard {rid,did,st,v} envelope.  A
// mismatch means the ont SDK could not pick the identity up (e.g. a
// missing/stale cached ont asset), which the runtime heals with an online
// refresh.  An empty seeded identity or a non-envelope token skips the check.
function tokenDidMatches(token, deviceId) {
  if (!deviceId) return true;
  try {
    const payload = JSON.parse(Buffer.from(token.split(".")[0], "base64url").toString("utf8"));
    return typeof payload.did !== "string" || payload.did === deviceId;
  } catch {
    return true;
  }
}

function browserSandbox(targetURL, deviceId) {
  const parsed = new URL(targetURL);
  const element = () => ({
    getContext: () => null, style: {}, setAttribute() {}, appendChild() {}, removeChild() {}, remove() {},
    addEventListener() {}, attachShadow: () => ({ appendChild() {} }),
  });
  const react = frameworkStub();
  const localStorage = (() => {
    // The ont SDK captures the device identity during its own initialization,
    // before any signing request arrives.  Seed localStorage["devId"] up
    // front with the (already known) process device id so the SDK caches the
    // right value; seedDeviceId() keeps cookie and storage in sync later.
    const store = new Map(deviceId ? [["devId", deviceId]] : []);
    return {
      getItem: (key) => (store.has(String(key)) ? store.get(String(key)) : null),
      setItem: (key, value) => { store.set(String(key), String(value)); },
      removeItem: (key) => { store.delete(String(key)); },
      clear: () => { store.clear(); },
      key: (index) => [...store.keys()][index] ?? null,
      get length() { return store.size; },
    };
  })();
  sandboxLocalStorage = localStorage;
  const document = {
    createElement: element, getElementsByTagName: () => [], querySelector: () => null,
    addEventListener() {}, removeEventListener() {}, body: { appendChild() {}, removeChild() {}, contains: () => true },
    documentElement: {}, head: { appendChild() {} }, cookie: "", baseURI: targetURL,
    currentScript: { src: "https://static.oklink.com/cdn/assets/okfe/util/ont/5.8.72/ont.js", tagName: "SCRIPT" },
  };
  sandboxDocument = document;
  const sandbox = {
    URL, TextEncoder, TextDecoder, atob, btoa, setTimeout, clearTimeout, crypto: crypto.webcrypto,
    console: Object.freeze({ log() {}, info() {}, warn() {}, error() {}, groupEnd() {} }),
    React: react, ReactDOM: frameworkStub(), ReactJSX: { Fragment: Symbol("Fragment"), jsx: () => ({}), jsxs: () => ({}) },
    navigator: Object.freeze({ userAgent: userAgent(), language: "zh-CN", languages: ["zh-CN", "zh"], platform: "Win32", hardwareConcurrency: 8, plugins: [] }),
    location: Object.freeze({ href: targetURL, pathname: parsed.pathname, hostname: parsed.hostname, protocol: parsed.protocol }),
    // The current OKLink build reads the browser device identity through the
    // page-level `window.utils.ont` SDK and localStorage["devId"].  A stub is
    // enough to satisfy the dead probe in vendor chunks when the real ont
    // asset is not cached.
    utils: { ont: ontStub() },
    performance: Object.freeze({ now: () => Date.now() }),
    localStorage,
    matchMedia: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }),
    document,
  };
  sandbox.window = sandbox;
  sandbox.globalThis = sandbox;
  sandbox.self = sandbox;
  sandbox.top = sandbox;
  sandbox.addEventListener = () => {};
  sandbox.removeEventListener = () => {};
  sandbox.dispatchEvent = () => false;
  return sandbox;
}

function loadOntSDK(context, assetDir) {
  if (!assetDir) return;
  let code;
  try {
    code = fs.readFileSync(path.join(assetDir, "ont.js"), "utf8");
  } catch {
    return; // no cached ont SDK: the stub below is used instead
  }
  try {
    vm.runInContext(code, context, { filename: "ont.js", timeout: 10_000 });
  } catch {
    // A broken or stale ont asset must not block signing; the stub covers it.
  }
}

function ontStub() {
  const identity = () => "";
  return new Proxy({}, {
    get: (target, property) => {
      if (property === "then" || property === Symbol.toStringTag || property === "toJSON") return undefined;
      return identity;
    },
  });
}

function frameworkStub() {
  const Component = class {};
  const callable = () => ({});
  return new Proxy({ Component, PureComponent: Component, Fragment: Symbol("Fragment") }, { get: (target, name) => target[name] ?? callable });
}

function hash(value) { return crypto.createHash("sha256").update(value).digest("hex"); }
function userAgent() { return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"; }
