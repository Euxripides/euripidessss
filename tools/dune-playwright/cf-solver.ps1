# CF Turnstile Auto-Solver v2 — Title-based detection + SendInput click
param([switch]$Once)

$code = @'
using System; using System.Runtime.InteropServices;
public class Scr {
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
    [DllImport("user32.dll")] public static extern uint SendInput(uint n, INPUT[] p, int s);
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
    [DllImport("user32.dll")] public static extern int GetWindowText(IntPtr h, System.Text.StringBuilder s, int n);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc p, IntPtr l);
    public delegate bool EnumWindowsProc(IntPtr h, IntPtr l);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
}
public struct RECT { public int L,T,R,B; }
public struct INPUT { public uint type; public MOUSEINPUT mi; }
public struct MOUSEINPUT { public int dx,dy; public uint mouseData,dwFlags,time; public IntPtr extra; }
'@
Add-Type -TypeDefinition $code

function Get-CF-Window {
    $chromePids = (Get-Process chrome -ErrorAction SilentlyContinue).Id
    $found = @()
    $callback = {
        param($hwnd, $lparam)
        $pid = 0; [Scr]::GetWindowThreadProcessId($hwnd, [ref]$pid)
        if ($pid -in $chromePids -and [Scr]::IsWindowVisible($hwnd)) {
            $sb = New-Object System.Text.StringBuilder 256
            [Scr]::GetWindowText($hwnd, $sb, 256)
            $title = $sb.ToString()
            if ($title -match 'Just a moment|请稍候|Attention Required') {
                $rect = New-Object RECT
                [Scr]::GetWindowRect($hwnd, [ref]$rect)
                $found += @{ hwnd=$hwnd; title=$title; left=$rect.L; top=$rect.T; right=$rect.R; bottom=$rect.B }
            }
        }
        return $true
    }
    [Scr]::EnumWindows([Scr+EnumWindowsProc]$callback, [IntPtr]::Zero) | Out-Null
    return $found
}

function Click-CF-Checkbox($win) {
    $w = $win.right - $win.left
    $h = $win.bottom - $win.top
    $cx = $win.left + [int]($w/2)
    $cy = $win.top + [int]($h * 0.52)
    
    Write-Host "CF_DETECTED: $($win.title) at ${cx},${cy}"
    
    [Scr]::SetForegroundWindow($win.hwnd)
    Start-Sleep -Milliseconds 200
    [Scr]::SetCursorPos($cx, $cy)
    Start-Sleep -Milliseconds 80
    
    $down = New-Object INPUT; $down.type = 0; $down.mi.dwFlags = 0x0002
    $up = New-Object INPUT; $up.type = 0; $up.mi.dwFlags = 0x0004
    [Scr]::SendInput(1, @($down), [Runtime.InteropServices.Marshal]::SizeOf($down))
    Start-Sleep -Milliseconds 120
    [Scr]::SendInput(1, @($up), [Runtime.InteropServices.Marshal]::SizeOf($up))
    
    Write-Host "CLICKED at ${cx},${cy}"
}

if ($Once) {
    $wins = Get-CF-Window
    if ($wins.Count -gt 0) { Click-CF-Checkbox $wins[0] }
    else { Write-Host "NO_CF_WINDOW" }
    exit
}

Write-Host "CF Monitor v2 started (title-based detection)"
$round = 0
while ($round -lt 120) {
    Start-Sleep -Seconds 2
    $wins = Get-CF-Window
    if ($wins.Count -gt 0) {
        foreach ($w in $wins) { Click-CF-Checkbox $w }
        Start-Sleep -Seconds 5
    }
    $round++
    if ($round % 30 -eq 0) { Write-Host "Monitor alive round $round/120" }
}
Write-Host "Monitor timeout"
