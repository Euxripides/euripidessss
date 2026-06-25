// Dune Playwright Bridge — handles GraphQL and REST requests via headless browser.
// Reads JSON from stdin, navigates to dune.com for Cloudflare, makes the request, writes JSON to stdout.
// modes: "graphql" (with operationName, query, variables, extensions) or "execution" (with body)

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');
const {
    STEALTH_ARGS,
    STEALTH_HEADERS,
    STEALTH_USER_AGENT,
    STEALTH_VIEWPORT,
    applyStealthConfig,
    solveCloudflareWithStealth,
} = require(path.resolve(__dirname, '..', '..', '..', 'tools', 'dune-playwright', 'stealth-config.cjs'));

const DEFAULT_PROFILE_DIR = path.resolve(__dirname, 'playwright-profile');
const DUNE_GRAPHQL_URL = 'https://dune.com/public/graphql';
const DUNE_EXECUTION_URL = 'https://dune.com/public/execution';
const DUNE_HOME = 'https://dune.com/';

function firstNonEmpty(...values) {
    for (const value of values) {
        if (typeof value === 'string' && value.trim()) return value.trim();
    }
    return '';
}

function buildLaunchOptions() {
    const opts = {
        headless: false,
        args: [
            ...STEALTH_ARGS,
            '--window-position=120,120', '--window-size=1100,800',
        ],
        ignoreDefaultArgs: ['--enable-automation'],
        userAgent: STEALTH_USER_AGENT,
        viewport: STEALTH_VIEWPORT,
        locale: 'zh-CN',
        timezoneId: 'Asia/Shanghai',
        permissions: ['geolocation'],
        colorScheme: 'light',
        deviceScaleFactor: 1,
        javaScriptEnabled: true,
        bypassCSP: true,
        ignoreHTTPSErrors: true,
        extraHTTPHeaders: STEALTH_HEADERS,
    };
    const chromePath = firstNonEmpty(process.env.DUNE_QUERY_CHROME_PATH, process.env.DUNE_CHROME_PATH);
    if (chromePath) {
        opts.executablePath = chromePath;
        return opts;
    }
    let channel = 'chrome';
    channel = firstNonEmpty(process.env.DUNE_QUERY_CHANNEL, process.env.DUNE_BATCH_CHANNEL) || channel;
    if (channel) opts.channel = channel;
    return opts;
}

function resolveCDPEndpoint() {
    const explicit = firstNonEmpty(process.env.DUNE_QUERY_CDP_URL, process.env.DUNE_CHROME_CDP_URL);
    if (explicit) return explicit;
    const port = firstNonEmpty(process.env.DUNE_QUERY_CDP_PORT, process.env.DUNE_CHROME_CDP_PORT);
    if (!port) return '';
    return 'http://127.0.0.1:' + port;
}

async function launchDuneContext(queryProfileDir) {
    const cdpEndpoint = resolveCDPEndpoint();
    if (cdpEndpoint) {
        const browser = await chromium.connectOverCDP(cdpEndpoint);
        const context = browser.contexts()[0] || await browser.newContext();
        return { context, cdp: true, close: async () => {} };
    }
    const launchOpts = buildLaunchOptions();
    try {
        const context = await chromium.launchPersistentContext(path.resolve(queryProfileDir), launchOpts);
        return { context, cdp: false, close: async () => { await context.close(); } };
    } catch (err) {
        if (!launchOpts.channel && !launchOpts.executablePath) throw err;
        process.stderr.write('CHROME_CHANNEL_FALLBACK: ' + (err.message || String(err)) + '\n');
        const fallbackOpts = { ...launchOpts };
        delete fallbackOpts.channel;
        delete fallbackOpts.executablePath;
        const context = await chromium.launchPersistentContext(path.resolve(queryProfileDir), fallbackOpts);
        return { context, cdp: false, close: async () => { await context.close(); } };
    }
}

async function isCloudflarePage(page) {
    const title = await page.title().catch(() => '');
    if (title === 'Just a moment...' || title === '请稍候…' || title === '请稍候...') return true;
    if (title.startsWith('Attention Required')) return true;
    const text = await page.evaluate(() => document.body?.innerText?.substring(0, 250) || '').catch(() => '');
    if (text.startsWith('Just a moment') || text.startsWith('请稍候') || text.startsWith('Sorry, you have been blocked')) return true;
    const html = await page.content().catch(() => '');
    return html.includes('challenges.cloudflare.com') || html.includes('cf-turnstile-response') || html.includes('_cf_chl_opt');
}

async function waitForManualCloudflare(page, maxSeconds) {
    process.stderr.write('MANUAL_CF: Cloudflare detected. Please click the challenge in the browser window.\n');
    process.stderr.write('MANUAL_CF: The request will continue automatically after verification.\n');
    const rounds = Math.ceil(maxSeconds / 2);
    for (let i = 0; i < rounds; i++) {
        await new Promise(resolve => setTimeout(resolve, 2000));
        if (!(await isCloudflarePage(page))) {
            process.stderr.write('CF_MANUAL_PASSED at ' + ((i + 1) * 2) + 's\n');
            return true;
        }
        if (i > 0 && i % 30 === 0) {
            process.stderr.write('CF_WAITING: ' + (i * 2 / 60) + ' min elapsed. Please click Cloudflare in the browser window.\n');
        }
    }
    process.stderr.write('CF_MANUAL_TIMEOUT\n');
    return false;
}

