export function sleep(ms) {
  return new Promise(r => setTimeout(r, ms));
}

export async function firstVisible(page, ss, ms) {
  for (const s of ss) {
    const locator = page.locator(s);
    const count = Math.min(await locator.count().catch(() => 0), 20);
    for (let i = 0; i < count; i++) {
      const el = locator.nth(i);
      if (await el.isVisible({ timeout: ms || 3000 }).catch(() => false)) return el;
    }
  }
  return null;
}

export async function clickFirstSelector(page, ss, ms) {
  const el = await firstVisible(page, ss, ms);
  if (!el) return false;
  await el.click();
  await sleep(1000);
  return true;
}

export async function clickFirstText(page, texts, ms) {
  for (const t of texts) {
    const ok = await clickFirstSelector(page, [
      `button:has-text("${t}")`,
      `a:has-text("${t}")`,
      `text=${t}`,
    ], Math.min(ms || 2000, 2000));
    if (ok) return true;
  }
  return false;
}

export async function clickSubmit(page, texts) {
  for (const t of texts) {
    const b = page.locator('button[type="submit"]:has-text("' + t + '"), button:has-text("' + t + '")').first();
    if (await b.isVisible({ timeout: 2000 }).catch(() => false)) {
      await b.click();
      return true;
    }
  }
  return false;
}

export async function visibleInputs(page, sel) {
  const inputs = await page.locator(sel).all();
  const visible = [];
  for (const input of inputs) {
    if (await input.isVisible().catch(() => false)) visible.push(input);
  }
  return visible;
}

export async function detectionFailed(page, msg) {
  const h = await page.content().catch(() => '');
  console.error('DETECT_FAIL', msg, h.substring(0, 400));
  return { ok: false, error: 'detection_failed: ' + msg };
}
