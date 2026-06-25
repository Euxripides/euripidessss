// Intercept Cognito API call during signup form submission
import { chromium } from 'playwright';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.cwd();
const AUTH = JSON.parse(readFileSync(join(ROOT, 'backend', 'data', 'dune', 'auth.json'), 'utf8'));
const PROFILE_DIR = join(ROOT, 'backend', 'data', 'dune', 'profiles', 'js_sniffer');

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }
function parseCookies(s) {
  return s.split(';').map(p => p.trim()).filter(p => p.includes('=')).map(p => {
    const i = p.indexOf('=');
    return { name: p.substring(0,i).trim(), value: p.substring(i+1).trim(), domain: '.dune.com', path: '/' };
  });
}

async function main() {
  mkdirSync(PROFILE_DIR, { recursive: true });
  const browser = await chromium.launchPersistentContext(PROFILE_DIR, {
    headless: false,
    args: ['--no-sandbox', '--disable-blink-features=AutomationControlled'],
    ignoreDefaultArgs: ['--enable-automation'],
  });

  let cognitoPayloads = [];
  
  try {
    if (AUTH.cookie) await browser.addCookies(parseCookies(AUTH.cookie));
    const page = await browser.newPage();

    // Intercept ALL fetch/XHR to capture Cognito calls
    await page.route('**/*', (route, request) => {
      const url = request.url();
      if (url.includes('cognito-idp')) {
        const data = request.postData() || '';
        cognitoPayloads.push({ url, method: request.method(), postData: data, headers: request.headers() });
        console.error('COGNITO_CAPTURED ' + url + ' ' + data.substring(0, 2000));
      }
      route.continue();
    });

    // Load homepage then register page
    console.error('Loading pages...');
    await page.goto('https://dune.com/', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
    for (let i = 0; i < 20; i++) { await sleep(1000); if ((await page.title().catch(()=>'')) !== 'Just a moment...') break; }
    await page.goto('https://dune.com/auth/register', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
    for (let i = 0; i < 20; i++) { await sleep(1000); if ((await page.title().catch(()=>'')) !== 'Just a moment...') break; }
    console.error('Register page title: ' + (await page.title().catch(()=>'')));

    // Click "Sign up with email"
    await page.locator('button:has-text("Sign up"), a:has-text("Sign up"), button:has-text("email"), a:has-text("email")').first().click({ timeout: 5000 }).catch(() => {});
    await sleep(3000);

    // Fill the form with a test email
    const email = 'ldj1009538134+intercept_test@gmail.com';
    const emailInput = page.locator('input[type="email"]').first();
    if (await emailInput.isVisible({ timeout: 5000 }).catch(() => false)) {
      await emailInput.fill(email);
      console.error('Email filled');
    }

    const pwInputs = page.locator('input[type="password"]');
    const pwCount = await pwInputs.count().catch(() => 0);
    if (pwCount > 0) {
      await pwInputs.first().fill('Test1234!@#$');
      console.error('Password filled');
    }

    // Username
    const userInput = page.locator('input[placeholder="satoshi"]').first();
    if (await userInput.isVisible({ timeout: 3000 }).catch(() => false)) {
      await userInput.fill('u_intercept_test');
      console.error('Username filled');
    } else {
      const textInput = page.locator('input[type="text"]').first();
      if (await textInput.isVisible({ timeout: 2000 }).catch(() => false)) {
        await textInput.fill('u_intercept_test');
        console.error('Username filled (fallback)');
      }
    }

    // Submit the form - this should trigger the Cognito SignUp API call
    console.error('Submitting form...');
    const submitBtn = page.locator('button[type="submit"]').first();
    if (await submitBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
      await submitBtn.click();
      console.error('Submit clicked');
    }
    await sleep(5000);

    // Output results
    if (cognitoPayloads.length > 0) {
      console.log('COGNITO_PAYLOADS: ' + JSON.stringify(cognitoPayloads, null, 2));
    } else {
      // Maybe the form wasn't fully rendered. Try clicking "Create account" etc.
      console.error('No Cognito calls yet, trying more buttons...');
      const btns = page.locator('button');
      const count = await btns.count().catch(() => 0);
      for (let i = 0; i < count; i++) {
        const text = await btns.nth(i).innerText().catch(() => '');
        if (text.includes('Sign') || text.includes('Create') || text.includes('Register') || text.includes('Continue')) {
          await btns.nth(i).click().catch(() => {});
          console.error('Clicked: ' + text);
          await sleep(3000);
          if (cognitoPayloads.length > 0) break;
        }
      }
      console.log('COGNITO_PAYLOADS: ' + JSON.stringify(cognitoPayloads, null, 2));
    }
  } finally {
    await browser.close().catch(() => {});
  }
}

main().catch(e => { console.error('FATAL', e.message); process.exit(1); });
