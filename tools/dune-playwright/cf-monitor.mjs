// Standalone: detect CF page via screenshot, click Turnstile checkbox
import { execSync } from 'node:fs';
import { exec } from 'node:child_process';

function ps(cmd) {
  return new Promise((resolve) => {
    exec(`powershell -NoProfile -Command "${cmd}"`, { timeout: 5000 }, (err, stdout) => {
      resolve((stdout || '').trim());
    });
  });
}

// Take screenshot of Chrome window, save it, detect if CF page
async function detectAndClick() {
  const result = await ps(`
    Add-Type -AssemblyName System.Drawing
    $procs = Get-Process chrome -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -match 'Dune|Just a moment|请稍候|新的标签|Google Chrome' } | Select-Object -First 1
    if (-not $procs) { return 'NO_WINDOW' }
    
    $code = @'
using System; using System.Runtime.InteropServices; using System.Drawing;
public class Scr {
[DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
[DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
[DllImport("user32.dll")] public static extern uint SendInput(uint n, INPUT[] p, int s);
}
public struct RECT { public int L,T,R,B; }
public struct INPUT { public uint type; public MOUSEINPUT mi; }
public struct MOUSEINPUT { public int dx,dy; public uint mouseData,dwFlags,time; public IntPtr extra; }
'@
    Add-Type -TypeDefinition $code
    
    $rect = New-Object RECT
    [Scr]::GetWindowRect($procs.MainWindowHandle, [ref]$rect)
    $w = $rect.R - $rect.L; $h = $rect.B - $rect.T
    if ($w -lt 200 -or $h -lt 200) { return 'TOO_SMALL' }
    
    # Capture window screenshot
    $bmp = New-Object Drawing.Bitmap $w, $h
    $g = [Drawing.Graphics]::FromImage($bmp)
    $g.CopyFromScreen($rect.L, $rect.T, 0, 0, (New-Object Drawing.Size $w, $h))
    $g.Dispose()
    
    # Sample pixels: center area (Turnstile widget ~ white), background (dark)
    $cx = [int]($w/2); $cy = [int]($h/2)
    $topPixel = $bmp.GetPixel($cx, [int]($h*0.15))
    $midPixel = $bmp.GetPixel($cx, [int]($h*0.45))
    $widgetPixel = $bmp.GetPixel($cx, [int]($h*0.52))
    $bottomPixel = $bmp.GetPixel($cx, [int]($h*0.9))
    
    $isDark = ($topPixel.R -lt 60 -and $topPixel.G -lt 60 -and $topPixel.B -lt 60)
    $hasWhiteWidget = ($widgetPixel.R -gt 200 -and $widgetPixel.G -gt 200 -and $widgetPixel.B -gt 200)
    $isCF = $isDark -and $hasWhiteWidget
    
    # Save screenshot
    $dir = "E:\\codex\\etl\\backend\\data\\dune\\screenshots"
    mkdir $dir -Force -ErrorAction SilentlyContinue | Out-Null
    $path = Join-Path $dir "cf_$(Get-Date -Format HHmmss).png"
    $bmp.Save($path)
    $bmp.Dispose()
    
    if ($isCF) {
      # Click Turnstile checkbox: center of window at ~52% height
      $clickX = $rect.L + [int]($w/2)
      $clickY = $rect.T + [int]($h*0.52)
      [Scr]::SetCursorPos($clickX, $clickY)
      Start-Sleep -Milliseconds 30
      $down = New-Object INPUT; $down.type = 0; $down.mi.dwFlags = 0x0002
      $up = New-Object INPUT; $up.type = 0; $up.mi.dwFlags = 0x0004
      [Scr]::SendInput(1, @($down), [Runtime.InteropServices.Marshal]::SizeOf($down))
      Start-Sleep -Milliseconds 80
      [Scr]::SendInput(1, @($up), [Runtime.InteropServices.Marshal]::SizeOf($up))
      return "CF_CLICKED,$path"
    }
    return "NO_CF,$path"
  `);
  return result;
}

// Continuous monitoring loop
async function monitor() {
  console.error('CF Monitor started. Watching for Cloudflare verification pages...');
  for (let round = 0; round < 120; round++) {
    await new Promise(r => setTimeout(r, 2000));
    const result = await detectAndClick();
    if (result.startsWith('CF_CLICKED')) {
      console.error('[' + new Date().toISOString() + '] CF DETECTED + CLICKED! ' + result);
      console.log('CF_SOLVED');
    } else if (result.startsWith('NO_CF')) {
      // Silent
    } else if (result.startsWith('NO_WINDOW') || result.startsWith('TOO_SMALL')) {
      // No Chrome window or too small - browser might not be open yet
    }
    if (round % 30 === 29) console.error('Monitor alive, round ' + (round+1));
  }
}

monitor().catch(e => console.error('FATAL ' + e.message));
