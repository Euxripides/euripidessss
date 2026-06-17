// Dune GraphQL Playwright Bridge
// Reads GraphQL request from stdin, executes via hidden browser, writes JSON to stdout.
// Navigates to dune.com first to get fresh tokens via the persistent profile's cookies,
// then extracts accessToken from localStorage and makes the GraphQL request.

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const PROFILE_DIR = path.resolve(__dirname, 'playwright-profile');
const DUNE_GRAPHQL_URL = 'https://dune.com/public/graphql';
const DUNE_HOME = 'https://dune.com/';

async function main() {
    const stdin = fs.readFileSync(0, 'utf-8');
    let input;
    try {
        input = JSON.parse(stdin);
    } catch (e) {
        process.stderr.write(JSON.stringify({ error: 'invalid json input: ' + e.message }));
        process.exit(1);
    }

    const { operationName, query, variables, extensions, timeoutMs = 30000, cookie, authorization: goAuth, accessToken: goToken } = input;
    if (!operationName || !query) {
        process.stderr.write(JSON.stringify({ error: 'operationName and query are required' }));
        process.exit(1);
    }

    const body = {
        operationName,
        query,
        variables: variables || {},
        extensions: extensions || { clientLibrary: { name: '@apollo/client', version: '4.1.6' } },
    };

    // Use Go-provided credentials if available, otherwise get from browser
    let authorization = goAuth || '';
    let accessToken = goToken || '';

    let browser;
    try {
        browser = await chromium.launchPersistentContext(PROFILE_DIR, {
            headless: false,
            args: [
                '--no-sandbox',
                '--disable-setuid-sandbox',
                '--disable-blink-features=AutomationControlled',
                '--disable-features=IsolateOrigins,site-per-process',
                '--window-position=-32000,-32000',
                '--window-size=800,600',
            ],
            ignoreDefaultArgs: ['--enable-automation'],
        });

        // Set Dune auth cookies from input (overrides profile cookies for dune.com)
        if (cookie) {
            const pairs = cookie.split(';').map(p => p.trim()).filter(p => p.includes('='));
            const duneCookies = pairs.map(p => {
                const [name, ...rest] = p.split('=');
                return { name: name.trim(), value: rest.join('=').trim(), domain: '.dune.com', path: '/' };
            });
            if (duneCookies.length > 0) {
                await browser.addCookies(duneCookies);
            }
        }

        const page = await browser.newPage();

        // Navigate to dune.com to pass Cloudflare challenge
        try {
            await page.goto(DUNE_HOME, { waitUntil: 'networkidle', timeout: Math.min(timeoutMs, 30000) });
        } catch (_) {
            // Timeout on Cloudflare page is OK - we just need browser clearance
        }

        // If Go didn't provide tokens, try to get them from browser session
        if (!authorization || !accessToken) {
            const browserCookies = await browser.cookies();
            const idToken = browserCookies.find(c => c.name === 'auth-id-token');
            if (!authorization && idToken && idToken.value) {
                authorization = 'Bearer ' + idToken.value;
            }
            if (!accessToken) {
                try {
                    accessToken = await page.evaluate(() => {
                        for (let i = 0; i < localStorage.length; i++) {
                            const k = localStorage.key(i);
                            if (k && k.includes('accessToken') && k.includes('Cognito')) {
                                return localStorage.getItem(k);
                            }
                        }
                        return localStorage.getItem('accessToken') || '';
                    });
                } catch (_) {}
            }
        }

        if (!authorization && !accessToken) {
            process.stderr.write(JSON.stringify({ error: 'Could not obtain Dune auth tokens' }));
            process.exit(1);
        }

        const url = DUNE_GRAPHQL_URL + '?operationName=' + encodeURIComponent(operationName);

        const fetchHeaders = { 'Content-Type': 'application/json' };
        if (authorization) fetchHeaders['Authorization'] = authorization;
        if (accessToken) fetchHeaders['X-Dune-Access-Token'] = accessToken;

        const response = await page.evaluate(async ({ url, body, headers, timeoutMs }) => {
            const controller = new AbortController();
            const timer = setTimeout(() => controller.abort(), timeoutMs);
            try {
                const res = await fetch(url, {
                    method: 'POST',
                    headers: headers,
                    body: JSON.stringify(body),
                    signal: controller.signal,
                    credentials: 'include',
                });
                const text = await res.text();
                return { status: res.status, body: text };
            } finally {
                clearTimeout(timer);
            }
        }, { url, body, headers: fetchHeaders, timeoutMs });

        if (response.status < 200 || response.status >= 300) {
            process.stderr.write(JSON.stringify({
                error: 'Dune GraphQL returned HTTP ' + response.status,
                status: response.status,
                body: response.body.substring(0, 2000),
            }));
            process.exit(1);
        }

        process.stdout.write(response.body);
    } catch (e) {
        process.stderr.write(JSON.stringify({ error: e.message || String(e) }));
        process.exit(1);
    } finally {
        if (browser) await browser.close().catch(() => {});
    }
}

main();
