// OS-level mouse click for CF Turnstile bypass
import { execSync } from 'node:child_process';

// Get the Chrome window position via PowerShell, then click at the CF checkbox position
export function getChromeWindowRect() {
  try {
    const script = `
      Add-Type @"
        using System;
        using System.Runtime.InteropServices;
        public class Win32 {
          [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
          [DllImport("user32.dll")] public static extern IntPtr FindWindow(string lpClassName, string lpWindowName);
          [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
          [DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, UIntPtr dwExtraInfo);
        }
        public struct RECT { public int Left, Top, Right, Bottom; }
"@
      $processes = Get-Process -Name "chrome" -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -match "Dune|Just a moment|请稍候" } | Select-Object -First 1
      if ($processes) {
        $hwnd = $processes.MainWindowHandle
        $rect = New-Object RECT
        [Win32]::GetWindowRect($hwnd, [ref]$rect)
        Write-Output "$($rect.Left),$($rect.Top),$($rect.Right),$($rect.Bottom)"
      } else { Write-Output "NOT_FOUND" }
    `;
    const result = execSync(`powershell -NoProfile -Command "${script.replace(/"/g, '\\"')}"`, { encoding: 'utf8', timeout: 5000 }).trim();
    if (result === 'NOT_FOUND') return null;
    const [left, top, right, bottom] = result.split(',').map(Number);
    return { left, top, right, bottom, width: right - left, height: bottom - top };
  } catch (e) {
    return null;
  }
}

export function clickAtScreen(x, y) {
  try {
    execSync(`powershell -NoProfile -Command "
      Add-Type -AssemblyName System.Windows.Forms
      [System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(${x},${y})
      Add-Type -MemberDefinition '[DllImport(\"user32.dll\")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, UIntPtr dwExtraInfo);' -Name Win32 -Namespace Win32Functions
      [Win32Functions.Win32]::mouse_event(0x0002, 0, 0, 0, [UIntPtr]::Zero)
      Start-Sleep -Milliseconds 50
      [Win32Functions.Win32]::mouse_event(0x0004, 0, 0, 0, [UIntPtr]::Zero)
    "`, { timeout: 3000 });
    return true;
  } catch (e) {
    return false;
  }
}

// Click the CF Turnstile checkbox using OS-level mouse
export function clickCFCheckbox() {
  const rect = getChromeWindowRect();
  if (!rect) return false;
  
  // CF Turnstile checkbox is typically in the center of the viewport
  // Window chrome (title bar, borders) takes about 100px top, 16px sides
  const checkboxX = rect.left + Math.floor(rect.width / 2);
  const checkboxY = rect.top + Math.floor(rect.height / 2) + 50; // Slightly below center
  
  console.error('OS_CLICK at ' + checkboxX + ',' + checkboxY + ' (window: ' + rect.left + ',' + rect.top + ')');
  return clickAtScreen(checkboxX, checkboxY);
}
