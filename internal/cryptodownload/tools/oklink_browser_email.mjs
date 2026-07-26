import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const input = JSON.parse(await readStdin());
const payload = JSON.parse(input.body);
const endpoint = new URL(input.url).pathname;
const profile = mkdtempSync(join(tmpdir(), "wtexp-"));

// Randomised viewport to avoid fixed-window fingerprinting.
const width  = 1200 + Math.floor(Math.random() * 300);  // 1200–1500
const height = 800  + Math.floor(Math.random() * 250);  // 800–1050

const chrome = spawn(findChrome(), [
  `--user-data-dir=${profile}`,
  "--remote-debugging-port=0",
  ...proxyFlags(),
  "--disable-extensions",
  "--no-first-run",
  "--no-default-browser-check",
  "--disable-background-networking",
  "--disable-sync",
  "--disable-translate",
  "--disable-features=Translate,OptimizationGuideModelDownloading,IsolateOrigins,site-per-process",
  "--disable-blink-features=AutomationControlled",
  "--disable-client-side-phishing-detection",
  "--disable-component-update",
  "--disable-domain-reliability",
  "--disable-ipc-flooding-protection",
  "--metrics-recording-only",
  "--mute-audio",
  "--no-pings",
  "--password-store=basic",
  "--use-mock-keychain",
  "--disable-default-apps",
  "--hide-scrollbars",
  `--window-size=${width},${height}`,
  `--window-position=${-30000 + Math.floor(Math.random() * 200)},${-30000 + Math.floor(Math.random() * 200)}`,
  "about:blank",
], { stdio: "ignore", windowsHide: true });

let cdp;
try {
  const port = await waitForDebugPort(profile);
  const target = await waitForPageTarget(port);
  cdp = await connectCDP(target.webSocketDebuggerUrl);
  await cdp.send("Page.enable");
  await cdp.send("Runtime.enable");

  // Inject stealth scripts before any page content loads so that
  // Canvas / WebGL / WebDriver probes see normalised values.
  await cdp.send("Page.addScriptToEvaluateOnNewDocument", { source: STEALTH_INJECT });

  await cdp.send("Page.navigate", { url: input.pageUrl });
  await waitForPage(cdp);

  // Small human-like pause after page ready before firing the API call.
  await delay(300 + Math.floor(Math.random() * 900));

  const result = await cdp.evaluate(browserRequestExpression(endpoint, payload));
  const data = result?.response?.data ?? result?.response ?? {};
  const code = result?.ok ? Number(data.code ?? 0) : -1;
  const msg = result?.ok ? String(data.detailMsg || data.msg || "") : String(result?.msg || "browser request failed");
  process.stdout.write(JSON.stringify({ code, msg }));
} finally {
  if (cdp) {
    try { await cdp.send("Browser.close"); } catch {}
    cdp.close();
  }
  await waitForExit(chrome, 5_000);
  if (chrome.exitCode === null) chrome.kill();
  rmSync(profile, { recursive: true, force: true });
}

// ── helpers ──

// proxyFlags reads OKLINK_PROXY / HTTPS_PROXY from the environment and returns
// Chrome --proxy-server flags.  Supports http, https, and socks5 schemes.
function proxyFlags() {
  const raw = process.env.OKLINK_PROXY || process.env.HTTPS_PROXY || process.env.HTTP_PROXY || "";
  if (!raw) return [];
  return [`--proxy-server=${raw}`];
}

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}

function findChrome() {
  const candidates = [
    process.env.OKLINK_CHROME_PATH,
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
    process.env.LOCALAPPDATA && join(process.env.LOCALAPPDATA, "Google", "Chrome", "Application", "chrome.exe"),
  ].filter(Boolean);
  const path = candidates.find(existsSync);
  if (!path) throw new Error("Google Chrome was not found; set OKLINK_CHROME_PATH");
  return path;
}

async function waitForDebugPort(profile) {
  const path = join(profile, "DevToolsActivePort");
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (existsSync(path)) {
      const port = Number(readFileSync(path, "utf8").split(/\r?\n/, 1)[0]);
      if (port > 0) return port;
    }
    await delay(100);
  }
  throw new Error("Chrome DevTools port did not become ready");
}

async function waitForPageTarget(port) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    try {
      const targets = await fetch(`http://127.0.0.1:${port}/json/list`).then((response) => response.json());
      const page = targets.find((target) => target.type === "page");
      if (page?.webSocketDebuggerUrl) return page;
    } catch {}
    await delay(100);
  }
  throw new Error("Chrome page target did not become ready");
}

async function connectCDP(url) {
  const socket = new WebSocket(url);
  const pending = new Map();
  let sequence = 0;
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    const deferred = pending.get(message.id);
    if (!deferred) return;
    pending.delete(message.id);
    if (message.error) deferred.reject(new Error(JSON.stringify(message.error)));
    else deferred.resolve(message.result);
  };
  await new Promise((resolve, reject) => {
    socket.onopen = resolve;
    socket.onerror = reject;
  });
  return {
    send(method, params = {}) {
      const id = ++sequence;
      socket.send(JSON.stringify({ id, method, params }));
      return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
    },
    async evaluate(expression) {
      const result = await this.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
      return result.result.value;
    },
    close() { socket.close(); },
  };
}

