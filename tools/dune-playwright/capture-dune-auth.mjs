// Dune Auth Capture — launches visible browser, user logs in,
// then captures Cookie + Authorization + AccessToken to auth.json.

import { chromium } from 'playwright';
import { resolve, dirname } from 'path';
import { writeFileSync, mkdirSync, existsSync } from 'fs';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const PROFILE_DIR = resolve(__dirname, '..', '..', 'backend', 'data', 'dune', 'playwright-profile');
const AUTH_FILE = resolve(__dirname, '..', '..', 'backend', 'data', 'dune', 'auth.json');

async function main() {
    console.log('Launching browser...');
    const browser = await chromium.launchPersistentContext(PROFILE_DIR, {
        headless: false,
        args: [
            '--no-sandbox',
            '--disable-setuid-sandbox',
            '--disable-blink-features=AutomationControlled',
        ],
        ignoreDefaultArgs: ['--enable-automation'],
    });

    const page = await browser.newPage();
    await page.goto('https://dune.com/', { waitUntil: 'networkidle', timeout: 30000 });

    console.log('');
    console.log('========================================');
    console.log('  Browser opened. Please log in to Dune.');
    console.log('  After login, press Enter here to capture.');
    console.log('========================================');
    console.log('');

    // Wait for user to press Enter
    await new Promise(resolve => {
        process.stdin.once('data', () => resolve());
    });

    console.log('Capturing credentials...');

    // Get all cookies
    const cookies = await browser.cookies();
    const cookieStr = cookies
        .filter(c => c.domain.includes('dune.com'))
        .map(c => `${c.name}=${c.value}`)
        .join('; ');

    // Extract auth-id-token for Authorization header
    const idTokenCookie = cookies.find(c => c.name === 'auth-id-token');
    let authorization = '';
    if (idTokenCookie) {
        authorization = `Bearer ${idTokenCookie.value}`;
    }

    // Get access token from localStorage
    let accessToken = '';
    try {
        accessToken = await page.evaluate(() => {
            const keys = ['accessToken', 'dune_access_token', 'CognitoIdentityServiceProvider.FOO.accessToken'];
            for (const key of keys) {
                const val = localStorage.getItem(key);
                if (val) return val;
            }
            // Try to find any Cognito-related key
            for (let i = 0; i < localStorage.length; i++) {
                const k = localStorage.key(i);
                if (k && k.includes('accessToken') && k.includes('Cognito')) {
                    return localStorage.getItem(k);
                }
            }
            return '';
        });
    } catch (e) {
        console.warn('Could not read localStorage:', e.message);
    }

    // Also try sessionStorage
    if (!accessToken) {
        try {
            accessToken = await page.evaluate(() => {
                for (let i = 0; i < sessionStorage.length; i++) {
                    const k = sessionStorage.key(i);
                    if (k && k.includes('accessToken') && k.includes('Cognito')) {
                        return sessionStorage.getItem(k);
                    }
                }
                return '';
            });
        } catch (e) {
            // ignore
        }
    }

    // Build auth.json
    const auth = {
        cookie: cookieStr,
        authorization: authorization,
        access_token: accessToken,
        updated_at: new Date().toISOString(),
    };

    const dir = dirname(AUTH_FILE);
    if (!existsSync(dir)) {
        mkdirSync(dir, { recursive: true });
    }
    writeFileSync(AUTH_FILE, JSON.stringify(auth, null, '  '), 'utf-8');

    console.log('');
    console.log('Credentials saved to:', AUTH_FILE);
    console.log('Cookie length:', cookieStr.length);
    console.log('Authorization:', authorization.substring(0, 50) + '...');
    console.log('AccessToken length:', accessToken.length);
    console.log('');
    console.log('Done! You can close the browser and test Dune query.');

    await browser.close();
}

main().catch(e => {
    console.error('Error:', e.message);
    process.exit(1);
});
