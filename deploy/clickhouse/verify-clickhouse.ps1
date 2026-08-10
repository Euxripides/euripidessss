$ErrorActionPreference = 'Stop'
$root = 'E:\database\clickhouse'
$distroName = 'clickhouse-bsc'

$required = @('runtime\wsl\clickhouse-bsc', 'migration', 'export_spool', 'backups', 'tmp', 'config')
foreach ($relative in $required) {
    $path = Join-Path $root $relative
    if (-not $path.StartsWith('E:\', [StringComparison]::OrdinalIgnoreCase) -or -not (Test-Path -LiteralPath $path)) {
        throw "STORAGE_POLICY_VIOLATION: $path"
    }
}

$basePath = $null
foreach ($key in Get-ChildItem 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss' -ErrorAction SilentlyContinue) {
    $properties = Get-ItemProperty $key.PSPath
    if ($properties.DistributionName -eq $distroName) {
        $basePath = [string]$properties.BasePath
        break
    }
}
if (-not $basePath -or -not $basePath.StartsWith('E:\', [StringComparison]::OrdinalIgnoreCase)) {
    throw "STORAGE_POLICY_VIOLATION: distro base path is $basePath"
}

& wsl.exe -d $distroName -u root -- bash -lc 'systemctl is-active clickhouse-server >/dev/null || service clickhouse-server status >/dev/null'
if ($LASTEXITCODE -ne 0) { throw 'ClickHouse service is not active.' }

$version = & wsl.exe -d $distroName -u root -- clickhouse-client --query 'SELECT version()'
if ($LASTEXITCODE -ne 0 -or -not $version) { throw 'Native ClickHouse query failed.' }

$tables = & wsl.exe -d $distroName -u root -- clickhouse-client --query "SELECT count() FROM system.tables WHERE database='onchain'"
if ($LASTEXITCODE -ne 0 -or [int]$tables -lt 10) { throw "Schema verification failed: only $tables tables found." }

$ping = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8123/ping' -TimeoutSec 10
if ($ping.StatusCode -ne 200 -or $ping.Content.Trim() -ne 'Ok.') { throw 'HTTP ping failed.' }

$drive = Get-PSDrive -Name E
$freePercent = [math]::Round(($drive.Free / ($drive.Used + $drive.Free)) * 100, 2)
$status = if ($freePercent -gt 20) { 'HEALTHY' } elseif ($freePercent -gt 10) { 'WARNING' } elseif ($freePercent -gt 5) { 'CRITICAL' } else { 'STOP_INGEST' }

[PSCustomObject]@{
    Version = $version.Trim()
    Database = 'onchain'
    Tables = [int]$tables
    Distro = $distroName
    PhysicalStorage = $basePath
    EDriveFreePercent = $freePercent
    StorageStatus = $status
    HttpPing = 'Ok.'
} | Format-List

