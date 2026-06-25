'use strict';

const STEALTH_USER_AGENT = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

const STEALTH_VIEWPORT = Object.freeze({ width: 1920, height: 1080 });

const STEALTH_ARGS = Object.freeze([
  '--no-sandbox',
  '--disable-setuid-sandbox',
  '--disable-dev-shm-usage',
  '--disable-gpu',
  '--disable-web-security',
  '--disable-blink-features=AutomationControlled',
  '--disable-features=IsolateOrigins,site-per-process',
  '--disable-features=VizDisplayCompositor',
]);

const STEALTH_HEADERS = Object.freeze({
  'Accept-Language': 'zh-CN,zh;q=0.9,en;q=0.8',
  'Sec-CH-UA': '"Not/A)Brand";v="8", "Chromium";v="126", "Google Chrome";v="126"',
  'Sec-CH-UA-Mobile': '?0',
  'Sec-CH-UA-Platform': '"Windows"',
  'Sec-Fetch-Dest': 'document',
  'Sec-Fetch-Mode': 'navigate',
  'Sec-Fetch-Site': 'none',
  'Sec-Fetch-User': '?1',
  'Upgrade-Insecure-Requests': '1',
  'Cache-Control': 'max-age=0',
});

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function applyStealthConfig(page) {
  await page.addInitScript(() => {
    const defineGetter = (target, key, value) => {
      try {
        Object.defineProperty(target, key, { get: () => value, configurable: true });
        return true;
      } catch (_error) {
        return false;
      }
    };

    defineGetter(navigator, 'webdriver', false);
    defineGetter(navigator, 'languages', ['zh-CN', 'zh', 'en']);
    defineGetter(navigator, 'plugins', [1, 2, 3, 4, 5]);
    defineGetter(navigator, 'platform', 'Win32');
    defineGetter(navigator, 'hardwareConcurrency', 8);
    defineGetter(navigator, 'deviceMemory', 8);

    window.chrome = window.chrome || {};
    window.chrome.app = window.chrome.app || {
      InstallState: { DISABLED: 'disabled', INSTALLED: 'installed', NOT_INSTALLED: 'not_installed' },
      RunningState: { CANNOT_RUN: 'cannot_run', READY_TO_RUN: 'ready_to_run', RUNNING: 'running' },
      getDetails: () => null,
      getIsInstalled: () => false,
      installState: () => 'not_installed',
      isInstalled: false,
      runningState: () => 'cannot_run',
    };
    window.chrome.runtime = window.chrome.runtime || {
      OnInstalledReason: { CHROME_UPDATE: 'chrome_update', INSTALL: 'install', SHARED_MODULE_UPDATE: 'shared_module_update', UPDATE: 'update' },
      OnRestartRequiredReason: { APP_UPDATE: 'app_update', OS_UPDATE: 'os_update', PERIODIC: 'periodic' },
      PlatformArch: { ARM: 'arm', ARM64: 'arm64', MIPS: 'mips', MIPS64: 'mips64', X86_32: 'x86-32', X86_64: 'x86-64' },
      PlatformNaclArch: { ARM: 'arm', MIPS: 'mips', MIPS64: 'mips64', X86_32: 'x86-32', X86_64: 'x86-64' },
      PlatformOs: { ANDROID: 'android', CROS: 'cros', LINUX: 'linux', MAC: 'mac', OPENBSD: 'openbsd', WIN: 'win' },
      RequestUpdateCheckStatus: { NO_UPDATE: 'no_update', THROTTLED: 'throttled', UPDATE_AVAILABLE: 'update_available' },
      connect: () => ({ onDisconnect: { addListener: () => {} }, onMessage: { addListener: () => {} }, postMessage: () => {} }),
      sendMessage: () => undefined,
    };

    const permissions = navigator.permissions;
    if (permissions && typeof permissions.query === 'function') {
      const originalQuery = permissions.query.bind(permissions);
      permissions.query = parameters => {
        if (parameters && parameters.name === 'notifications') {
          return Promise.resolve({ state: Notification.permission });
        }
        return originalQuery(parameters);
      };
    }

    const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
    HTMLCanvasElement.prototype.toDataURL = function patchedToDataURL(...args) {
      const context = this.getContext('2d');
      if (context) {
        context.fillStyle = 'rgba(255,255,255,0.01)';
        context.fillRect(0, 0, 1, 1);
      }
      return originalToDataURL.apply(this, args);
    };

    const getParameter = WebGLRenderingContext.prototype.getParameter;
    WebGLRenderingContext.prototype.getParameter = function patchedGetParameter(parameter) {
      if (parameter === 37445) return 'Intel Inc.';
      if (parameter === 37446) return 'Intel Iris OpenGL Engine';
      return getParameter.apply(this, [parameter]);
    };
  });

  await page.setExtraHTTPHeaders(STEALTH_HEADERS);
}

async function hasCloudflareClearance(context) {
  const cookies = await context.cookies('https://dune.com').catch(() => []);
  return cookies.some(cookie => cookie.name === 'cf_clearance' && cookie.value);
}

