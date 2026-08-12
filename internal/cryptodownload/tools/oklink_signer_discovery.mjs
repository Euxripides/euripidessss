import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

const ASSET_BASE = "https://static.oklink.com/cdn/assets/okfe/all-block-chain/assets/";
const MAX_ASSETS = 96;
const MAX_BYTES = 2_000_000;
const ASSET_NAME = /^[A-Za-z0-9_-]+\.js$/;
const IMPORT_PATTERNS = [
  /from\s*["']\.\/([^"']+\.js)["']/g,
  /import\(\s*["']\.\/([^"']+\.js)["']\s*\)/g,
  /import\(\s*`\.\/([^`]+\.js)`\s*\)/g,
];

export async function findCachedSignerEntry(assetDir, excluded) {
  let names;
  try {
    names = await fs.readdir(assetDir);
  } catch (error) {
    // A fresh user cache has no asset directory yet.  Treat that as "no
    // cached entry" and let the caller fall through to online discovery
    // instead of failing closed on ENOENT.
    if (error?.code === "ENOENT") return "";
    throw error;
  }
  names = names.filter((name) => name !== excluded && ASSET_NAME.test(name)).slice(0, MAX_ASSETS);
  for (const name of names) {
    const code = await boundedRead(path.join(assetDir, name));
    if (isSignerEntryCode(code)) return name;
  }
  return "";
}

export function isSignerEntryCode(code) {
  if (/export\{[^}]*\bas generateSecToken\b/.test(code)) return true;
  return code.includes("12441684wraQpF") && /export\{[^}]*\bas D\b/.test(code);
}

export async function discoverGraph(options) {
  const assetDir = options.assetDir;
  await fs.mkdir(assetDir, { recursive: true });
  await fs.writeFile(path.join(assetDir, "package.json"), '{"type":"module"}\n');
  const entry = options.entryOverride || await discoverEntry(options.fetchImpl, options.pageURL, assetDir);
  const refreshDependencies = options.refreshDependencies === true || !options.entryOverride;
  assertAssetName(entry);
  const files = new Map();
  const queue = [entry];
  while (queue.length > 0) {
    if (files.size >= MAX_ASSETS) throw new Error("asset graph exceeds bounded module limit");
    const name = queue.shift();
    if (files.has(name)) continue;
    const code = await readOrDownload(options.fetchImpl, assetDir, name, refreshDependencies);
    files.set(name, { code, hash: hash(code) });
    for (const dependency of importsFrom(code)) {
      assertAssetName(dependency);
      if (!files.has(dependency)) queue.push(dependency);
    }
  }
  const fingerprintInput = [...files].sort(([a], [b]) => a.localeCompare(b)).map(([name, file]) => `${name}:${file.hash}`).join("\n");
  return { assetDir, entry, files, buildFingerprint: hash(fingerprintInput) };
}

async function discoverEntry(fetchImpl, pageURL, assetDir) {
  const page = await boundedFetch(fetchImpl, pageURL);
  const scripts = [...page.matchAll(/(?:src|href)=["']([^"']+\.js)["']/g)]
    .map((match) => new URL(match[1], pageURL)).filter(isAllowedURL).slice(0, 48);
  const inspected = new Map();
  for (let offset = 0; offset < scripts.length; offset += 3) {
    const batch = await Promise.all(scripts.slice(offset, offset + 3).map(async (scriptURL) => [path.posix.basename(scriptURL.pathname), await boundedFetch(fetchImpl, scriptURL.href)]));
    for (const [name, code] of batch) {
      if (!ASSET_NAME.test(name)) continue;
      inspected.set(name, code);
      await fs.writeFile(path.join(assetDir, name), code);
    }
  }
  const referenced = new Set();
  for (const code of inspected.values()) {
    const signerEntry = encryptionImport(code);
    if (signerEntry) return signerEntry;
    for (const match of code.matchAll(/["']\.\/([A-Za-z0-9_-]+\.js)["']/g)) referenced.add(match[1]);
  }
  const queue = [...referenced];
  while (queue.length > 0 && inspected.size < MAX_ASSETS) {
    const name = queue.shift();
    if (inspected.has(name)) continue;
    const code = await readOrDownload(fetchImpl, assetDir, name);
    const signerEntry = encryptionImport(code);
    if (signerEntry) return signerEntry;
    inspected.set(name, code);
    const dependencies = [...code.matchAll(/["']\.\/([A-Za-z0-9_-]+\.js)["']/g)].map((match) => match[1]);
    queue.unshift(...dependencies.filter((dependency) => dependency.startsWith("async-shared-") && !inspected.has(dependency)));
    queue.push(...dependencies.filter((dependency) => !dependency.startsWith("async-shared-") && !inspected.has(dependency)));
    if (isPlausibleEntry(name, code)) return name;
  }
  for (const [name, code] of inspected) if (isPlausibleEntry(name, code)) return name;
  throw new Error("current OKLink signer entry was not found in the bounded public module graph");
}

function encryptionImport(code) {
  if (!code.includes("generateSecToken")) return "";
  // Current OKLink build: generateSecToken is dynamically imported from a
  // dedicated sec-token module (e.g. `import(\`./17203-sQeGX4It.js\`)`).
  for (const match of code.matchAll(/import\(\s*[`"']\.\/([^`"']+\.js)[`"']\s*\)/g)) {
    const target = match[1];
    if (ASSET_NAME.test(target)) return target;
  }
  // Legacy build: the module importing generateSecToken re-exports the
  // encrypt function as `D` from an async-shared chunk.
  for (const match of code.matchAll(/import\{([^}]*)\}from["']\.\/(async-shared-[A-Za-z0-9_-]+\.js)["']/g)) {
    if (/\bD as\b/.test(match[1])) return match[2];
  }
  return "";
}

async function readOrDownload(fetchImpl, assetDir, name, refresh = false) {
  const filePath = path.join(assetDir, name);
  if (!refresh) {
    try {
      return await boundedRead(filePath);
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
  }
  let code;
  try {
    code = await boundedFetch(fetchImpl, ASSET_BASE + name);
  } catch (error) {
    throw new Error(`asset ${name}: ${error.message}`);
  }
  await fs.writeFile(filePath, code);
  return code;
}

async function boundedRead(filePath) {
  const stat = await fs.stat(filePath);
  if (stat.size > MAX_BYTES) throw new Error("asset exceeds bounded size limit");
  return fs.readFile(filePath, "utf8");
}

async function boundedFetch(fetchImpl, url) {
  const parsed = new URL(url);
  if (!isAllowedURL(parsed)) throw new Error("asset origin is not allowlisted");
  let response;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      response = await fetchImpl(parsed, { redirect: "error", signal: AbortSignal.timeout(8_000) });
      break;
    } catch (error) {
      if (attempt === 1) throw error;
    }
  }
  if (!response.ok) throw new Error(`public asset fetch failed: HTTP ${response.status}`);
  if (Number(response.headers.get("content-length") || 0) > MAX_BYTES) throw new Error("asset exceeds bounded size limit");
  const value = await response.text();
  if (Buffer.byteLength(value) > MAX_BYTES) throw new Error("asset exceeds bounded size limit");
  return value;
}

function isAllowedURL(url) {
  return url.protocol === "https:" && (url.hostname === "www.oklink.com" || url.hostname === "static.oklink.com");
}

export function assertAssetName(name) {
  if (!ASSET_NAME.test(name)) throw new Error("asset module name is not allowlisted");
}

function importsFrom(code) {
  const names = new Set();
  for (const pattern of IMPORT_PATTERNS) for (const match of code.matchAll(pattern)) names.add(match[1]);
  return names;
}

function isPlausibleEntry(name, code) {
  if (isSignerEntryCode(code)) return true;
  return name.startsWith("async-shared-") && /export\{[^}]*\bas D\b/.test(code) && code.includes("rid");
}

function hash(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}
