param(
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$BinPath = Join-Path $ProjectRoot "bin\etl-server.exe"
$Port = 8000
$HealthUrl = "http://127.0.0.1:$Port/api/health"
$Curl = if (Get-Command "curl.exe" -ErrorAction SilentlyContinue) { "curl.exe" } else { "curl" }
$MaxRetry = 3
$RetryInterval = 2

Write-Host "=== ETL Backend Startup ==="

# ---- Build ----
if (-not (Test-Path $BinPath)) {
    if (-not $SkipBuild) {
        Write-Host "[BUILD] Building..."
        Push-Location $ProjectRoot
        try {
            & "go" "build" "-o" $BinPath ".\cmd\server\"
            if ($LASTEXITCODE -ne 0) { throw "Build failed" }
        } finally { Pop-Location }
    } else {
        Write-Host "[ERROR] Binary not found: $BinPath"
        exit 1
    }
}

# ---- Kill old process ----
$oldPid = $null
$proc = Get-Process -Name "etl-server" -ErrorAction SilentlyContinue
if ($proc) {
    $oldPid = $proc.Id
    Write-Host "[STOP] Stopping old process (PID: $oldPid)..."
    Stop-Process -Id $oldPid -Force -ErrorAction SilentlyContinue

    for ($i = 0; $i -lt $MaxRetry; $i++) {
        Start-Sleep -Seconds $RetryInterval
        $p = Get-Process -Id $oldPid -ErrorAction SilentlyContinue
        if (-not $p) {
            Write-Host "[STOP] Old process exited"
            break
        }
        Write-Host "[STOP] Waiting... attempt $($i+2)/$($MaxRetry+1)"
        Stop-Process -Id $oldPid -Force -ErrorAction SilentlyContinue
    }
}

# ---- Port check function ----
function Test-PortFree {
    $result = & $Curl -s --connect-timeout 1 $HealthUrl 2>&1 | Out-String
    return $LASTEXITCODE -ne 0
}

# ---- Wait for port to be free (max ~15s) ----
Write-Host "[CHECK] Waiting for port $Port..."
for ($i = 0; $i -lt 15; $i++) {
    if (Test-PortFree) {
        break
    }
    if ($i -eq 14) {
        Write-Host "[ERROR] Port $Port still occupied after 15s"
        exit 1
    }
    Start-Sleep -Seconds 1
}
Write-Host "[CHECK] Port $Port free"

# ---- Start server ----
Write-Host "[START] Starting server..."
$proc = Start-Process -FilePath $BinPath -WindowStyle Hidden -PassThru
Start-Sleep -Milliseconds 500

# ---- Health check (max 15s) ----
Write-Host "[CHECK] Waiting for server..."
for ($i = 0; $i -lt 15; $i++) {
    Start-Sleep -Seconds 1
    try {
        $body = & $Curl -s --connect-timeout 2 $HealthUrl 2>&1 | Out-String
        if ($LASTEXITCODE -eq 0 -and $body -like '*"status":"ok"*') {
            Write-Host "[CHECK] Server ready (PID: $($proc.Id))"
            exit 0
        }
    } catch {
        # Not ready yet
    }
}
Write-Host "[ERROR] Server not ready after 15s"
exit 1
