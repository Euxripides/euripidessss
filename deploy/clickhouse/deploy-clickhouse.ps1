[CmdletBinding()]
param(
    [switch]$EnableFeatures,
    [switch]$VerifyOnly
)

$ErrorActionPreference = 'Stop'
$root = 'E:\database\clickhouse'
$distroName = 'clickhouse-bsc'
$distroPath = Join-Path $root 'runtime\wsl\clickhouse-bsc'
$linuxInstaller = '/mnt/e/codex/etl/deploy/clickhouse/install-inside-wsl.sh'
$schemaFile = '/mnt/e/codex/etl/deploy/clickhouse/schema.sql'
$credentialFile = Join-Path $root 'config\clickhouse.env'

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Ensure-StorageLayout {
    $paths = @(
        $distroPath,
        (Join-Path $root 'data'),
        (Join-Path $root 'logs'),
        (Join-Path $root 'tmp'),
        (Join-Path $root 'user_files'),
        (Join-Path $root 'format_schemas'),
        (Join-Path $root 'backups'),
        (Join-Path $root 'config\config.d'),
        (Join-Path $root 'config\users.d'),
        (Join-Path $root 'migration'),
        (Join-Path $root 'export_spool')
    )
    foreach ($path in $paths) {
        if (-not $path.StartsWith('E:\', [StringComparison]::OrdinalIgnoreCase)) {
            throw "STORAGE_POLICY_VIOLATION: $path is not on E:"
        }
    }
    New-Item -ItemType Directory -Force -Path $paths | Out-Null
}

function Enable-RequiredFeatures {
    if (-not (Test-Administrator)) {
        throw 'Enabling WSL2 requires an elevated PowerShell process.'
    }
    Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Windows-Subsystem-Linux -All -NoRestart | Out-Null
    Enable-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -All -NoRestart | Out-Null
    & bcdedit /set hypervisorlaunchtype auto | Out-Null
    $runOnce = 'powershell.exe -NoProfile -ExecutionPolicy Bypass -File "' + $PSCommandPath + '"'
    New-Item -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\RunOnce' -Force | Out-Null
    Set-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\RunOnce' -Name 'CompleteClickHouseDeployment' -Value $runOnce
    Write-Host 'WSL2 features are enabled. Restart Windows; deployment will resume automatically after sign-in.' -ForegroundColor Yellow
}

function Get-DistroBasePath {
    foreach ($key in Get-ChildItem 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss' -ErrorAction SilentlyContinue) {
        $properties = Get-ItemProperty $key.PSPath
        if ($properties.DistributionName -eq $distroName) {
            return [string]$properties.BasePath
        }
    }
    return $null
}

function Ensure-Distro {
    $basePath = Get-DistroBasePath
    if (-not $basePath) {
        & wsl.exe --install -d Ubuntu-24.04 --name $distroName --location $distroPath --no-launch
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to install WSL distribution $distroName (exit $LASTEXITCODE)."
        }
        $basePath = Get-DistroBasePath
    }
    if (-not $basePath -or -not $basePath.StartsWith('E:\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "STORAGE_POLICY_VIOLATION: WSL distribution is not physically stored on E: ($basePath)"
    }
}

function New-DatabasePassword {
    $bytes = [byte[]]::new(36)
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
}

function Install-ClickHouse {
    $password = New-DatabasePassword
    $previousWSLEnv = $env:WSLENV
    try {
        $env:CLICKHOUSE_ETL_PASSWORD = $password
        $env:WSLENV = if ($previousWSLEnv) { "CLICKHOUSE_ETL_PASSWORD:$previousWSLEnv" } else { 'CLICKHOUSE_ETL_PASSWORD' }
        & wsl.exe -d $distroName -u root -- env "CLICKHOUSE_ETL_PASSWORD=$password" bash $linuxInstaller $schemaFile
        if ($LASTEXITCODE -ne 0) {
            throw "ClickHouse Linux installation failed (exit $LASTEXITCODE)."
        }
    }
    finally {
        Remove-Item Env:\CLICKHOUSE_ETL_PASSWORD -ErrorAction SilentlyContinue
        $env:WSLENV = $previousWSLEnv
    }

    $content = @(
        'CLICKHOUSE_HOST=127.0.0.1',
        'CLICKHOUSE_HTTP_PORT=8123',
        'CLICKHOUSE_NATIVE_PORT=9000',
        'CLICKHOUSE_DATABASE=onchain',
        'CLICKHOUSE_USER=etl_app',
        "CLICKHOUSE_PASSWORD=$password",
        "CLICKHOUSE_DSN=clickhouse://etl_app:$password@127.0.0.1:9000/onchain"
    )
    Set-Content -LiteralPath $credentialFile -Value $content -Encoding utf8
    & icacls.exe $credentialFile /inheritance:r /grant:r "${env:USERNAME}:(R,W)" | Out-Null
}

function Register-AutoStart {
    $startScript = Join-Path $PSScriptRoot 'start-clickhouse.ps1'
    $command = 'powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $startScript + '"'
    New-Item -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Force | Out-Null
    Set-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'ClickHouseBSC' -Value $command
}

function Start-ClickHouse {
    & (Join-Path $PSScriptRoot 'start-clickhouse.ps1')
}

function Invoke-Verification {
    & (Join-Path $PSScriptRoot 'verify-clickhouse.ps1')
    if ($LASTEXITCODE -ne 0) {
        throw 'ClickHouse verification failed.'
    }
}

Ensure-StorageLayout

if ($EnableFeatures) {
    Enable-RequiredFeatures
    exit 3010
}

if ($VerifyOnly) {
    Invoke-Verification
    exit 0
}

try {
    & wsl.exe -d Ubuntu -u root -- true 2>$null
    $wslReady = $LASTEXITCODE -eq 0
}
catch {
    $wslReady = $false
}

if (-not $wslReady) {
    if (Test-Administrator) {
        Enable-RequiredFeatures
    }
    else {
        $arguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $PSCommandPath, '-EnableFeatures')
        Start-Process powershell.exe -Verb RunAs -ArgumentList $arguments -Wait
    }
    exit 3010
}

Ensure-Distro
Install-ClickHouse
Register-AutoStart
Start-ClickHouse
Invoke-Verification
Write-Host 'ClickHouse deployment completed successfully.' -ForegroundColor Green
