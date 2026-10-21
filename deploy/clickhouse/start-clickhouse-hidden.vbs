' ClickHouse logon autostart - hidden window (no Win11 terminal flash)
' Used by HKCU Run value "ClickHouseBSC".
Set shell = CreateObject("WScript.Shell")
target = "E:\codex\etl\deploy\clickhouse\start-clickhouse.ps1"
command = "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File """ & target & """"
' Delay so the desktop finishes loading before the WSL/ClickHouse work starts (ms).
WScript.Sleep 60000
shell.Run command, 0, False
