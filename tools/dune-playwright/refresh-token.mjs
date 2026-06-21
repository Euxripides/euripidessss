// Dune Token Refresher — keeps tokens fresh by refreshing via Cognito.
// Run once after logging in: node tools/dune-playwright/refresh-token.mjs
// It navigates to dune.com, extracts fresh tokens, saves to auth.json.

const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const PROFILE_DIR = path.resolve(__dirname, '..', '..', 'backend', 'data', 'dune', 'playwright-profile');
const AUTH_FILE = path.resolve(__dirname, '..', '..', 'backend', 'data', 'dune', 'auth.json');

async function refresh() {
    const browser = await chromium.launchPersistentContext(PROFILE_DIR, {
        headless: false,
        args: [
            '--no-sandbox', '--disable-setuid-sandbox',
            '--disable-blink-features=AutomationControlled',
            '--window-position=-32000,-32000', '--window-size=800,600',
        ],
        ignoreDefaultArgs: ['--enable-automation'],
    });

    try {
        const page = await browser.newPage();
        await page.goto('https://dune.com/', { waitUntil: 'networkidle', timeout: 30000 });

        // Check if logged in
        const isLoggedIn = await page.evaluate(() => {
            return document.cookie.includes('auth-user=') && document.cookie.includes('auth-id-token=');
        });

        if (!isLoggedIn) {
            console.error('Not logged in. Please log in to Dune first.');
            await browser.close();
            process.exit(1);
        }

        // Extract fresh tokens
        const cookies = await browser.cookies();
        const cookieStr = cookies
            .filter(c => c.domain.includes('dune.com'))
            .map(c => c.name + '=' + c.value)
            .join('; ');

        const idToken = cookies.find(c => c.name === 'auth-id-token');
        const authorization = idToken ? 'Bearer ' + idToken.value : '';

        const accessToken = await page.evaluate(() => {
            for (let i = 0; i < localStorage.length; i++) {
                const k = localStorage.key(i);
                if (k && k.includes('accessToken') && k.includes('Cognito')) {
                    return localStorage.getItem(k);
                }
            }
            return '';
        });

        // Load existing to preserve team_id
        let auth = {};
        try { auth = JSON.parse(fs.readFileSync(AUTH_FILE, 'utf-8')); } catch (_) {}

        auth.cookie = cookieStr;
        auth.authorization = authorization;
        auth.access_token = accessToken;
        auth.updated_at = new Date().toISOString();

        if (!auth.team_id) {
            // Try to get team_id from FindQuery response
            try {
                const queryId = await page.evaluate(() => {
                    const m = location.pathname.match(/\/queries\/(\d+)/);
                    return m ? parseInt(m[1]) : 0;
                });
                if (queryId > 0) {
                    const resp = await page.evaluate(async (id) => {
                        const res = await fetch('/public/graphql?operationName=FindQuery', {
                            method: 'POST',
                            headers: { 'Content-Type': 'application/json' },
                            body: JSON.stringify({
                                operationName: 'FindQuery',
                                variables: { id },
                                extensions: { clientLibrary: { name: '@apollo/client', version: '4.1.6' } },
                                query: 'query FindQuery($id: Int!) { query(id: $id) { team { id __typename } __typename } }'
                            }),
                            credentials: 'include',
                        });
                        return res.json();
                    }, queryId);
                    if (resp?.data?.query?.team?.id) {
                        auth.team_id = resp.data.query.team.id;
                    }
                }
            } catch (_) {}
        }

        fs.writeFileSync(AUTH_FILE, JSON.stringify(auth, null, '  '), 'utf-8');

        const idPayload = idToken ? JSON.parse(Buffer.from(idToken.value.split('.')[1], 'base64').toString()) : null;
        console.log('Token refreshed!');
        console.log('  User:', idPayload?.email || 'unknown');
        console.log('  Expires:', idPayload ? new Date(idPayload.exp * 1000).toISOString() : 'unknown');
        console.log('  Team ID:', auth.team_id || 'unknown');
        console.log('  Cookie length:', cookieStr.length);
        console.log('  AccessToken length:', accessToken.length);
    } finally {
        await browser.close();
    }
}

refresh().catch(e => { console.error(e.message); process.exit(1); });
