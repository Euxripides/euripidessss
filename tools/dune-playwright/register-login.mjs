import { chromium } from 'playwright';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

const DUNE_HOME = 'https://dune.com/';
const AUTH_URL = 'https://dune.com/auth/login';

// Human-like random delays
function rand(min, max) { return min + Math.floor(Math.random() * (max - min)); }
function humanDelay() { return sleep(rand(800, 3000)); }
function randWindowWidth() { return rand(950, 1150); }
function randWindowHeight() { return rand(650, 820); }

const STEALTH_ARGS = [
  '--no-sandbox',
  '--disable-setuid-sandbox',
  '--disable-blink-features=AutomationControlled',
  '--disable-features=IsolateOrigins,site-per-process',
  '--window-position=100,100',
  '--window-size=1000,700',
];

function requiredString(v, name) { if (typeof v !== 'string' || !v.trim()) throw new Error(name + ' required'); return v.trim(); }
function writeJSON(d) { process.stdout.write(JSON.stringify(d) + '\n'); }
function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

function shouldInjectCookie(name, mode) {
  const lower = String(name || '').toLowerCase();
  if (mode === 'capture') return true;
  return !lower.startsWith('auth-');
}

function isLoggedInSurface(url, body) {
  return url.includes('/welcome') || url.includes('/queries') || url.includes('/discover') || url.includes('/home')
    || body.includes('Activity') || body.includes('My Queries') || body.includes('Discover') || body.includes('Dashboard');
}

function hasCapturedAuth(creds) {
  return !!(creds && creds.cookie && (creds.authorization || creds.access_token));
}

function saveCapturedAuth(creds) {
  const authFile = join(process.cwd(), '..', '..', 'backend', 'data', 'dune', 'auth.json');
  mkdirSync(dirname(authFile), { recursive: true });
  writeFileSync(authFile, JSON.stringify({
    cookie: creds.cookie,
    authorization: creds.authorization,
    access_token: creds.access_token,
    team_id: creds.team_id,
    updated_at: new Date().toISOString(),
  }, null, 2));
}

// ── DOM helpers ──

async function firstVisible(page, ss, ms) {
  for (const s of ss) {
    const el = page.locator(s).first();
    if (await el.isVisible({ timeout: ms || 3000 }).catch(() => false)) return el;
  }
  return null;
}

async function clickFirstText(page, texts, ms) {
  for (const t of texts) {
    // Try clicking a button/link that contains this text
    const el = page.locator('button:has-text("' + t + '"), a:has-text("' + t + '"), [role="button"]:has-text("' + t + '")').first();
    if (await el.isVisible({ timeout: Math.min(ms || 2000, 2000) }).catch(() => false)) { 
      await el.click(); 
      await sleep(1000); 
      console.error('CLICKED: ' + t);
      return true; 
    }
  }
  // Fallback: any element with this text
  for (const t of texts) {
    const b = page.getByText(t, { exact: true }).first();
    if (await b.isVisible({ timeout: 1000 }).catch(() => false)) { await b.click({ force: true }); await sleep(1000); console.error('CLICKED_FALLBACK: ' + t); return true; }
  }
  return false;
}

async function clickSubmit(page, texts) {
  for (const t of texts) {
    const b = page.locator('button[type="submit"]:has-text("' + t + '"), button:has-text("' + t + '")').first();
    if (await b.isVisible({ timeout: 2000 }).catch(() => false)) { await b.click(); return true; }
  }
  return false;
}

async function visibleInputs(page, sel) {
  return (await page.locator(sel).all()).filter(async e => await e.isVisible().catch(() => false));
}

async function detectionFailed(page, msg) {
  const h = await page.content().catch(() => '');
  console.error('DETECT_FAIL ' + msg + ' ' + h.substring(0, 400));
  return { ok: false, error: 'detection_failed: ' + msg };
}

// ── CF detection ──

