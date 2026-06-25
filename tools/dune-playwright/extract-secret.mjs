// Fetch Dune JS chunks and search for Cognito client secret
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.cwd();
const AUTH = JSON.parse(readFileSync(join(ROOT, 'backend', 'data', 'dune', 'auth.json'), 'utf8'));
const COOKIE_STR = AUTH.cookie || '';
const PROFILE_DIR = join(ROOT, 'backend', 'data', 'dune', 'profiles', 'ldj1009538134_dune_2d685f01_at_gmail_com');

function parseCookies(str) {
  return str.split(';').map(p => p.trim()).filter(p => p.includes('=')).map(p => {
    const idx = p.indexOf('=');
    return { name: p.substring(0, idx).trim(), value: p.substring(idx + 1).trim(), domain: '.dune.com', path: '/' };
  });
}

// Known Dune chunk URLs from previous extraction
const CHUNKS = [
  'https://dune.com/_next/static/chunks/2yp5xlu03moeu.js',
  'https://dune.com/_next/static/chunks/2ff9zrhj4fcsw.js',
  'https://dune.com/_next/static/chunks/0uhulnmc6aunq.js',
  'https://dune.com/_next/static/chunks/2ajildtuxjjul.js',
  'https://dune.com/_next/static/chunks/2df1wul28lgcr.js',
  'https://dune.com/_next/static/chunks/43a786fjc6ct7.js',
  'https://dune.com/_next/static/chunks/1_6q31vmxp1p4.js',
];

async function main() {
  const browser = await chromium.launchPersistentContext(PROFILE_DIR, {
    headless: false,
    args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-blink-features=AutomationControlled'],
    ignoreDefaultArgs: ['--enable-automation'],
  });

  try {
    if (COOKIE_STR) await browser.addCookies(parseCookies(COOKIE_STR));
    
    const page = await browser.newPage();
    
    // Load homepage to establish CF clearance in this session
    console.error('Loading homepage for CF clearance...');
    await page.goto('https://dune.com/', { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => {});
    await page.waitForTimeout(5000);
    console.error('Homepage loaded: ' + (await page.title()));

    // Now fetch each JS chunk through the browser's fetch (has CF clearance)
    for (const url of CHUNKS) {
      try {
        console.error('Fetching: ' + url.split('/').pop());
        const text = await page.evaluate(async (u) => {
          const res = await fetch(u);
          if (!res.ok) return 'HTTP ' + res.status;
          return await res.text();
        }, url);
        
        if (text.startsWith('HTTP ')) {
          console.error('  ' + text);
          continue;
        }

        // Search for Cognito config
        const patterns = [
          /clientSecret\s*:\s*["']([^"']{10,})["']/g,
          /UserPoolId\s*:\s*["']([^"']+)["']/g,
          /ClientId\s*:\s*["'](\w+)["']/g,
          /userPoolWebClientId\s*:\s*["'](\w+)["']/g,
          /cognito\.[^}]*clientSecret[^}]*["']([^"']{10,})["']/g,
        ];
        
        for (const p of patterns) {
          let m;
          while ((m = p.exec(text)) !== null) {
            console.log('FOUND: ' + url.split('/').pop() + ' :: ' + p.source.substring(0, 40) + ' :: ' + m[1]);
          }
        }
        
        // Also grep for bare strings near "cognito" or "amplify"
        const cognitoLines = text.split('\n').filter(l => 
          l.includes('cognito') || l.includes('Cognito') || 
          l.includes('UserPool') || l.includes('clientSecret') ||
          l.includes('amplify'));
        if (cognitoLines.length > 0) {
          console.error('  Cognito-related lines: ' + cognitoLines.length);
          for (const line of cognitoLines.slice(0, 5)) {
            console.error('    ' + line.substring(0, 200));
          }
        }
      } catch (e) {
        console.error('  Error: ' + e.message);
      }
    }
  } finally {
    await browser.close().catch(() => {});
  }
}

main().catch(e => { console.error('FATAL', e.message); process.exit(1); });