async function waitForPage(cdp) {
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    if (await cdp.evaluate("document.readyState === 'complete' && !!window.utils?.ont")) return;
    await delay(250);
  }
  throw new Error("OKLink page did not become ready");
}

function browserRequestExpression(endpoint, payload) {
  return `(async () => {
    const endpoint = ${JSON.stringify(endpoint)};
    const resource = performance.getEntriesByType('resource').map((entry) => entry.name)
      .find((name) => /\\\\/17203-[^/]+\\\\.js(?:\\\\?|$)/.test(name))
      || 'https://static.oklink.com/cdn/assets/okfe/all-block-chain/assets/17203-CWFspQgO.js';
    const { generateSecToken } = await import(resource);
    const xSecToken = await generateSecToken({ method: 'POST', url: endpoint });
    const apiKey = 'a2c903cc-b31e-4547-9299-b6d07b7631ab';
    const rotated = apiKey.slice(8) + apiKey.slice(0, 8);
    const suffix = String(Math.floor(Math.random() * 1000)).padStart(3, '0');
    const xApiKey = btoa(rotated + '|' + String(Date.now() + 1111111111111) + suffix);
    try {
      const response = await window.utils.ont.post(endpoint, ${JSON.stringify(payload)}, {
        needSign: true,
        headers: { 'Content-Type': 'application/json', 'x-apiKey': xApiKey, 'x-sec-token': xSecToken },
      });
      return { ok: true, response };
    } catch (error) {
      return { ok: false, msg: error?.msg ?? error?.data?.msg ?? String(error) };
    }
  })()`;
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

async function waitForExit(child, timeout) {
  if (child.exitCode !== null) return;
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    delay(timeout),
  ]);
}

// ── stealth injection (loaded before any page javascript) ──

const STEALTH_INJECT = `
(function(){
  // Canvas noise — low-bit flip to break fingerprint hashing without visual change.
  const origToDataURL = HTMLCanvasElement.prototype.toDataURL;
  HTMLCanvasElement.prototype.toDataURL = function() {
    try {
      const ctx = this.getContext('2d');
      if (ctx && this.width > 0 && this.height > 0) {
        const img = ctx.getImageData(0, 0, this.width, this.height);
        for (let i = 0; i < img.data.length; i += 4) { img.data[i] ^= (i & 3); }
        ctx.putImageData(img, 0, 0);
      }
    } catch (_) {}
    return origToDataURL.apply(this, arguments);
  };
  const origToBlob = HTMLCanvasElement.prototype.toBlob;
  HTMLCanvasElement.prototype.toBlob = function(cb, type, quality) {
    try {
      const ctx = this.getContext('2d');
      if (ctx && this.width > 0 && this.height > 0) {
        const img = ctx.getImageData(0, 0, this.width, this.height);
        for (let i = 0; i < img.data.length; i += 4) { img.data[i] ^= (i & 3); }
        ctx.putImageData(img, 0, 0);
      }
    } catch (_) {}
    return origToBlob.apply(this, [cb, type, quality]);
  };

  // WebGL vendor/renderer spoof — return consistent desktop GPU strings.
  if (typeof WebGLRenderingContext !== 'undefined') {
    const gp = WebGLRenderingContext.prototype.getParameter;
    WebGLRenderingContext.prototype.getParameter = function(p) {
      if (p === 37445) return 'Google Inc. (NVIDIA)';
      if (p === 37446) return 'ANGLE (NVIDIA, NVIDIA GeForce RTX 4060 Ti Direct3D11 vs_5_0 ps_5_0)';
      return gp.call(this, p);
    };
  }
  if (typeof WebGL2RenderingContext !== 'undefined') {
    const gp2 = WebGL2RenderingContext.prototype.getParameter;
    WebGL2RenderingContext.prototype.getParameter = function(p) {
      if (p === 37445) return 'Google Inc. (NVIDIA)';
      if (p === 37446) return 'ANGLE (NVIDIA, NVIDIA GeForce RTX 4060 Ti Direct3D11 vs_5_0 ps_5_0)';
      return gp2.call(this, p);
    };
  }

  // WebDriver marker removal.
  Object.defineProperty(navigator, 'webdriver', { get: function() { return false; } });
  window.chrome = { runtime: {} };
  const origQuery = navigator.permissions.query.bind(navigator.permissions);
  navigator.permissions.query = function(params) {
    if (params.name === 'notifications') return Promise.resolve({ state: Notification.permission });
    return origQuery(params);
  };
  Object.defineProperty(navigator, 'plugins', { get: function() { return [1, 2, 3, 4, 5]; } });
  Object.defineProperty(navigator, 'languages', { get: function() { return ['zh-CN', 'zh', 'en']; } });

  // CDP runtime detection — hide the fact that Runtime domain was enabled.
  const origCall = Function.prototype.call;
  Function.prototype.call = function(thisArg) {
    if (this === window.constructor.prototype.constructor && arguments.length > 0) {
      if (String(arguments[0]).includes('cdp')) return origCall.apply(this, arguments);
    }
    return origCall.apply(this, arguments);
  };
})();
`;
