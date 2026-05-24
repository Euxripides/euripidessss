# ETL Go 后端启动脚本
# 用法: .\run.ps1

Write-Host "=== 资金流水自动清洗合并工具 - Go 后端 ===" -ForegroundColor Cyan
Write-Host ""

# Check binary
$binPath = Join-Path $PSScriptRoot "bin\etl-server.exe"
if (-not (Test-Path $binPath)) {
    Write-Host "[构建] 未找到可执行文件，开始构建..." -ForegroundColor Yellow
    try {
        Set-Location $PSScriptRoot
        go build -o $binPath .\cmd\server\
        Write-Host "[构建] 成功" -ForegroundColor Green
    }
    catch {
        Write-Host "[错误] 构建失败: $_" -ForegroundColor Red
        exit 1
    }
}

# Ensure data directories
$dirs = @(
    "backend\data\uploads",
    "backend\data\outputs",
    "backend\data\logs",
    "backend\data\rule_samples",
    "backend\config"
)
foreach ($dir in $dirs) {
    $fullPath = Join-Path $PSScriptRoot $dir
    if (-not (Test-Path $fullPath)) {
        New-Item -ItemType Directory -Force -Path $fullPath | Out-Null
    }
}

Write-Host "[启动] 服务器端口: 8000" -ForegroundColor Green
Write-Host "[启动] 工作目录: $PSScriptRoot" -ForegroundColor Green
Write-Host ""

# Start server
Set-Location $PSScriptRoot
& $binPath
