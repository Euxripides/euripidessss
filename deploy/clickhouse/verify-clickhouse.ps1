$ErrorActionPreference = 'Stop'
$root = 'E:\database\clickhouse'
$distroName = 'clickhouse-bsc'
$credentialFile = Join-Path $root 'config\clickhouse.env'
$systemSettingsFile = Join-Path $PSScriptRoot '..\..\backend\config\system_settings.json'

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

# The production recovery gate must prove that the application identity can
# write and read through the same HTTP endpoint used by the ETL process. Keep
# credentials in memory only and never include the password in command output.
if (-not (Test-Path -LiteralPath $credentialFile)) {
    throw "ClickHouse credential file is missing: $credentialFile"
}
$clickHouseEnv = @{}
foreach ($line in Get-Content -LiteralPath $credentialFile) {
    if ($line -match '^([^#=]+)=(.*)$') {
        $clickHouseEnv[$matches[1].Trim()] = $matches[2]
    }
}
foreach ($name in @('CLICKHOUSE_USER', 'CLICKHOUSE_PASSWORD', 'CLICKHOUSE_DATABASE')) {
    if (-not $clickHouseEnv.ContainsKey($name) -or [string]::IsNullOrWhiteSpace($clickHouseEnv[$name])) {
        throw "ClickHouse credential file is missing required key: $name"
    }
}

$required = $false
if ($clickHouseEnv.ContainsKey('CLICKHOUSE_REQUIRED')) {
    $required = $clickHouseEnv['CLICKHOUSE_REQUIRED'] -match '^(?i:true|1|yes|on)$'
}
if (Test-Path -LiteralPath $systemSettingsFile) {
    $settings = Get-Content -Raw -LiteralPath $systemSettingsFile | ConvertFrom-Json
    if ($null -ne $settings.clickhouse_required) {
        $required = [bool]$settings.clickhouse_required
    }
}
if (-not $required) {
    throw 'CLICKHOUSE_REQUIRED recovery gate is not enabled.'
}

$httpPort = if ($clickHouseEnv.ContainsKey('CLICKHOUSE_HTTP_PORT')) {
    [int]$clickHouseEnv['CLICKHOUSE_HTTP_PORT']
} else {
    8123
}
$endpoint = "http://127.0.0.1:$httpPort/"
$credentialPair = '{0}:{1}' -f $clickHouseEnv['CLICKHOUSE_USER'], $clickHouseEnv['CLICKHOUSE_PASSWORD']
$authorization = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($credentialPair))
$headers = @{ Authorization = "Basic $authorization" }
function Invoke-ClickHouseAppQuery([string]$Query) {
    (Invoke-WebRequest -UseBasicParsing -Method Post -Uri $endpoint -Headers $headers `
        -ContentType 'text/plain; charset=utf-8' -Body $Query -TimeoutSec 15).Content.Trim()
}

$smokeTable = 'onchain.codex_verify_' + ([Guid]::NewGuid().ToString('N').Substring(0, 12))
$smokeMarker = 'smoke-' + ([Guid]::NewGuid().ToString('N'))
$smokeCreated = $false
try {
    Invoke-ClickHouseAppQuery "CREATE TABLE $smokeTable (marker String, created_at DateTime64(3)) ENGINE=MergeTree ORDER BY marker" | Out-Null
    $smokeCreated = $true
    Invoke-ClickHouseAppQuery "INSERT INTO $smokeTable FORMAT JSONEachRow`n{`"marker`":`"$smokeMarker`",`"created_at`":`"2026-01-01 00:00:00.000`"}" | Out-Null
    $insertedRows = Invoke-ClickHouseAppQuery "SELECT count() FROM $smokeTable WHERE marker='$smokeMarker'"
    if ($insertedRows -ne '1') { throw "Application insert smoke test failed: rows=$insertedRows" }
    $queriedMarker = Invoke-ClickHouseAppQuery "SELECT marker FROM $smokeTable WHERE marker='$smokeMarker' LIMIT 1"
    if ($queriedMarker -ne $smokeMarker) { throw 'Application query smoke test failed.' }
}
finally {
    if ($smokeCreated) {
        Invoke-ClickHouseAppQuery "DROP TABLE IF EXISTS $smokeTable SYNC" | Out-Null
    }
}

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
    RequiredGate = 'PASS'
    ApplicationUser = $clickHouseEnv['CLICKHOUSE_USER']
    InsertSmoke = 'PASS'
    QuerySmoke = 'PASS'
} | Format-List
