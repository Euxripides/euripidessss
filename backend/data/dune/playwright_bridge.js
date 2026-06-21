// Dune Playwright Bridge — handles GraphQL and REST requests via headless browser.
// Reads JSON from stdin, navigates to dune.com for Cloudflare, makes the request, writes JSON to stdout.
// modes: "graphql" (with operationName, query, variables, extensions) or "execution" (with body)

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const PROFILE_DIR = path.resolve(__dirname, 'playwright-profile');
const DUNE_GRAPHQL_URL = 'https://dune.com/public/graphql';
const DUNE_EXECUTION_URL = 'https://dune.com/public/execution';
const DUNE_HOME = 'https://dune.com/';

async function main() {
    const stdin = fs.readFileSync(0, 'utf-8');
    let input;
    try { input = JSON.parse(stdin); }
    catch (e) {
        process.stderr.write(JSON.stringify({ error: 'invalid json input: ' + e.message }));
        process.exit(1);
    }

        const { mode = 'graphql', operationName, query, variables, extensions, body: execBody, timeoutMs = 30000, cookie, authorization: goAuth, accessToken: goToken } = input;

    let browser;
    try {
        browser = await chromium.launchPersistentContext(PROFILE_DIR, {
            headless: false,
            args: [
                '--no-sandbox', '--disable-setuid-sandbox',
                '--disable-blink-features=AutomationControlled',
                '--disable-features=IsolateOrigins,site-per-process',
                '--window-position=-32000,-32000', '--window-size=800,600',
            ],
            ignoreDefaultArgs: ['--enable-automation'],
        });

        let page = await browser.newPage();

        // Set Dune cookies from input first (so navigation refreshes them)
        if (cookie) {
            const pairs = cookie.split(';').map(p => p.trim()).filter(p => p.includes('='));
            const duneCookies = pairs.map(p => {
                const [name, ...rest] = p.split('=');
                return { name: name.trim(), value: rest.join('=').trim(), domain: '.dune.com', path: '/' };
            });
            if (duneCookies.length > 0) await browser.addCookies(duneCookies);
        }

        // Navigate to dune.com to pass Cloudflare challenge and refresh tokens
        try {
            await page.goto(DUNE_HOME, { waitUntil: 'networkidle', timeout: Math.min(timeoutMs, 30000) });
        } catch (_) {}

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
    } catch (e) {
        process.stderr.write(JSON.stringify({ error: e.message || String(e) }));
        process.exit(1);
    } finally {
        if (browser) await browser.close().catch(() => {});
    }
}

main();
