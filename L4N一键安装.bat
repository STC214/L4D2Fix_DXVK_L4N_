@echo off
setlocal
cd /d "%~dp0"

title L4N One Click Install
echo.
echo Installing / updating L4N...
echo.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install_l4n.ps1"

echo.
echo Finished. Press any key to close this window.
pause >nul