function isBlocked(page) {
  return page.title().catch(() => '').then(async (title) => {
    if (title === 'Just a moment...' || title === '请稍候…' || title === '请稍候...') return true;
    if (title.startsWith('Attention Required')) return true;
    const body = await page.evaluate(() => document.body?.innerText?.substring(0, 250) || '').catch(() => '');
    if (body.startsWith('Sorry, you have been blocked')) return true;
    if (body.startsWith('Just a moment') || body.startsWith('请稍候')) {
      const hasDuneContent = await page.evaluate(() =>
        !!document.querySelector('nav, header a, [class*="Nav"], input[type="email"], input[type="password"]')
      ).catch(() => false);
      if (!hasDuneContent) return true;
    }
    const h = await page.content().catch(() => '');
    if (h.includes('challenges.cloudflare.com') || h.includes('cf-turnstile-response') || h.includes('_cf_chl_opt')) {
      const hasDuneNav = h.includes('class="geist_') || h.includes('class="geist-') || h.includes('nav') || h.includes('Dune');
      if (!hasDuneNav) return true;
    }
    return false;
  });
}

async function hasCaptcha(page) { return await isBlocked(page); }

async function solveTurnstile(page) {
  console.error('SOLVING_CF...');
  
  // Strategy 1: Try clicking Turnstile checkbox in iframe
  try {
    const frame = page.frameLocator('iframe[src*="challenges.cloudflare.com"]');
    // Try checkbox
    const cb = frame.locator('input[type="checkbox"], #challenge-stage label, .cb-lb').first();
    if (await cb.isVisible({ timeout: 3000 }).catch(() => false)) {
      await cb.click({ force: true });
      console.error('TURNSTILE_CLICKED');
      for (let i = 0; i < 30; i++) { await sleep(2000); if (!(await isBlocked(page))) return true; }
    }
  } catch {}

  // Strategy 2: Click the center of the page (some CF pages have invisible challenge area)
  try {
    const box = await page.evaluate(() => ({ w: window.innerWidth, h: window.innerHeight }));
    await page.mouse.click(box.w / 2, box.h / 2);
    console.error('CENTER_CLICKED');
    await sleep(3000);
    if (!(await isBlocked(page))) return true;
  } catch {}

  // Strategy 3: Force-submit any form on the page
  try {
    const submitted = await page.evaluate(() => {
      const forms = document.querySelectorAll('form');
      for (const f of forms) {
        f.submit();
        return true;
      }
      // Also try clicking any visible button
      const btns = document.querySelectorAll('button, input[type="submit"], input[type="button"]');
      for (const b of btns) {
        if (b.offsetParent !== null) { b.click(); return true; }
      }
      return false;
    });
    if (submitted) {
      console.error('FORM_SUBMITTED');
      for (let i = 0; i < 20; i++) { await sleep(2000); if (!(await isBlocked(page))) return true; }
    }
  } catch {}

  // Strategy 4: Try to execute CF challenge JavaScript
  try {
    await page.evaluate(() => {
      // Try calling any CF challenge functions
      if (typeof window._cf_chl_opt !== 'undefined') {
        // Force challenge retry
        if (window._cf_chl_opt.cUq) window._cf_chl_opt.cUq();
      }
      // Trigger CF's own retry mechanism
      const event = new Event('cf_challenge_restart');
      document.dispatchEvent(event);
    });
  } catch {}

  // Strategy 5: Reload the page and try again
  try {
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
    console.error('RELOADED');
    await sleep(5000);
  } catch {}

  // Wait up to 2 minutes for auto-resolution
  console.error('Waiting for CF auto-resolve...');
  for (let i = 0; i < 60; i++) {
    await sleep(2000);
    if (!(await isBlocked(page))) {
      console.error('CF_RESOLVED at ' + (i * 2) + 's');
      return true;
    }
    if (i % 15 === 0) {
      // Retry strategies periodically
      try {
        const frame = page.frameLocator('iframe[src*="challenges.cloudflare.com"]');
        const cb = frame.locator('input[type="checkbox"]').first();
        if (await cb.isVisible({ timeout: 500 }).catch(() => false)) await cb.click({ force: true });
      } catch {}
      try {
        await page.mouse.click(400, 300);
      } catch {}
    }
  }

  console.error('CF_TIMEOUT');
  return false;
}