async function main() {
    const stdin = fs.readFileSync(0, 'utf-8');
    let input;
    try { input = JSON.parse(stdin); }
    catch (e) {
        process.stderr.write(JSON.stringify({ error: 'invalid json input: ' + e.message }));
        process.exit(1);
    }

    const { mode = 'graphql', operationName, query, variables, extensions, body: execBody, timeoutMs = 30000, cookie, authorization: goAuth, accessToken: goToken } = input;
    const queryProfileDir = firstNonEmpty(input.profileDir, process.env.DUNE_QUERY_PROFILE_DIR) || DEFAULT_PROFILE_DIR;

    let browserSession;
    try {
        browserSession = await launchDuneContext(queryProfileDir);
        const browser = browserSession.context;

        let page = browser.pages().find(p => p.url().includes('dune.com')) || await browser.newPage();
        if (!browserSession.cdp) await applyStealthConfig(page);

        // Set Dune cookies from input first (so navigation refreshes them)
        if (cookie) {
            const pairs = cookie.split(';').map(p => p.trim()).filter(p => p.includes('='));
            const duneCookies = pairs.map(p => {
                const [name, ...rest] = p.split('=');
                return { name: name.trim(), value: rest.join('=').trim(), domain: '.dune.com', path: '/' };
            });
            if (duneCookies.length > 0) await browser.addCookies(duneCookies);
        }

        const shouldNavigateHome = !browserSession.cdp || !page.url().includes('dune.com');
        if (shouldNavigateHome) {
            try {
                await page.goto(DUNE_HOME, { waitUntil: 'domcontentloaded', timeout: Math.min(timeoutMs, 30000) });
            } catch (_) {}
        }
        if (await isCloudflarePage(page)) {
            if (!(await solveCloudflareWithStealth(page, page.context(), Math.max(timeoutMs, 60000)))) {
                process.stderr.write(JSON.stringify({ error: 'cloudflare_stealth_timeout' }));
                process.exit(1);
            }
        }

        // Refresh mode: just return fresh tokens (after navigation refreshed them)
        if (mode === 'refresh') {
            const cookies = await browser.cookies();
            const cookieStr = cookies.filter(c => c.domain.includes('dune.com')).map(c => c.name + '=' + c.value).join('; ');
            const idToken = cookies.find(c => c.name === 'auth-id-token');
            const authorization = idToken ? 'Bearer ' + idToken.value : '';
            let accessToken = '';
            try {
                accessToken = await page.evaluate(() => {
                    for (let i = 0; i < localStorage.length; i++) {
                        const k = localStorage.key(i);
                        if (k && k.includes('accessToken') && k.includes('Cognito')) return localStorage.getItem(k);
                    }
                    return '';
                });
            } catch (_) {}
            process.stdout.write(JSON.stringify({ cookie: cookieStr, authorization, access_token: accessToken }));
            if (browserSession.cdp) process.exit(0);
            return;
        }

        let url, reqHeaders = { 'Content-Type': 'application/json', 'Origin': 'https://dune.com' };
        let reqBody;
        if (goAuth) reqHeaders['Authorization'] = goAuth;
        if (goToken) reqHeaders['X-Dune-Access-Token'] = goToken;

        if (mode === 'graphql') {
            url = DUNE_GRAPHQL_URL + '?operationName=' + encodeURIComponent(operationName);
            reqBody = JSON.stringify({ operationName, query, variables: variables || {}, extensions: extensions || { clientLibrary: { name: '@apollo/client', version: '4.1.6' } } });
        } else if (mode === 'execution') {
            url = DUNE_EXECUTION_URL;
            reqBody = typeof execBody === 'string' ? execBody : JSON.stringify(execBody);
        } else {
            process.stderr.write(JSON.stringify({ error: 'unknown mode: ' + mode }));
            process.exit(1);
        }

        const result = await page.evaluate(async ({ url, body, headers, timeoutMs }) => {
            const controller = new AbortController();
            const timer = setTimeout(() => controller.abort(), timeoutMs);
            try {
                const res = await fetch(url, {
                    method: 'POST',
                    headers,
                    body,
                    signal: controller.signal,
                    credentials: 'include',
                });
                const text = await res.text();
                return { status: res.status, body: text };
            } finally { clearTimeout(timer); }
        }, { url, body: reqBody, headers: reqHeaders, timeoutMs });

        if (result.status < 200 || result.status >= 300) {
            process.stderr.write(JSON.stringify({
                error: 'Dune HTTP ' + result.status,
                status: result.status,
                body: result.body.substring(0, 2000),
            }));
            process.exit(1);
        }

        process.stdout.write(result.body);
        if (browserSession.cdp) process.exit(0);
    } catch (e) {
        process.stderr.write(JSON.stringify({ error: e.message || String(e) }));
        process.exit(1);
    } finally {
        if (browserSession && !browserSession.cdp) await browserSession.close().catch(() => {});
    }
}

main();
