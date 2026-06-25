// Diagnostic: capture Dune register page
import { chromium } from 'playwright-extra';
import StealthPlugin from 'puppeteer-extra-plugin-stealth';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

chromium.use(StealthPlugin());

const OUT = 'E:/codex/etl/backend/data/dune/diag';

async function main() {
  mkdirSync(OUT, { recursive: true });
  const browser = await chromium.launch({ headless: false, args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  
  // Step 1: Go to homepage
  console.log('1. Loading homepage...');
  await page.goto('https://dune.com/', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(3000);
  await page.screenshot({ path: join(OUT, '1_homepage.png'), fullPage: false });
  writeFileSync(join(OUT, '1_homepage.html'), await page.content());
  console.log('   Title:', await page.title(), 'URL:', page.url());

  // Step 2: Click Sign up
  console.log('2. Clicking Sign up...');
  const signup = page.locator('a[href*="auth/register"], a:has-text("Sign up"), button:has-text("Sign up")').first();
  if (await signup.isVisible({ timeout: 5000 }).catch(() => false)) {
    await signup.click();
  } else {
    await page.goto('https://dune.com/auth/register', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
  }
  await page.waitForTimeout(5000);
  await page.screenshot({ path: join(OUT, '2_register.png'), fullPage: false });
  writeFileSync(join(OUT, '2_register.html'), await page.content());
  console.log('   Title:', await page.title(), 'URL:', page.url());

  // Step 3: Click Sign up with email
  console.log('3. Clicking Sign up with email...');
  const emailBtn = page.locator('button:has-text("email"), button:has-text("Sign up"), a:has-text("email")').first();
  if (await emailBtn.isVisible({ timeout: 5000 }).catch(() => false)) {
    await emailBtn.click();
    await page.waitForTimeout(5000);
  }
  await page.screenshot({ path: join(OUT, '3_form.png'), fullPage: false });
  writeFileSync(join(OUT, '3_form.html'), await page.content());
  console.log('   Title:', await page.title(), 'URL:', page.url());

  // Step 4: List all inputs
  const inputs = await page.evaluate(() => {
    return Array.from(document.querySelectorAll('input, button, [role="button"]')).map(el => ({
      tag: el.tagName,
      type: el.getAttribute('type') || '',
      name: el.getAttribute('name') || '',
      placeholder: el.getAttribute('placeholder') || '',
      autocomplete: el.getAttribute('autocomplete') || '',
      id: el.id || '',
      class: el.className?.substring(0, 100) || '',
      text: (el.textContent || '').substring(0, 50),
      visible: el.offsetParent !== null,
    }));
  });
  writeFileSync(join(OUT, '4_inputs.json'), JSON.stringify(inputs, null, 2));
  console.log('4. Inputs found:', inputs.length);
  inputs.forEach(i => console.log('  ', i.tag, i.type, i.placeholder, i.name, 'visible:', i.visible));

  await browser.close();
  console.log('Done. Check', OUT);
}

main().catch(e => { console.error(e.message); process.exit(1); });
