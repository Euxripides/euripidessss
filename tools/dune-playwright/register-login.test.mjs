import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import assert from 'node:assert/strict';

const source = readFileSync(join(import.meta.dirname, 'register-login.mjs'), 'utf8');
const stealthSource = readFileSync(join(import.meta.dirname, 'stealth-config.cjs'), 'utf8');
const bridgeSource = readFileSync(join(import.meta.dirname, '..', '..', 'backend', 'data', 'dune', 'playwright_bridge.js'), 'utf8');
const queryAccountSource = readFileSync(join(import.meta.dirname, '..', '..', 'internal', 'api', 'dune_account_query.go'), 'utf8');

test('capture mode stays independent from register and login flows', () => {
  assert.match(source, /}\s*else if \(mode === 'capture'\) \{/);
});

test('captured auth is saved under backend data, not tools/backend', () => {
  assert.doesNotMatch(source, /process\.cwd\(\),\s*'\.\.',\s*'backend'/);
  assert.match(source, /process\.cwd\(\),\s*'\.\.',\s*'\.\.',\s*'backend'/);
});

test('automatic auth flows do not inject saved Dune login cookies', () => {
  assert.match(source, /function shouldInjectCookie\(name, mode\)/);
  assert.match(source, /lower\.startsWith\('auth-'\)/);
});

test('login bridge supports silent headless mode for background query auth', () => {
  assert.match(source, /const headless = input\.headless === true;/);
  assert.match(source, /headless,/);
});

test('automatic query login keeps browser visible for manual Cloudflare clicks', () => {
  assert.match(source, /const headless = input\.headless === true;/);
  assert.match(queryAccountSource, /Headless:\s*false/, 'Go query login should pass headless=false so the user can click Cloudflare');
});

test('homepage Cloudflare goes directly to manual user resolution without stealth clicking', () => {
  assert.match(source, /async function waitForManualCloudflareOnly\(page, label, maxSeconds = 600\)/);
  assert.match(source, /waitForManualCloudflareOnly\(page, 'homepage', 600\)/);
  assert.ok(
    source.indexOf("homeOk = await waitForManualCloudflareOnly(page, 'homepage', 600);") < source.indexOf("console.error('CF_BLOCKED_HOMEPAGE');"),
    'homepage should wait for manual Cloudflare resolution before returning CF_BLOCKED_HOMEPAGE',
  );
  assert.ok(
    source.indexOf("await page.goto(DUNE_HOME, { waitUntil: 'domcontentloaded'") < source.indexOf('waitForManualCloudflareOnly(page, \'homepage\''),
    'homepage should navigate to DUNE_HOME before waiting for manual Cloudflare',
  );
});