async function navigateWithCFRetry(page, url, maxAttempts = 5) {
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
    if (!(await isBlocked(page))) return true;
    const waitMs = Math.min(5000 * Math.pow(2, attempt), 60000);
    console.error('CF_RETRY ' + (attempt + 1) + '/' + maxAttempts + ' wait=' + waitMs + 'ms');
    await sleep(waitMs);
  }
  return false;
}

// Navigate to auth page — try homepage click first, fallback to direct goto + solveTurnstile
async function navigateToAuth(page) {
  const currentUrl = page.url();
  if (currentUrl.includes('/auth/login') || currentUrl.includes('/auth/register')) {
    return !(await isBlocked(page));
  }

  // Ensure we're on dune.com homepage first
  if (!currentUrl.includes('dune.com')) {
    if (!(await navigateWithCFRetry(page, DUNE_HOME, 5))) return false;
  }
  await sleep(3000);

  // Dismiss cookie banners
  await clickFirstText(page, ['Accept all', 'Accept All', 'Accept all cookies', 'Accept'], 2000);
  await sleep(1000);

  // Try clicking auth link
  let clicked = false;
  try {
    const pwLink = page.locator('a[href*="auth/login"], a[href*="auth/register"], a:has-text("Log in"), a:has-text("Sign up")').first();
    await pwLink.click({ timeout: 5000 });
    clicked = true;
    console.error('PW_CLICK_OK');
  } catch {}

  if (!clicked) {
    clicked = await page.evaluate(() => {
      for (const el of document.querySelectorAll('a, button')) {
        const text = (el.textContent || '').toLowerCase();
        if ((text.includes('log in') || text.includes('sign up') || text.includes('sign in')) && el.offsetParent !== null) {
          el.click(); return true;
        }
      }
      for (const el of document.querySelectorAll('a')) {
        if (el.href && el.href.includes('/auth/')) { el.click(); return true; }
      }
      return false;
    });
    if (clicked) console.error('EVAL_CLICK_OK');
  }

  if (!clicked) {
    // DIRECT GOTO to auth/register — then wait for manual CF resolution
    console.error('========================================');
    console.error('MANUAL_CF: Browser window opened. Please solve the Cloudflare challenge.');
    console.error('MANUAL_CF: The script will auto-continue once CF passes.');
    console.error('========================================');
    await page.goto('https://dune.com/auth/register', { waitUntil: 'domcontentloaded', timeout: 15000 }).catch(() => {});
    await sleep(2000);
  }

  // Wait for CF to resolve — with manual intervention support
  // The user can see the browser window and interact with it
  for (let i = 0; i < 300; i++) {  // up to 10 minutes
    await sleep(2000);
    const blocked = await isBlocked(page);
    
    // Try auto-solve every 10 seconds
    if (blocked && i % 5 === 0) {
      await solveTurnstile(page);
    }
    
    const url = page.url();
    const emails = await page.evaluate(() => document.querySelectorAll('input[type="email"]').length).catch(() => 0);
    const pws = await page.evaluate(() => document.querySelectorAll('input[type="password"]').length).catch(() => 0);
    
    if (!blocked && (url.includes('/auth/'))) {
      // Page loaded! Even if inputs aren't visible yet (e.g., "Sign up with email" step)
      console.error('CF_PASSED at ' + (i * 2) + 's emails=' + emails + ' pws=' + pws + ' url=' + url.substring(url.lastIndexOf('/')));
      return true;
    }
    
    if (i === 0) {
      console.error('CF_DETECTED: Waiting for manual resolution (browser window is visible)...');
    }
    if (i % 30 === 0 && i > 0) {
      console.error('CF_WAITING: ' + (i * 2 / 60) + ' min elapsed. Please click the Cloudflare checkbox in the browser window.');
    }
  }
  console.error('CF_MANUAL_TIMEOUT: 10 minutes passed without resolution.');
  return false;
}

// ── register ──

