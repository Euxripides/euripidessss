import { chromium } from 'playwright';

const PROFILE = 'E:/codex/etl/backend/data/dune/profiles/ldj1009538134_dune_2d685f01_at_gmail_com';

async function main() {
  const browser = await chromium.launchPersistentContext(PROFILE, {
    headless: false,
    args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-blink-features=AutomationControlled'],
    ignoreDefaultArgs: ['--enable-automation'],
  });

  const page = await browser.newPage();
  await page.goto('https://dune.com/', { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(5000);

  const result = await page.evaluate(async () => {
    const results = {};
    const scripts = document.querySelectorAll('script');
    for (const s of scripts) {
      if (!s.src || !s.src.includes('dune.com/_next')) continue;
      try {
        const res = await fetch(s.src);
        const text = await res.text();

        // Find aws_user_pools_web_client_id
        const cidRe = /aws_user_pools_web_client_id[^"']*["'](\w+)["']/;
        const cidMatch = cidRe.exec(text);
        if (cidMatch) {
          const idx = text.indexOf(cidMatch[0]);
          results.clientId = cidMatch[1];
          results.clientContext = text.substring(Math.max(0, idx - 300), idx + 500);
          results.file = s.src.split('/').pop();
        }

        // Find anything that looks like a Cognito client secret
        // AWS Cognito client secrets are typically 26-52 char base64 strings
        const lines = text.split('\n');
        for (const line of lines) {
          if (line.includes('clientSecret') || line.includes('CLIENT_SECRET') || line.includes('client_secret')) {
            results.secretLine = line.trim().substring(0, 300);
            results.secretFile = s.src.split('/').pop();
            break;
          }
        }

        // Search for Amplify.configure or Auth.configure
        const ampRe = /(?:Amplify|Auth)\.configure\s*\(\s*(\{[^}]*\})/;
        const ampMatch = ampRe.exec(text);
        if (ampMatch) {
          results.amplifyConfig = ampMatch[1].substring(0, 500);
        }

        if (results.clientId && results.secretLine) break;
      } catch {}
    }
    return results;
  });

  console.log(JSON.stringify(result, null, 2));
  await browser.close();
}

main().catch(e => { console.error('FATAL', e.message); process.exit(1); });
