param(
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

# ── Phase 5：SQD Cloud 生产模式注入 ──
# Secret 只允许来自环境变量（SQD_DEPLOY_KEY / R2_*），禁止硬编码到脚本或仓库。
if ([string]::IsNullOrEmpty($env:SQD_CLOUD_MODE) -and -not [string]::IsNullOrEmpty($env:SQD_RUNTIME_MODE)) {
    $env:SQD_CLOUD_MODE = $env:SQD_RUNTIME_MODE
}
if ($env:SQD_CLOUD_MODE -eq "cloud") {
    $missing = @()
    foreach ($name in @("SQD_DEPLOY_KEY", "R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY")) {
        if ([string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($name))) { $missing += $name }
    }
    if ($missing.Count -gt 0) {
        Write-Host "[ERROR] SQD_CLOUD_MODE=cloud 需要环境变量: $($missing -join ', ')（禁止硬编码到 run.ps1）"
        exit 1
    }
}
if ([string]::IsNullOrEmpty($env:SQD_CLOUD_ORG)) { $env:SQD_CLOUD_ORG = "supreme" }

$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$BinPath = Join-Path $ProjectRoot "bin\etl-server.exe"
$Port = 8000
$HealthUrl = "http://127.0.0.1:$Port/api/health"
$Curl = if (Get-Command "curl.exe" -ErrorAction SilentlyContinue) { "curl.exe" } else { "curl" }
$MaxRetry = 3
$RetryInterval = 2

Write-Host "=== ETL Backend Startup ==="

# ---- Build ----
if (-not $SkipBuild) {
    Write-Host "[BUILD] Building windows/amd64..."
    Push-Location $ProjectRoot
    $PreviousGoArch = $env:GOARCH
    try {
        $env:GOARCH = "amd64"
        & "go" "build" "-o" $BinPath ".\cmd\server\"
        if ($LASTEXITCODE -ne 0) { throw "Build failed" }
    } finally {
        if ($null -eq $PreviousGoArch) {
            Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        } else {
            $env:GOARCH = $PreviousGoArch
        }
        Pop-Location
    }
} elseif (-not (Test-Path $BinPath)) {
    Write-Host "[ERROR] Binary not found: $BinPath"
    exit 1
}

# ---- Kill old process tree ----
function Get-DescendantProcessIds {
    param([int]$RootProcessId)
    $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$RootProcessId" -ErrorAction SilentlyContinue)
    foreach ($child in $children) {
        Get-DescendantProcessIds -RootProcessId ([int]$child.ProcessId)
        [int]$child.ProcessId
    }
}

$oldProcesses = @(Get-Process -Name "etl-server" -ErrorAction SilentlyContinue)
foreach ($oldProcess in $oldProcesses) {
    $oldProcessId = [int]$oldProcess.Id
    $descendantIds = @(Get-DescendantProcessIds -RootProcessId $oldProcessId)
    Write-Host "[STOP] Stopping old process tree (PID: $oldProcessId, children: $($descendantIds.Count))..."
    foreach ($descendantId in $descendantIds) {
        Stop-Process -Id $descendantId -Force -ErrorAction SilentlyContinue
    }
    Stop-Process -Id $oldProcessId -Force -ErrorAction SilentlyContinue

    for ($i = 0; $i -lt $MaxRetry; $i++) {
        Start-Sleep -Seconds $RetryInterval
        $oldProcessCheck = Get-Process -Id $oldProcessId -ErrorAction SilentlyContinue
        if (-not $oldProcessCheck) {
            Write-Host "[STOP] Old process tree exited"
            break
        }
        Write-Host "[STOP] Waiting... attempt $($i+2)/$($MaxRetry+1)"
        Stop-Process -Id $oldProcessId -Force -ErrorAction SilentlyContinue
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
