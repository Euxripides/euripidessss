$ErrorActionPreference = 'Stop'
$distroName = 'clickhouse-bsc'

# A keeper may outlive a stopped server. Always restore the service first,
# then ensure the distro has a long-lived process.
& wsl.exe -d $distroName -u root -- bash -lc "systemctl is-active clickhouse-server >/dev/null || systemctl start clickhouse-server 2>/dev/null || service clickhouse-server start"
if ($LASTEXITCODE -ne 0) {
    throw 'Unable to start ClickHouse service.'
}

# WSL may stop a distribution after the launching Windows process exits, even
# when a Linux service was started successfully. Keep one hidden WSL process
# alive so ClickHouse remains reachable on localhost for the whole login session.
& wsl.exe -d $distroName -u root -- bash -lc "pgrep -f '^clickhouse-wsl-keeper ' >/dev/null"
if ($LASTEXITCODE -ne 0) {
    $arguments = '-d clickhouse-bsc -u root -- bash -lc "exec -a clickhouse-wsl-keeper sleep infinity"'
    Start-Process -FilePath wsl.exe -ArgumentList $arguments -WindowStyle Hidden
}

for ($attempt = 0; $attempt -lt 30; $attempt++) {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:8123/ping' -TimeoutSec 2
        if ($response.StatusCode -eq 200 -and $response.Content.Trim() -eq 'Ok.') {
            exit 0
        }
    }
    catch {
        Start-Sleep -Seconds 1
    }
}
throw 'ClickHouse did not become ready within 30 seconds.'