async function registerAccount(page, account) {
  if (!(await navigateToAuth(page))) {
    return { ok: false, error: 'cloudflare_blocked_auth_page' };
  }
  await sleep(3000);

  // Check if we're already on a sign-up page or if it shows a login page
  const url = page.url();
  if (url.includes('/auth/login')) {
    // Click "Sign up" tab
    await clickFirstText(page, ['Sign up', 'Create account', 'Register'], 2000);
    await sleep(2000);
  }

  // Dune's signup flow: first step shows "Sign up with email" button
  // We need to click it before the form appears
  if (url.includes('/auth/register')) {
    await clickFirstText(page, ['Sign up with email', 'Continue with email', 'Email'], 2000);
    await sleep(3000);
  }

  // Detect and fill the form
  console.error('Detecting signup form...');
  const emailInput = await firstVisible(page, [
    'input[autocomplete="email"]',
    'input[placeholder="mail@example.com"]',
    'input[type="email"]',
    'input[placeholder*="email" i]',
  ]);
  const pwInputs = await visibleInputs(page, 'input[type="password"]');

  if (!emailInput) return await detectionFailed(page, 'no email input');
  if (pwInputs.length === 0) return await detectionFailed(page, 'no password input');

  await emailInput.fill(account.email);
  // Username: Dune uses placeholder="satoshi" for username
  const userInput = await firstVisible(page, [
    'input[placeholder="satoshi"]',
    'input[placeholder*="username" i]',
    'input[placeholder*="name" i]',
    'input[name*="username" i]',
    'input[type="text"]:nth-of-type(1)',
  ]);
  if (userInput) await userInput.fill(account.username);
  if (pwInputs.length > 0) {
    await pwInputs[0].fill(account.password);
  }

  const ok = await clickSubmit(page, ['Create account', 'Sign up', 'Create', 'Register', 'Continue', 'Get started', 'Next']);
  if (!ok) return await detectionFailed(page, 'no submit button');
  await sleep(4000);

  // After first submit (might redirect to verification page or show next step)
  const pwInputs2 = await visibleInputs(page, 'input[type="password"]');
  if (pwInputs2.length > 0 && pwInputs.length === 0) {
    await pwInputs2[0].fill(account.password);
    await clickSubmit(page, ['Create account', 'Sign up', 'Continue', 'Next']);
    await sleep(4000);
  }

  // Wait for redirect — check both URL and page content
  for (let i = 0; i < 20; i++) {
    await sleep(1000);
    if (await isBlocked(page)) {
      console.error('CF_AFTER_SUBMIT');
      await solveTurnstile(page);
      if (!(await isBlocked(page))) continue;
      await clickSubmit(page, ['Sign up', 'Create', 'Continue', 'Next']);
      await sleep(3000);
    }
    const u = page.url();
    if (u.includes('verify') || u.includes('check-email') || u.includes('confirm')) break;
    if (u.includes('login') || u.includes('signin')) break;
    // Check for success messages in page body
    const b = await page.evaluate(() => document.body?.innerText?.substring(0, 500) || '').catch(() => '');
    if (b.includes('Check your email') || b.includes('check your inbox') || b.includes('verify your email')
        || b.includes('Confirmation email') || b.includes('We sent you')) {
      console.error('REGISTER_CONFIRMATION body text detected');
      break;
    }
    if (b.includes('already exists') || b.includes('already taken') || b.includes('already registered')) {
      return { ok: false, error: 'email_already_registered' };
    }
  }

  if (await isBlocked(page)) return { ok: false, error: 'cloudflare_after_submit' };
  const body = await page.evaluate(() => document.body?.innerText?.substring(0, 500)).catch(() => '');
  // Success: Dune should have sent verification email
  if (body.includes('Check your email') || body.toLowerCase().includes('check your inbox')
      || body.toLowerCase().includes('verify your email') || body.toLowerCase().includes('we sent you')) {
    return { ok: true };
  }
  // If we ended up on login page, registration might have auto-logged-in
  if (page.url().includes('/login') || page.url().includes('/signin') || page.url().includes('/queries')) {
    return { ok: true };
  }
  if (body.includes('error') && !body.toLowerCase().includes('check your email')) {
    return { ok: false, error: 'registration failed: ' + body.substring(0, 200) };
  }
  // Assume success if no explicit error
  return { ok: true };
}

// ── login + extract ──

