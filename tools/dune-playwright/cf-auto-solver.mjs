// Screen capture + CF detection + OS-level click script
// Uses PowerShell for screenshot and Win32 SendInput for undetectable clicks
import { chromium } from 'playwright';
import { execSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.cwd();
const PROFILE_DIR = join(ROOT, 'backend', 'data', 'dune', 'profiles', 'auto_solver');

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

// ── PowerShell helpers for screen capture and OS click ──

function psExec(script) {
  try {
    return execSync(`powershell -NoProfile -Command "${script.replace(/"/g, '\\"').replace(/\n/g, ' ')}"`, { encoding: 'utf8', timeout: 5000 }).trim();
  } catch { return ''; }
}

// Take screenshot of Chrome window, detect CF page
function detectCF() {
  const script = `
    Add-Type -AssemblyName System.Drawing
    $h = (Get-Process chrome -ErrorAction SilentlyContinue | Where-Object { \$_.MainWindowTitle -match 'Dune|Just a moment|请稍候|新的标签' } | Select-Object -First 1)
    if (-not \$h) { return 'NO_WINDOW' }
    Add-Type @"
      using System; using System.Runtime.InteropServices; using System.Drawing;
      public class Scr {
        [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
        [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
        [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
      }
      public struct RECT { public int L,T,R,B; }
"@
    \$rect = New-Object RECT
    [Scr]::GetWindowRect(\$h.MainWindowHandle, [ref]\$rect)
    \$w = \$rect.R - \$rect.L; \$h2 = \$rect.B - \$rect.T
    if (\$w -lt 100 -or \$h2 -lt 100) { return 'TOO_SMALL' }
    
    # Capture screenshot of the window
    \$bmp = New-Object Drawing.Bitmap \$w, \$h2
    \$g = [Drawing.Graphics]::FromImage(\$bmp)
    \$g.CopyFromScreen(\$rect.L, \$rect.T, 0, 0, (New-Object Drawing.Size \$w, \$h2))
    \$g.Dispose()
    
    # Check for CF indicators: dark background + "Just a moment" text area is lighter
    # Sample center-top area (where CF title text usually is)
    \$centerX = [int](\$w/2)
    \$titleY = [int](\$h2 * 0.25)
    \$pixel = \$bmp.GetPixel(\$centerX, \$titleY)
    
    # CF page has dark background (R<60, G<60, B<60) and center area has light widget
    \$centerPixel = \$bmp.GetPixel(\$centerX, [int](\$h2/2))
    \$darkBg = (\$pixel.R -lt 80 -and \$pixel.G -lt 80 -and \$pixel.B -lt 80)
    
    # Check if center area has a bright rectangle (Turnstile widget ~ white bg)
    \$widgetCenter = \$bmp.GetPixel(\$centerX, [int](\$h2 * 0.55))
    \$hasWidget = (\$widgetCenter.R -gt 200 -and \$widgetCenter.G -gt 200 -and \$widgetCenter.B -gt 200)
    
    # Also check for blue Cloudflare branding colors at bottom
    \$cfBlue = \$bmp.GetPixel(\$centerX, [int](\$h2 * 0.9))
    \$hasBlue = (\$cfBlue.B -gt 150 -and \$cfBlue.R -lt 100 -and \$cfBlue.G -lt 100)
    
    \$isCF = (\$darkBg -and \$hasWidget) -or (\$darkBg -and \$hasBlue)
    \$result = if (\$isCF) { "CF_DETECTED" } else { "NORMAL_PAGE" }
    
    # Save screenshot for debugging
    \$dir = Join-Path \$PSScriptRoot "backend" "data" "dune" "screenshots" -Resolve -ErrorAction SilentlyContinue
    if (-not \$dir) { \$dir = "E:\\codex\\etl\\backend\\data\\dune\\screenshots"; mkdir \$dir -Force -ErrorAction SilentlyContinue }
    \$path = Join-Path \$dir "cf_detect_$(Get-Date -Format yyyyMMddHHmmss).png"
    \$bmp.Save(\$path)
    
    \$bmp.Dispose()
    \$result + ',' + \$rect.L + ',' + \$rect.T + ',' + \$rect.R + ',' + \$rect.B + ',' + \$path
  `;
  const out = psExec(script);
  if (!out || out.startsWith('NO_WINDOW') || out.startsWith('TOO_SMALL')) return null;
  const parts = out.split(',');
  return {
    isCF: parts[0] === 'CF_DETECTED',
    left: parseInt(parts[1]), top: parseInt(parts[2]),
    right: parseInt(parts[3]), bottom: parseInt(parts[4]),
    screenshot: parts.slice(5).join(','),
  };
}

// Click at specific screen position using SendInput (harder to detect than mouse_event)
function clickScreen(x, y) {
  const script = `
    Add-Type @"
      using System; using System.Runtime.InteropServices;
      public struct INPUT { public uint type; public MOUSEINPUT mi; }
      public struct MOUSEINPUT { public int dx; public int dy; public uint mouseData; public uint dwFlags; public uint time; public IntPtr dwExtraInfo; }
      public class Snd {
        [DllImport("user32.dll")] public static extern uint SendInput(uint nInputs, INPUT[] pInputs, int cbSize);
        [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
      }
"@
    [Snd]::SetCursorPos(${x}, ${y})
    Start-Sleep -Milliseconds 50
    
    \$click = New-Object INPUT; \$click.type = 0
    \$click.mi.dwFlags = 0x0002
    [Snd]::SendInput(1, @(\$click), [System.Runtime.InteropServices.Marshal]::SizeOf(\$click))
    Start-Sleep -Milliseconds 80
    \$click.mi.dwFlags = 0x0004
    [Snd]::SendInput(1, @(\$click), [System.Runtime.InteropServices.Marshal]::SizeOf(\$click))
    "CLICKED"
  `;
  const out = psExec(script);
  return out.includes('CLICKED');
}

// Click at the Turnstile checkbox position relative to window
function clickTurnstile(rect) {
  // Turnstile widget is centered horizontally, about 45-55% vertically
  const cx = rect.left + Math.floor(rect.width / 2);
  const cy = rect.top + Math.floor(rect.height * 0.52);
  console.error('CLICK Turnstile at ' + cx + ',' + cy);
  return clickScreen(cx, cy);
}

// ── Main ──

async function main() {
  mkdirSync(PROFILE_DIR, { recursive: true });
  const browser = await chromium.launchPersistentContext(PROFILE_DIR, {
    headless: false,
    args: ['--no-sandbox', '--window-position=200,100', '--window-size=1000,700', '--disable-blink-features=AutomationControlled'],
    ignoreDefaultArgs: ['--enable-automation'],
    viewport: { width: 1000, height: 700 },
  });
  
  try {
    const page = await browser.newPage();
    console.error('Loading dune.com...');
    await page.goto('https://dune.com/', { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => {});
    await sleep(3000);
    
    // Poll for CF detection
    for (let round = 0; round < 60; round++) {
      await sleep(2000);
      
      // Check if page already passed CF
      const title = await page.title().catch(() => '');
      const url = page.url();
      if (title && title !== 'Just a moment...' && title !== '请稍候…' && url.includes('dune.com')) {
        console.error('CF_PASSED title=' + title);
        // Navigate to register page
        await page.goto('https://dune.com/auth/register', { waitUntil: 'networkidle', timeout: 20000 }).catch(() => {});
        await sleep(3000);
        if (!(await checkCF(page))) {
          console.log('SUCCESS_CF_BYPASSED');
          process.exit(0);
        }
      }
      
      // Try screenshot-based CF detection + click
      const result = detectCF();
      if (result && result.isCF) {
        console.error('CF_DETECTED at round ' + round + ' screenshot=' + result.screenshot);
        
        // Click the Turnstile checkbox at center of window
        if (clickTurnstile({ left: result.left, top: result.top, width: result.right - result.left, height: result.bottom - result.top })) {
          console.error('TURNSTILE_CLICKED');
          await sleep(4000);
          
          // Try clicking at slightly different positions too
          clickScreen(result.left + Math.floor((result.right - result.left) / 2), result.top + Math.floor((result.bottom - result.top) * 0.48));
          await sleep(2000);
          clickScreen(result.left + Math.floor((result.right - result.left) / 2), result.top + Math.floor((result.bottom - result.top) * 0.56));
          await sleep(2000);
        }
      } else if (result) {
        console.error('NO_CF round=' + round + ' screenshot=' + result.screenshot);
      } else {
        console.error('NO_WINDOW round=' + round);
      }
      
      if (round % 10 === 9) console.error('Still waiting... round ' + (round+1) + '/60');
    }
    console.log('TIMEOUT');
  } finally {
    await browser.close().catch(() => {});
  }
}

async function checkCF(page) {
  const title = await page.title().catch(() => '');
  return title === 'Just a moment...' || title === '请稍候…';
}

main().catch(e => { console.error('FATAL ' + e.message); process.exit(1); });
