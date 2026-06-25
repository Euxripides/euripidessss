// Quick API sniffer — loads Dune pages with existing cookies, captures all API requests
import { chromium } from 'playwright';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.cwd();
const AUTH_PATH = join(ROOT, 'backend', 'data', 'dune', 'auth.json');
const AUTH = JSON.parse(readFileSync(AUTH_PATH, 'utf8'));
const COOKIE_STR = AUTH.cookie || '';
const PROFILE_DIR = join(ROOT, 'backend', 'data', 'dune', 'profiles', 'ldj1009538134_dune_2d685f01_at_gmail_com'); // reuse profile that passed CF
const OUT_DIR = join(ROOT, 'backend', 'data', 'dune', 'api_captures');
const DUNE_HOME = 'https://dune.com/';

function parseCookies(str) {
  return str.split(';').map(p => p.trim()).filter(p => p.includes('=')).map(p => {
    const idx = p.indexOf('=');
    return { name: p.substring(0, idx).trim(), value: p.substring(idx + 1).trim(), domain: '.dune.com', path: '/' };
  });
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

async function sniff(page) {
  const calls = [];
  page.on('request', req => {
    const url = req.url();
    const method = req.method();
    const postData = req.postData();
    if (method === 'POST' || url.includes('graphql') || url.includes('cognito')
        || url.includes('signUp') || url.includes('signup') || url.includes('register')
        || url.includes('token') || url.includes('oauth') || url.includes('verify')) {
      calls.push({ method, url, postData: postData ? postData.substring(0, 3000) : '', headers: req.headers() });
    }
  });
  page.on('response', async resp => {
    const url = resp.url();
    const match = calls.find(c => c.url === url);
    if (match) {
      try {
        const ct = resp.headers()['content-type'] || '';
        if (ct.includes('json') || ct.includes('text')) {
          match.responseBody = (await resp.text().catch(() => '')).substring(0, 3000);
          match.responseStatus = resp.status();
        }
      } catch {}
    }
  });
  await sleep(3000);
  return calls;
}

async function main() {
  mkdirSync(PROFILE_DIR, { recursive: true });
  mkdirSync(OUT_DIR, { recursive: true });

  const browser = await chromium.launchPersistentContext(PROFILE_DIR, {
    headless: false,
    args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-blink-features=AutomationControlled'],
    ignoreDefaultArgs: ['--enable-automation'],
  });

  const results = {};
  try {
    if (COOKIE_STR) {
      await browser.addCookies(parseCookies(COOKIE_STR));
      console.error('Cookies injected');
    }

    const page = await browser.newPage();
    const calls = sniff(page);

    // 1. Homepage
    console.error('Loading homepage...');
    await page.goto(DUNE_HOME, { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
    await sleep(2000);
    results.home = { title: await page.title().catch(() => ''), calls: await calls };

    // 2. Signup page
    console.error('Loading signup page...');
    await page.goto('https://dune.com/auth/register', { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
    await sleep(3000);
    results.signup = { url: page.url(), calls: await calls };

    // 3. Login page  
    console.error('Loading login page...');
    await page.goto('https://dune.com/auth/login', { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
    await sleep(3000);
    results.login = { url: page.url(), calls: await calls };

    writeFileSync(join(OUT_DIR, 'sniff_result.json'), JSON.stringify(results, null, 2));
    console.error('Saved to ' + join(OUT_DIR, 'sniff_result.json'));
    console.log(JSON.stringify(results, null, 2));
  } finally {
    await browser.close().catch(() => {});
  }
}

main().catch(e => { console.error('FATAL', e.message); process.exit(1); });
