import { clickFirstSelector, sleep } from './register-login-dom.mjs';

const DUNE_HOME = 'https://dune.com/';
const LOGIN_URL = 'https://dune.com/auth/login';
const REGISTER_URL = 'https://dune.com/auth/register';

export async function navigateToAuth(page, mode) {
  const targetURL = mode === 'register' ? REGISTER_URL : LOGIN_URL;
  const targetPath = mode === 'register' ? '/auth/register' : '/auth/login';
  const targetText = mode === 'register' ? 'Sign up' : 'Log in';

  const currentUrl = page.url();
  if (currentUrl.includes(targetPath)) {
    return !(await isBlocked(page));
  }
  if (!currentUrl.includes('dune.com')) {
    for (let attempt = 0; attempt < 5; attempt++) {
      await page.goto(DUNE_HOME, { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
      if (!(await isBlocked(page))) break;
      const waitMs = Math.min(5000 * Math.pow(2, attempt), 60000);
      await sleep(waitMs);
    }
    if (await isBlocked(page)) return false;
  }

  const clicked = await clickFirstSelector(page, [
    `a[href*="${targetPath}"]`,
    `button:has-text("${targetText}")`,
    `a:has-text("${targetText}")`,
  ], 2500);
  if (!clicked) {
    await page.evaluate((url) => { window.location.href = url; }, targetURL);
  }
  await sleep(4000);

  for (let i = 0; i < 10; i++) {
    await sleep(1000);
    const url = page.url();
    if (url.includes(targetPath)) break;
  }

  if (await isBlocked(page)) {
    for (let attempt = 0; attempt < 3; attempt++) {
      await page.goto(targetURL, { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
      if (!(await isBlocked(page))) return true;
      await sleep(5000);
    }
    return false;
  }
  return true;
}

export async function isBlocked(page) {
  try {
    const title = await page.title().catch(() => '');
    if (title === 'Just a moment...' || title === '请稍候…' || title === '请稍候...') return true;
    if (title.startsWith('Attention Required')) return true;

    const body = await page.evaluate(() => document.body?.innerText?.substring(0, 250) || '').catch(() => '');
    if (body.startsWith('Sorry, you have been blocked')) return true;
    if (body.startsWith('Just a moment') || body.startsWith('请稍候')) {
      const hasDuneContent = await page.evaluate(() => {
        return !!document.querySelector('nav, header a, [class*="Nav"], [class*="Header"], [class*="Sidebar"], input[type="email"], input[type="password"]');
      }).catch(() => false);
      if (!hasDuneContent) return true;
    }

    const h = await page.content().catch(() => '');
    if (h.includes('challenges.cloudflare.com') || h.includes('cf-turnstile-response') || h.includes('_cf_chl_opt')) {
      const hasDuneNav = h.includes('class="geist_') || h.includes('class="geist-') || h.includes('nav') || h.includes('Dune');
      if (!hasDuneNav) return true;
    }
    return false;
  } catch {
    return false;
  }
}

export async function hasCaptcha(page) {
  return await isBlocked(page);
}

export async function solveTurnstile(page) {
  try {
    await sleep(3000);
    const frame = page.frameLocator('iframe[src*="challenges.cloudflare.com"]');
    const cb = frame.locator('#challenge-stage input[type="checkbox"], .cb-lb input[type="checkbox"]').first();
    if (await cb.isVisible({ timeout: 5000 }).catch(() => false)) {
      await cb.click();
      console.error('TURNSTILE_CLICKED');
      for (let i = 0; i < 60; i++) {
        await sleep(2000);
        if (!(await isBlocked(page))) return true;
      }
    }
  } catch {}
  for (let i = 0; i < 90; i++) {
    await sleep(2000);
    if (!(await isBlocked(page))) return true;
  }
  return false;
}
