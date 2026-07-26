import crypto from "node:crypto";
import vm from "node:vm";
import { parentPort, workerData } from "node:worker_threads";
import { assertAssetName } from "./oklink_signer_discovery.mjs";

const graph = { entry: workerData.graph.entry, files: new Map(workerData.graph.files) };
const encrypt = await loadEncrypt(graph, workerData.targetURL);
parentPort.postMessage({ type: "ready" });
parentPort.on("message", async (message) => {
  if (message?.type !== "encrypt") return;
  try {
    const value = await encrypt(message.value, message.timestamp);
    parentPort.postMessage({ type: "result", id: message.id, ok: true, value });
  } catch {
    parentPort.postMessage({ type: "result", id: message.id, ok: false });
  }
});

async function loadEncrypt(moduleGraph, targetURL) {
  const context = vm.createContext(browserSandbox(targetURL), {
    name: "oklink-signer",
    codeGeneration: { strings: true, wasm: false },
  });
  const modules = new Map();
  const load = (name) => {
    assertAssetName(name);
    if (modules.has(name)) return modules.get(name);
    const file = moduleGraph.files.get(name);
    if (!file || hash(file.code) !== file.hash) throw new Error("module graph hash verification failed");
    const module = new vm.SourceTextModule(file.code, {
      context,
      identifier: name,
      importModuleDynamically: () => Promise.reject(new Error("dynamic imports are disabled in signer sandbox")),
    });
    modules.set(name, module);
    return module;
  };
  const entry = load(moduleGraph.entry);
  await entry.link((specifier) => {
    if (!specifier.startsWith("./")) throw new Error("bare imports are disabled in signer sandbox");
    return load(specifier.slice(2));
  });
  await entry.evaluate({ timeout: 3_000 });
  if (typeof entry.namespace.O === "function") entry.namespace.O();
  const selected = await selectEncrypt(entry.namespace);
  if (!selected) throw new Error("OKLink encrypt export is unavailable");
  return selected;
}

async function selectEncrypt(namespace) {
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
      if (result && typeof result.data === "string" && result.data.length > 32 && result.data !== candidate.data) return candidate.fn;
    } catch {}
  }
  return null;
}

function browserSandbox(targetURL) {
  const parsed = new URL(targetURL);
  const element = () => ({
    getContext: () => null, style: {}, setAttribute() {}, appendChild() {}, removeChild() {}, remove() {},
    addEventListener() {}, attachShadow: () => ({ appendChild() {} }),
  });
  const react = frameworkStub();
  const sandbox = {
    URL, TextEncoder, TextDecoder, atob, btoa, setTimeout, clearTimeout, crypto: crypto.webcrypto,
    console: Object.freeze({ log() {}, info() {}, warn() {}, error() {}, groupEnd() {} }),
    React: react, ReactDOM: frameworkStub(), ReactJSX: { Fragment: Symbol("Fragment"), jsx: () => ({}), jsxs: () => ({}) },
    navigator: Object.freeze({ userAgent: userAgent(), language: "zh-CN", languages: ["zh-CN", "zh"], platform: "Win32", hardwareConcurrency: 8, plugins: [] }),
    location: Object.freeze({ href: targetURL, pathname: parsed.pathname, hostname: parsed.hostname, protocol: parsed.protocol }),
    matchMedia: () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }),
    document: { createElement: element, getElementsByTagName: () => [], querySelector: () => null, addEventListener() {}, removeEventListener() {}, body: { appendChild() {}, removeChild() {}, contains: () => true }, documentElement: {}, head: { appendChild() {} }, cookie: "" },
  };
  sandbox.window = sandbox;
  sandbox.globalThis = sandbox;
  return sandbox;
}

function frameworkStub() {
  const Component = class {};
  const callable = () => ({});
  return new Proxy({ Component, PureComponent: Component, Fragment: Symbol("Fragment") }, { get: (target, name) => target[name] ?? callable });
}

function hash(value) { return crypto.createHash("sha256").update(value).digest("hex"); }
function userAgent() { return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"; }
