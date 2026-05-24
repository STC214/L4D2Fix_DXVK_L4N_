@echo off
setlocal
cd /d "%~dp0"

title L4N One Click Restore
echo.
echo Restoring the game directory to the pre-install state...
echo.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0restore_l4n.ps1"

echo.
echo Finished. Press any key to close this window.
pause >nul