async function isCloudflareSurface(page) {
  const title = await page.title().catch(() => '');
  if (title === 'Just a moment...' || title === '请稍候…' || title === '请稍候...') return true;
  if (title.startsWith('Attention Required')) return true;
  const text = await page.evaluate(() => document.body?.innerText?.substring(0, 250) || '').catch(() => '');
  if (text.startsWith('Sorry, you have been blocked')) return true;
  if (text.startsWith('Just a moment') || text.startsWith('请稍候')) return true;
  const html = await page.content().catch(() => '');
  return html.includes('challenges.cloudflare.com') || html.includes('cf-turnstile-response') || html.includes('_cf_chl_opt');
}

async function clickTurnstile(page) {
  const selectors = [
    'input[type="checkbox"]',
    '#challenge-stage label',
    '.cb-lb',
    'label',
  ];

  for (const frame of page.frames()) {
    if (!frame.url().includes('challenges.cloudflare.com')) continue;
    for (const selector of selectors) {
      const target = frame.locator(selector).first();
      if (await target.isVisible({ timeout: 1000 }).catch(() => false)) {
        await target.click({ force: true, timeout: 3000 });
        process.stderr.write('STEALTH_TURNSTILE_CLICKED selector=' + selector + '\n');
        return true;
      }
    }
  }

  const iframe = page.locator('iframe[src*="challenges.cloudflare.com"]').first();
  const box = await iframe.boundingBox({ timeout: 1000 }).catch(() => null);
  if (box && box.width > 0 && box.height > 0) {
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    process.stderr.write('STEALTH_TURNSTILE_IFRAME_CLICKED\n');
    return true;
  }

  return false;
}

async function clickPageCenter(page) {
  const box = await page.evaluate(() => ({ width: window.innerWidth, height: window.innerHeight })).catch(() => null);
  if (!box || !box.width || !box.height) return false;
  await page.mouse.click(box.width / 2, box.height / 2);
  process.stderr.write('STEALTH_CF_CENTER_CLICKED\n');
  return true;
}

async function submitVisibleChallengeControl(page) {
  return await page.evaluate(() => {
    for (const form of document.querySelectorAll('form')) {
      if (typeof form.submit === 'function') {
        form.submit();
        return true;
      }
    }
    for (const control of document.querySelectorAll('button, input[type="submit"], input[type="button"]')) {
      const element = control;
      const style = window.getComputedStyle(element);
      const visible = style.visibility !== 'hidden' && style.display !== 'none' && element.getClientRects().length > 0;
      const disabled = element.disabled || element.getAttribute('aria-disabled') === 'true';
      if (visible && !disabled) {
        element.click();
        return true;
      }
    }
    return false;
  }).catch(() => false);
}

async function restartCloudflareChallenge(page) {
  return await page.evaluate(() => {
    if (window._cf_chl_opt && typeof window._cf_chl_opt.cUq === 'function') {
      window._cf_chl_opt.cUq();
      return true;
    }
    document.dispatchEvent(new Event('cf_challenge_restart'));
    return true;
  }).catch(() => false);
}

async function runCloudflareInteraction(page, round) {
  let acted = await clickTurnstile(page).catch(() => false);
  if (!acted && round % 2 === 0) acted = await clickPageCenter(page).catch(() => false);
  if (!acted && round % 3 === 0) {
    acted = await submitVisibleChallengeControl(page);
    if (acted) process.stderr.write('STEALTH_CF_FORM_OR_BUTTON_SUBMITTED\n');
  }
  if (!acted && round % 4 === 0) {
    acted = await restartCloudflareChallenge(page);
    if (acted) process.stderr.write('STEALTH_CF_RESTART_EVENT\n');
  }
  if (!acted && round > 0 && round % 6 === 0) {
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => {});
    process.stderr.write('STEALTH_CF_RELOADED\n');
    acted = true;
  }
  return acted;
}

async function solveCloudflareWithStealth(page, context, timeoutMs = 30000) {
  const deadline = Date.now() + Math.max(5000, timeoutMs);
  let clicked = false;
  let round = 0;

  while (Date.now() < deadline) {
    const blocked = await isCloudflareSurface(page);
    if (!blocked && await hasCloudflareClearance(context)) {
      process.stderr.write('STEALTH_CF_CLEARANCE_FOUND\n');
      return true;
    }
    if (!blocked) {
      if (clicked) process.stderr.write('STEALTH_CF_RESOLVED_AFTER_CLICK\n');
      return true;
    }
    clicked = (await runCloudflareInteraction(page, round)) || clicked;
    await sleep(2000);
    if (!(await isCloudflareSurface(page))) {
      if (clicked) process.stderr.write('STEALTH_CF_RESOLVED_AFTER_CLICK\n');
      return true;
    }
    round += 1;
  }

  return false;
}

module.exports = {
  STEALTH_ARGS,
  STEALTH_HEADERS,
  STEALTH_USER_AGENT,
  STEALTH_VIEWPORT,
  applyStealthConfig,
  clickTurnstile,
  hasCloudflareClearance,
  isCloudflareSurface,
  solveCloudflareWithStealth,
};
