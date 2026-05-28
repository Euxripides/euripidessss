@echo off
chcp 65001 >nul

set "BIN_DIR=%~dp0bin"
set "BIN_PATH=%BIN_DIR%\etl-server.exe"
set "PORT=8000"
set "URL=http://127.0.0.1:%PORT%/api/health"

echo === ETL Backend Startup ===
echo.

if not exist "%BIN_PATH%" (
    echo [BUILD] Building...
    cd /d "%~dp0"
    go build -o "%BIN_PATH%" .\cmd\server\
    if errorlevel 1 (
        echo [ERROR] Build failed
        pause
        exit /b 1
    )
)

echo [PORT] %PORT%

:kill
tasklist /FI "IMAGENAME eq etl-server.exe" 2>nul | find /I "etl-server.exe" >nul
if errorlevel 1 goto check_port
echo [STOP] Stopping old process...
taskkill /F /IM "etl-server.exe"

set WAIT=0
:wait_kill
set /a WAIT+=1
if %WAIT% gtr 7 goto check_port
timeout /t 2 /nobreak
tasklist /FI "IMAGENAME eq etl-server.exe" 2>nul | find /I "etl-server.exe" >nul
if errorlevel 1 goto check_port
taskkill /F /IM "etl-server.exe"
goto wait_kill

:check_port
echo [CHECK] Waiting for port %PORT%...
set PW=0
:port_loop
curl -s --connect-timeout 1 -o nul %URL%
if errorlevel 1 goto port_free
set /a PW+=1
if %PW% gtr 15 (
    echo [ERROR] Port %PORT% still occupied
    exit /b 1
)
timeout /t 1 /nobreak
goto port_loop
:port_free
echo [CHECK] Port %PORT% free

cd /d "%~dp0"
start /B "" "%BIN_PATH%"
echo [START] PID: unknown

echo [CHECK] Waiting for server...
for /L %%i in (1,1,15) do (
    timeout /t 1 /nobreak
    curl -s -o nul %URL%
    if not errorlevel 1 (
        echo [CHECK] Server ready
        goto done
    )
)
echo [ERROR] Server not ready after 15s
exit /b 1

:done
echo [LOG] backend/data/logs/app.log