async function loginAndExtract(browser, page, account) {
  if (!(await navigateToAuth(page))) {
    return { ok: false, error: 'cloudflare_blocked_auth_page' };
  }
  await sleep(3000);

  // Check if we need to click "Log in" tab
  const url = page.url();
  if (url.includes('/auth/register')) {
    await clickFirstText(page, ['Log in', 'Sign in'], 2000);
    await sleep(2000);
  }

  // Dune login: click "Log in with email" first
  await clickFirstText(page, ['Log in with email', 'Continue with email', 'Email'], 2000);
  await sleep(3000);

  // Login form: username/email uses autocomplete="username", password is type="password"
  let emailInput = await firstVisible(page, [
    'input[autocomplete="username"]',
    'input[placeholder="satoshi"]',
    'input[autocomplete="email"]',
    'input[type="email"]',
    'input[placeholder*="email" i]',
    'input[placeholder*="mail" i]',
  ]);
  let pwInput = await firstVisible(page, ['input[type="password"]']);
  if (!emailInput || !pwInput) return await detectionFailed(page, 'login form not found');

  await emailInput.fill(account.email);
  await pwInput.fill(account.password);
  await clickSubmit(page, ['Log in', 'Sign in', 'Continue']);
  await sleep(5000);

  // Handle post-login flows
  let loginAttempts = 0;
  let usernameDone = false;
  while (loginAttempts < 8) {
    const url = page.url();
    const body = await page.evaluate(() => document.body?.innerText?.substring(0, 300) || '').catch(() => '');

    if (await isBlocked(page)) {
      console.error('CF_AFTER_LOGIN');
      await solveTurnstile(page);
      await clickSubmit(page, ['Log in', 'Sign in', 'Continue']);
      await sleep(3000);
      loginAttempts++;
      continue;
    }

    // Check for username setup page (Dune asks for username after first login)
    if (!usernameDone && (body.includes('Choose a Dune username') || body.includes('Please provide a username'))) {
      console.error('USERNAME_SETUP');
      usernameDone = true;
      const usernameInput = await firstVisible(page, ['input[placeholder="satoshi"]', 'input[autocomplete="username"]', 'input[name="username"]']);
      if (usernameInput) {
        await usernameInput.fill(account.username);
        await clickSubmit(page, ['Continue', 'Next', 'Save', 'Submit']);
        await sleep(5000);
      }
      loginAttempts++;
      continue;
    }

    // If Dune lands on welcome/home, extract credentials instead of chasing auth forms.
    if (isLoggedInSurface(url, body)) {
      const creds = await extractCredentials(browser, page);
      if (hasCapturedAuth(creds)) {
        console.error('LOGIN_SUCCESS_EXTRACTED url=' + url);
        return creds;
      }
    }

    // Check for onboarding / team setup / interests pages
    if (body.includes('Skip') || body.includes('Get started') || body.includes('Maybe later')
        || body.includes('Create a team') || body.includes('Select your interests')
        || body.includes('Welcome') || body.includes('Complete your profile')) {
      console.error('ONBOARD_STEP');
      await clickFirstText(page, ['Skip', 'Next', 'Continue', 'Get started', 'Maybe later', 'Not now', 'Create', 'Save'], 1000);
      await sleep(2000);
      loginAttempts++;
      continue;
    }

    // Success: we're past login — look for Dune dashboard indicators
    if (url.includes('/queries') || url.includes('/discover') || url.includes('/home')
        || body.includes('My Queries') || body.includes('Discover') || body.includes('Dashboard')) {
      console.error('LOGIN_SUCCESS url=' + url);
      break;
    }

    // If we see a login form again, we may need to retry
    if (body.includes('Log in') && (body.includes('email') || body.includes('password'))) {
      console.error('LOGIN_RETRY');
      const emailEl = await firstVisible(page, ['input[autocomplete="username"]', 'input[type="email"]']);
      const pwEl = await firstVisible(page, ['input[type="password"]']);
      if (emailEl && pwEl) {
        await emailEl.fill(account.email);
        await pwEl.fill(account.password);
        await clickSubmit(page, ['Log in', 'Sign in', 'Continue']);
        await sleep(5000);
      }
      loginAttempts++;
      continue;
    }

    await sleep(2000);
    loginAttempts++;
  }

  // Final onboarding skip — up to 30 iterations
  for (let i = 0; i < 30; i++) {
    await sleep(1000);
    const url = page.url();
    if (url.includes('/welcome') || url.includes('/queries') || url.includes('/discover') || url.includes('/home')) {
      console.error('REACHED_DASHBOARD');
      break;
    }
    const body = await page.evaluate(() => document.body?.innerText?.substring(0, 300) || '').catch(() => '');
    if (isLoggedInSurface(url, body)) break;
    if (await clickFirstText(page, ['Skip', 'Next', 'Continue', 'Get started', 'Maybe later', 'Not now'], 800)) {
      console.error('SKIPPED');
      continue;
    }
    const close = page.locator('[aria-label="Close"], [data-testid="close"]').first();
    if (await close.isVisible({ timeout: 500 }).catch(() => false)) { await close.click(); console.error('CLOSED_MODAL'); }
  }

  let cookies = [];
  let cookieStr = '';
  let auth = '';
  try {
    if (browser && typeof browser.cookies === 'function') {
      cookies = await browser.cookies();
    }
  } catch (e) {
    console.error('COOKIE_EXTRACT_FAIL ' + e.message);
  }
  if (cookies.length > 0) {
    const dc = cookies.filter(c => c.domain && c.domain.includes('dune.com'));
    cookieStr = dc.map(c => c.name + '=' + c.value).join('; ');
    const idToken = dc.find(c => c.name === 'auth-id-token');
    auth = idToken ? 'Bearer ' + idToken.value : '';
    console.error('COOKIES_EXTRACTED ' + dc.length + ' idToken=' + !!idToken);
  }

  let accessToken = '';
  try {
    accessToken = await page.evaluate(() => {
      for (let i = 0; i < localStorage.length; i++) {
        const k = localStorage.key(i);
        if (k && k.includes('accessToken') && k.includes('Cognito')) return localStorage.getItem(k);
      }
      return '';
    });
  } catch {}

  let teamId = 0;
  if (auth) {
    try {
      const r = await page.evaluate(async (a) => {
        const res = await fetch('/public/graphql?operationName=GetTeams', { method:'POST', headers:{'Content-Type':'application/json', 'Authorization':a}, body:JSON.stringify({operationName:'GetTeams',query:'query GetTeams{teams{edges{node{id}}}}',variables:{},extensions:{clientLibrary:{name:'@apollo/client',version:'4.1.6'}}}), credentials:'include' });
        return res.json();
      }, auth);
      teamId = r?.data?.teams?.edges?.[0]?.node?.id || 0;
    } catch {}
  }

  return { ok: true, cookie: cookieStr, authorization: auth, access_token: accessToken, team_id: teamId };
}

