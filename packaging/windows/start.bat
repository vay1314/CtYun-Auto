@echo off
setlocal
cd /d "%~dp0"

if not exist "config.env" (
  echo [ERROR] config.env not found.
  pause
  exit /b 1
)

for /f "usebackq eol=# tokens=1,* delims==" %%A in ("config.env") do (
  if not "%%A"=="" set "%%A=%%B"
)

if "%UPDATE_RESTART_MODE%"=="" set "UPDATE_RESTART_MODE=self"
echo CtYunKeeper is starting at http://127.0.0.1:%APP_PORT%
ctyun-keeper.exe

if errorlevel 1 (
  echo.
  echo CtYunKeeper exited with an error.
  pause
)