test('welcome onboarding can be completed before credential extraction', () => {
  assert.match(source, /async function completeWelcomeOnboarding\(page, account, maxSteps = 12\)/);
  assert.match(source, /async function fillWelcomeInputs\(page, account\)/);
  assert.match(source, /async function clickWelcomeChoices\(page\)/);
  assert.match(source, /WELCOME_CHOICES/);
  assert.match(source, /WELCOME_FILLED/);
  assert.match(source, /WELCOME_GOTO_HOME/);
  assert.match(source, /const generic = Array\.from\(main\.querySelectorAll/);
});

test('welcome generic choices skip navigation and action buttons', () => {
  assert.match(source, /const blockedActionTexts = \[/);
  assert.match(source, /'back'/);
  assert.match(source, /'previous'/);
  assert.match(source, /'go back'/);
  assert.match(source, /'返回'/);
  assert.match(source, /'上一步'/);
  assert.match(source, /if \(isActionControl\(body\)\) continue;/);
});

test('welcome text inputs use short values so next can enable', () => {
  const valuesBlock = source.match(/const welcomeInputValues = \{([\s\S]*?)\};/);
  assert.ok(valuesBlock, 'expected named short welcome input fallbacks');
  const values = [...valuesBlock[1].matchAll(/: '([^']+)'/g)].map((match) => match[1]);
  assert.ok(values.length >= 4, 'expected multiple welcome input fallback values');
  for (const value of values) {
    assert.ok(value.length <= 5, `welcome fallback "${value}" must be at most 5 characters`);
  }
  assert.match(source, /normalizeWelcomeInputValue\(username\)/);
});

test('welcome onboarding fills fields before skip or generic choices', () => {
  assert.match(source, /async function clickWelcomeAction\(page, actionTexts\)/);
  assert.match(source, /aria-disabled'\) === 'true'/);
  assert.match(source, /clickWelcomeAction\(page, continueTexts\)/);
  assert.doesNotMatch(source, /clickFirstText\(page, skipTexts/);
  assert.doesNotMatch(source, /clickFirstText\(page, continueTexts/);
  const completeBody = source.match(/async function completeWelcomeOnboarding\(page, account, maxSteps = 12\) \{([\s\S]*?)\n\}/);
  assert.ok(completeBody, 'expected completeWelcomeOnboarding body');
  const body = completeBody[1];
  assert.ok(
    body.indexOf('const filled = await fillWelcomeInputs(page, account);') < body.indexOf('clickWelcomeAction(page, continueTexts)'),
    'welcome flow should fill text inputs before trying Continue/Next',
  );
  assert.ok(
    body.indexOf('clickWelcomeAction(page, continueTexts)') < body.indexOf('clickWelcomeAction(page, skipTexts)'),
    'welcome flow should prefer enabled Continue/Next after fill before Skip',
  );
  assert.ok(
    body.indexOf('clickWelcomeAction(page, skipTexts)') < body.indexOf('const choices = await clickWelcomeChoices(page);'),
    'welcome flow should try explicit Skip before generic choices',
  );
});

test('login verify and capture flows handle welcome before extracting credentials', () => {
  const completeCallCount = (source.match(/completeWelcomeOnboarding\(page, account/g) || []).length;
  assert.ok(completeCallCount >= 6, `expected welcome handler calls before extraction, got ${completeCallCount}`);
  assert.match(source, /ALREADY_LOGGED_IN[\s\S]*completeWelcomeOnboarding\(page, account, 10\)[\s\S]*extractCredentials/);
  assert.match(source, /mode === 'capture'[\s\S]*completeWelcomeOnboarding\(page, account, 10\)[\s\S]*extractCredentials/);
});

test('cloudflare verification uses shared stealth config before navigation', () => {
  assert.match(stealthSource, /STEALTH_ARGS/);
  assert.match(stealthSource, /disable-blink-features=AutomationControlled/);
  assert.match(stealthSource, /navigator,\s*'webdriver'/);
  assert.match(stealthSource, /challenges\.cloudflare\.com/);
  assert.match(stealthSource, /cf_clearance/);
  assert.match(source, /applyStealthConfig\(page\)/);
  assert.match(source, /solveCloudflareWithStealth\(page, page\.context\(\)/);
  const mainLaunchSection = source.slice(source.indexOf('const browser = await chromium.launchPersistentContext'));
  assert.ok(
    mainLaunchSection.indexOf('await applyStealthConfig(page);') < mainLaunchSection.indexOf("page.goto(DUNE_HOME, { waitUntil: 'domcontentloaded'"),
    'stealth init script must be installed before Dune navigation',
  );
});

test('query bridge applies the same stealth config before Dune navigation', () => {
  assert.match(bridgeSource, /stealth-config\.cjs/);
  assert.match(bridgeSource, /STEALTH_ARGS/);
  assert.match(bridgeSource, /applyStealthConfig\(page\)/);
  assert.match(bridgeSource, /solveCloudflareWithStealth\(page, page\.context\(\)/);
  assert.ok(
    bridgeSource.indexOf('await applyStealthConfig(page);') < bridgeSource.indexOf('page.goto(DUNE_HOME'),
    'query bridge must install stealth scripts before opening Dune',
  );
});

test('shared stealth solver does not treat clearance cookie alone as success', () => {
  assert.match(stealthSource, /const blocked = await isCloudflareSurface\(page\)/);
  assert.match(stealthSource, /if \(!blocked && await hasCloudflareClearance\(context\)\)/);
});

test('query bridge relies on stealth instead of manual Cloudflare clicks', () => {
  const cfBranch = bridgeSource.match(/if \(await isCloudflarePage\(page\)\) \{([\s\S]*?)\n        \}/);
  assert.ok(cfBranch, 'expected query bridge Cloudflare branch');
  assert.match(cfBranch[1], /solveCloudflareWithStealth\(page, page\.context\(\),/);
  assert.doesNotMatch(cfBranch[1], /waitForManualCloudflare/);
  assert.match(cfBranch[1], /cloudflare_stealth_timeout/);
});

test('query bridge can reuse a Chrome-like profile chosen by the operator', () => {
  assert.match(bridgeSource, /DUNE_QUERY_PROFILE_DIR/);
  assert.match(bridgeSource, /DEFAULT_PROFILE_DIR/);
  assert.match(bridgeSource, /path\.resolve\(queryProfileDir\)/);
});

test('query bridge prefers Google Chrome and falls back to bundled Chromium', () => {
  assert.match(bridgeSource, /DUNE_QUERY_CHANNEL/);
  assert.match(bridgeSource, /DUNE_CHROME_PATH/);
  assert.match(bridgeSource, /channel = 'chrome'/);
  assert.match(bridgeSource, /CHROME_CHANNEL_FALLBACK/);
});

test('query bridge can attach to an operator-controlled Chrome CDP session', () => {
  assert.match(bridgeSource, /DUNE_QUERY_CDP_URL/);
  assert.match(bridgeSource, /DUNE_QUERY_CDP_PORT/);
  assert.match(bridgeSource, /connectOverCDP/);
  assert.match(bridgeSource, /cdp: true/);
  assert.match(bridgeSource, /if \(!browserSession\.cdp\)/);
  assert.match(bridgeSource, /if \(browserSession\.cdp\) process\.exit\(0\);/);
});