// ── verify email + login ──

async function verifyAndLogin(browser, page, link, account) {
  try {
  // Step 1: Open verification link
  console.error('Opening verification link...');
  await navigateWithCFRetry(page, link, 5);
  await sleep(5000);
  if (await isBlocked(page)) {
    for (let i = 0; i < 30; i++) { await sleep(2000); if (!(await isBlocked(page))) break; }
    if (await isBlocked(page)) return { ok: false, error: 'cloudflare_blocked_verify_page' };
  }
  console.error('Verification link opened');

  // Step 2: Check if already logged in (Dune sets auth cookies during verification)
  await page.goto(DUNE_HOME, { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
  await sleep(3000);
  let body = await page.evaluate(() => document.body?.innerText?.substring(0, 300) || '').catch(() => '');
  if (isLoggedInSurface(page.url(), body)) {
    console.error('ALREADY_LOGGED_IN');
    return await extractCredentials(browser, page);
  }

  // Step 3: Try explicit login
  console.error('Trying login page...');
  await page.goto('https://dune.com/auth/login', { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
  await sleep(3000);
  await clickFirstText(page, ['Log in with email', 'Continue with email', 'Email'], 2000);
  await sleep(3000);

  let emailInput = await firstVisible(page, [
    'input[autocomplete="username"]', 'input[autocomplete="email"]', 'input[type="email"]',
  ]);
  let pwInput = await firstVisible(page, ['input[type="password"]']);
  if (!emailInput || !pwInput) {
    body = await page.evaluate(() => document.body?.innerText?.substring(0, 300) || '').catch(() => '');
    if (isLoggedInSurface(page.url(), body)) return await extractCredentials(browser, page);
    // Fallback: go home, click login
    await page.goto(DUNE_HOME, { waitUntil: 'networkidle', timeout: 15000 }).catch(() => {});
    await sleep(2000);
    await clickFirstText(page, ['Log in', 'Sign in'], 2000);
    await sleep(2000);
    await clickFirstText(page, ['Log in with email', 'Continue with email'], 2000);
    await sleep(2000);
    emailInput = await firstVisible(page, ['input[autocomplete="username"]', 'input[type="email"]']);
    pwInput = await firstVisible(page, ['input[type="password"]']);
  }

  if (emailInput && pwInput) {
    await emailInput.fill(account.email);
    await pwInput.fill(account.password);
    await clickSubmit(page, ['Log in', 'Sign in', 'Continue']);
    await sleep(5000);
    for (let i = 0; i < 15; i++) {
      await sleep(1000);
      const b2 = await page.evaluate(() => document.body?.innerText?.substring(0, 300) || '').catch(() => '');
      if (isLoggedInSurface(page.url(), b2)) break;
      await clickFirstText(page, ['Skip', 'Next', 'Continue', 'Get started', 'Maybe later'], 800);
    }
  }
  return await extractCredentials(browser, page);
  } catch (e) {
    console.error('VERIFY_FATAL ' + (e.message || e));
    return { ok: false, error: 'verify_login crashed: ' + String(e.message || e).substring(0, 200) };
  }
}

async function extractCredentials(browser, page) {
  let cookies = [];
  let cookieStr = '';
  let auth = '';
  try {
    if (browser && typeof browser.cookies === 'function') {
      cookies = await browser.cookies();
    }
  } catch (e) {
    console.error('COOKIE_EXTRACT_FAIL ' + e.message);
  }
  if (cookies.length > 0) {
    const dc = cookies.filter(c => c.domain && c.domain.includes('dune.com'));
    cookieStr = dc.map(c => c.name + '=' + c.value).join('; ');
    const idToken = dc.find(c => c.name === 'auth-id-token');
    auth = idToken ? 'Bearer ' + idToken.value : '';
    console.error('COOKIES_EXTRACTED ' + dc.length + ' idToken=' + !!idToken);
  }

  let accessToken = '';
  try {
    accessToken = await page.evaluate(() => {
      for (let i = 0; i < localStorage.length; i++) {
        const k = localStorage.key(i);
        if (k && k.includes('accessToken') && k.includes('Cognito')) return localStorage.getItem(k);
      }
      return '';
    });
  } catch {}

  let teamId = 0;
  if (auth) {
    try {
      const r = await page.evaluate(async (a) => {
        const res = await fetch('/public/graphql?operationName=GetTeams', { method:'POST', headers:{'Content-Type':'application/json', 'Authorization':a}, body:JSON.stringify({operationName:'GetTeams',query:'query GetTeams{teams{edges{node{id}}}}',variables:{},extensions:{clientLibrary:{name:'@apollo/client',version:'4.1.6'}}}), credentials:'include' });
        return res.json();
      }, auth);
      teamId = r?.data?.teams?.edges?.[0]?.node?.id || 0;
    } catch {}
  }

  return { ok: true, cookie: cookieStr, authorization: auth, access_token: accessToken, team_id: teamId };
}

// ── main ──

async function main() {
  const input = JSON.parse(readFileSync(0, 'utf8'));
  const mode = requiredString(input.mode, 'mode');
  const account = {
    email: (mode === 'capture') ? 'capture@dune.local' : requiredString(input.email, 'email'),
    username: (mode === 'capture') ? '' : requiredString(input.username, 'username'),
    password: (mode === 'capture') ? 'p' : requiredString(input.password, 'password'),
  };
  const profileDir = requiredString(input.profileDir, 'profileDir');
  const proxyServer = typeof input.proxyServer === 'string' ? input.proxyServer.trim() : '';
  mkdirSync(dirname(profileDir), { recursive: true });

  const ww = randWindowWidth();
  const wh = randWindowHeight();
  const launchOpts = {
    headless: false,
    args: [
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-blink-features=AutomationControlled',
      '--disable-features=IsolateOrigins,site-per-process',
      '--window-position=' + rand(50, 300) + ',' + rand(50, 200),
      '--window-size=' + ww + ',' + wh,
    ],
    ignoreDefaultArgs: ['--enable-automation'],
    viewport: { width: ww, height: wh },
  };
  if (proxyServer) launchOpts.proxy = { server: proxyServer };
  const channel = typeof input.channel === 'string' ? input.channel.trim() : '';
  if (channel) launchOpts.channel = channel;

  const browser = await chromium.launchPersistentContext(profileDir, launchOpts);
  try {
    const page = await browser.newPage();
    page.setDefaultTimeout(45000);

    // For automated auth modes: clear existing Dune auth cookies from shared profile
    // so we don't inherit a previous account's login session
    if (mode === 'register' || mode === 'verify' || mode === 'login') {
      try {
        const existing = await browser.cookies();
        for (const c of existing) {
          if (c.domain && c.domain.includes('dune.com') && c.name.toLowerCase().startsWith('auth-')) {
            await browser.clearCookies({ name: c.name, domain: c.domain, path: c.path || '/' });
            console.error('CLEARED_COOKIE ' + c.name);
          }
        }
      } catch (e) { console.error('CLEAR_COOKIE_ERR ' + e.message); }
    }

    // Inject cookies from auth.json (cf_clearance etc.)
    const injectCookie = typeof input.cookie === 'string' ? input.cookie.trim() : '';
    if (injectCookie) {
      const pairs = injectCookie.split(';').map(p => p.trim()).filter(p => p.includes('='));
      const duneCookies = pairs.map(p => {
        const eqIdx = p.indexOf('=');
        return { name: p.substring(0, eqIdx).trim(), value: p.substring(eqIdx + 1).trim(), domain: '.dune.com', path: '/' };
      }).filter(c => shouldInjectCookie(c.name, mode));
      if (duneCookies.length > 0) {
        await browser.addCookies(duneCookies);
        console.error('COOKIES_INJECTED ' + duneCookies.length);
      }
    }

    if (mode === 'verify') {
      const link = requiredString(input.verifyLink, 'verifyLink');
      writeJSON(await verifyAndLogin(browser, page, link, account));
    } else if (mode === 'register' || mode === 'login') {
      // Navigate to dune.com first (establishes session, passes CF), then click through to auth
      console.error('Loading homepage...');
      const homeOk = await navigateWithCFRetry(page, DUNE_HOME, 5);
      if (!homeOk) {
        console.error('CF_BLOCKED_HOMEPAGE');
        return writeJSON({ ok: false, error: 'cloudflare_blocked_homepage', html: (await page.content().catch(() => '')).substring(0, 500) });
      }
      console.error('Homepage loaded: ' + (await page.title()));

      if (mode === 'register') {
        console.error('Registering account...');
        writeJSON(await registerAccount(page, account));
      } else {
        writeJSON(await loginAndExtract(browser, page, account));
      }
      return;
    } else if (mode === 'capture') {
      // Capture: open browser, user manually logs in, extract + save to auth.json
      console.error('CAPTURE: Open https://dune.com/home. Login manually. Waiting 10min...');
      await page.goto('https://dune.com/home', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
      for (let i = 0; i < 120; i++) {
        await sleep(5000);
        const b = await page.evaluate(() => document.body?.innerText?.substring(0, 300) || '').catch(() => '');
        const u = page.url();
        if (isLoggedInSurface(u, b)) {
          const creds = await extractCredentials(browser, page);
          if (hasCapturedAuth(creds)) {
            saveCapturedAuth(creds);
            writeJSON(creds);
            return;
          }
          console.error('CAPTURE_AUTH_EMPTY url=' + u);
        }
      }
      writeJSON({ ok: false, error: 'capture_timeout' });
    } else {
      throw new Error('unknown mode: ' + mode);
    }
  } finally {
    await browser.close().catch(() => {});
  }
}

main().catch(e => { console.error('FATAL', e.message); process.exit(1); });
