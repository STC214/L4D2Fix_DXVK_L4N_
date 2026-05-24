@echo off
setlocal
cd /d "%~dp0"

title L4N One Click Tool
echo.
echo ================================
echo   L4N One Click Tool
echo ================================
echo.
echo  1. Install / update L4N
echo  2. Restore game directory
echo  3. Exit
echo.
choice /c 123 /n /m "Choose an action [1/2/3]: "

if errorlevel 3 goto :eof
if errorlevel 2 goto restore
if errorlevel 1 goto install

:install
echo.
echo Installing L4N...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install_l4n.ps1"
goto done

:restore
echo.
echo Restoring previous game directory state...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0restore_l4n.ps1"
goto done

:done
echo.
echo Finished. Press any key to close this window.
pause >nul
