param(
    [switch]$SkipBuild,
    [switch]$ForceBuild,
    [switch]$ForceRestart,
    [ValidateRange(15, 600)]
    [int]$StartupTimeoutSeconds = 180
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
if ($SkipBuild -and $ForceBuild) {
    Write-Host "[ERROR] -SkipBuild and -ForceBuild cannot be used together"
    exit 1
}

$BuildRequired = -not $SkipBuild
if ($BuildRequired -and -not $ForceBuild -and (Test-Path -LiteralPath $BinPath)) {
    $BinModifiedAt = (Get-Item -LiteralPath $BinPath).LastWriteTimeUtc
    $SourceRoots = @(
        (Join-Path $ProjectRoot "cmd"),
        (Join-Path $ProjectRoot "internal")
    )
    $ChangedSource = Get-ChildItem -LiteralPath $SourceRoots -File -Recurse -ErrorAction Stop |
        Where-Object { $_.LastWriteTimeUtc -gt $BinModifiedAt } |
        Select-Object -First 1
    if (-not $ChangedSource) {
        foreach ($ModuleFile in @("go.mod", "go.sum")) {
            $ModulePath = Join-Path $ProjectRoot $ModuleFile
            if ((Test-Path -LiteralPath $ModulePath) -and (Get-Item -LiteralPath $ModulePath).LastWriteTimeUtc -gt $BinModifiedAt) {
                $ChangedSource = Get-Item -LiteralPath $ModulePath
                break
            }
        }
    }
    if (-not $ChangedSource) {
        $BuildRequired = $false
        Write-Host "[BUILD] Binary is up to date; skipping rebuild (use -ForceBuild to rebuild)"
    }
}

if ($BuildRequired) {
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
} elseif (-not (Test-Path -LiteralPath $BinPath)) {
    Write-Host "[ERROR] Binary not found: $BinPath"
    exit 1
}

# ---- Healthy no-op ----
# 日常重复执行 run.ps1 时，如果代码未变化且当前服务已经健康，不再无意义地
# 杀进程、重载数万条历史状态。需要明确重启时使用 -ForceRestart。
if (-not $BuildRequired -and -not $ForceRestart) {
    $runningProcesses = @(Get-Process -Name "etl-server" -ErrorAction SilentlyContinue)
    if ($runningProcesses.Count -gt 0) {
        try {
            $body = & $Curl -s --connect-timeout 2 $HealthUrl 2>&1 | Out-String
            if ($LASTEXITCODE -eq 0 -and $body -like '*"status":"ok"*') {
                Write-Host "[CHECK] Server already healthy; no restart needed (PID: $($runningProcesses[0].Id), use -ForceRestart to restart)"
                exit 0
            }
        } catch {
            # Existing process is unhealthy; continue with normal replacement.
        }
    }
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

# ---- Health check ----
$startupWatch = [System.Diagnostics.Stopwatch]::StartNew()
Write-Host "[CHECK] Waiting for server (timeout: ${StartupTimeoutSeconds}s)..."
for ($i = 0; $i -lt $StartupTimeoutSeconds; $i++) {
    Start-Sleep -Seconds 1
    $proc.Refresh()
    if ($proc.HasExited) {
        Write-Host "[ERROR] Server process exited early (PID: $($proc.Id), exit code: $($proc.ExitCode))"
        exit 1
    }
    try {
        $body = & $Curl -s --connect-timeout 2 $HealthUrl 2>&1 | Out-String
        if ($LASTEXITCODE -eq 0 -and $body -like '*"status":"ok"*') {
            $startupWatch.Stop()
            Write-Host "[CHECK] Server ready in $([math]::Round($startupWatch.Elapsed.TotalSeconds, 1))s (PID: $($proc.Id))"
            exit 0
        }
    } catch {
        # Not ready yet
    }
    if (($i + 1) % 10 -eq 0) {
        Write-Host "[CHECK] Still starting... $($i + 1)s elapsed"
    }
}
$startupWatch.Stop()
Write-Host "[ERROR] Server not ready after ${StartupTimeoutSeconds}s (PID: $($proc.Id))"
exit 1
