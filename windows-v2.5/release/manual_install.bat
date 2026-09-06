@echo off
setlocal EnableDelayedExpansion
title 40HX Unlock - Manual Install v2.5 (no test signing)

rem ============ CMP 40HX Windows Unlock manual install v2.5 ============
rem  Use when double-clicking 40HXInstaller.exe fails / is blocked.
rem  Run as Administrator.  Components: files\40HXUNLK.EFI + gen2\
rem  No test-signing required.  Shut down fully (not restart) after done.
rem =====================================================================

net session >nul 2>&1
if %errorlevel% neq 0 (
    echo [ERROR] Administrator required. Right-click this file - Run as administrator.
    pause
    exit /b 1
)

cd /d "%~dp0"
if not exist "files\40HXUNLK.EFI" (
    echo [ERROR] files\40HXUNLK.EFI not found. Keep files beside this script.
    pause
    exit /b 1
)

echo ============================================
echo   40HX Windows Unlock manual install v2.5
echo   No test-signing. Shut down fully after done.
echo ============================================
echo.

echo [1/5] Pre-checks
echo       - BIOS: Secure Boot Disabled + Above 4G Decoding Enabled
echo       - Test-signing not needed
bcdedit /enum firmware | findstr /i "40HX Unlock" >nul 2>&1
if not errorlevel 1 (
    echo       [i] Detected existing install - re-running just refreshes files (safe).
)

echo [2/5] Enable GSP (EnableGpuFirmware=1)
set CLASS=HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}
set FOUND=0
for /L %%i in (0,1,9) do (
    reg query "%CLASS%\000%%i" /v "HardwareInformation.AdapterString" 2>nul | findstr /i "40HX" >nul
    if not errorlevel 1 (
        reg add "%CLASS%\000%%i" /v EnableGpuFirmware /t REG_DWORD /d 1 /f >nul 2>&1
        set FOUND=1
    )
)
if "%FOUND%"=="1" (
    echo       GSP-RM enabled
) else (
    echo       [WARN] 40HX device key not found - install NVIDIA driver first
)

echo [3/5] Deploy unlock EFI (dual path on system EFI partition)
set ESP=
for %%L in (Y X W V U T S) do (
    if not defined ESP (
        mountvol %%L: /S >nul 2>&1
        if not errorlevel 1 if exist %%L:/ (
            set ESP=%%L
        )
    )
)
if not defined ESP (
    echo       [ERROR] Cannot mount EFI partition. Try: mountvol S: /S
    pause
    exit /b 1
)
echo       ESP mounted at !ESP!:\
mkdir "!ESP!:\EFI\40HX" >nul 2>&1
mkdir "!ESP!:\EFI\Boot" >nul 2>&1
copy /y "files\40HXUNLK.EFI" "!ESP!:\EFI\40HX\40HXUNLK.EFI" >nul
fc /b "files\40HXUNLK.EFI" "!ESP!:\EFI\40HX\40HXUNLK.EFI" >nul 2>&1
if errorlevel 1 (
    echo       [ERROR] write verify FAILED (A) - removing bad file, boot untouched
    del /f /q "!ESP!:\EFI\40HX\40HXUNLK.EFI" >nul 2>&1
    mountvol !ESP!: /D >nul 2>&1
    pause
    exit /b 1
)
if exist "!ESP!:\EFI\Boot\bootx64.efi" (
    if not exist "!ESP!:\EFI\Boot\bootx64.efi.40hx.bak" (
        move /y "!ESP!:\EFI\Boot\bootx64.efi" "!ESP!:\EFI\Boot\bootx64.efi.40hx.bak" >nul 2>&1
        echo       [i] original bootx64.efi backed up as .40hx.bak
    )
)
copy /y "files\40HXUNLK.EFI" "!ESP!:\EFI\Boot\bootx64.efi" >nul
fc /b "files\40HXUNLK.EFI" "!ESP!:\EFI\Boot\bootx64.efi" >nul 2>&1
if errorlevel 1 (
    echo       [ERROR] write verify FAILED (B) - removing bad file, boot untouched
    del /f /q "!ESP!:\EFI\Boot\bootx64.efi" >nul 2>&1
    mountvol !ESP!: /D >nul 2>&1
    pause
    exit /b 1
)
echo       dual-path EFI deployed + verified (fc /b OK)

echo [4/5] Create firmware boot entry (40HX Unlock first)
bcdedit /enum firmware | findstr /i "40HX Unlock" >nul 2>&1
if not errorlevel 1 (
    echo       boot entry exists, skip
) else (
    rem Create boot entry WITHOUT inline-quoted for /f (cmd parsing hazard).
    bcdedit /copy {bootmgr} /d "40HX Unlock" > "%TEMP%\40hx_bcd.txt" 2>nul
    set BCDGUID=
    for /f "usebackq tokens=2 delims={}" %%g in ("%TEMP%\40hx_bcd.txt") do set BCDGUID={%%g}
    del /f /q "%TEMP%\40hx_bcd.txt" >nul 2>&1
    if not defined BCDGUID (
        echo       [WARN] failed to create boot entry - set unlock EFI first in BIOS
    ) else (
        bcdedit /set %BCDGUID% device partition=!ESP!: >nul 2>&1
        bcdedit /set %BCDGUID% path \EFI\40HX\40HXUNLK.EFI >nul 2>&1
        bcdedit /set {fwbootmgr} displayorder %BCDGUID% /addfirst >nul 2>&1
        echo       boot entry created and set first
    )
)
mountvol !ESP!: /D >nul 2>&1
echo       ESP unmounted

echo [5/5] Register Gen2 logon autostart (SYSTEM, self-cleaning)
rem    Delegated to 40HXInstaller.exe -task: it builds the /TR quoting
rem    internally (Go), so this script has ZERO inline quotes -> no crash.
if exist "%~dp040HXInstaller.exe" (
    echo       calling 40HXInstaller.exe -task (registers logon task)...
    "%~dp040HXInstaller.exe" -task
    if errorlevel 1 (
        echo       [WARN] -task failed - run this script as admin
    )
) else (
    echo       [WARN] 40HXInstaller.exe missing - skip Gen2 autostart
    echo             (compute unlock unaffected; Gen2 needs exe present)
)
echo.
echo ============================================
echo   Manual install done!
echo.
echo   Next:
echo   1. Shut down FULLY (not restart), then power on
echo      - you should see the 40HX Unlock boot screen
echo   2. After logon, Gen2 completes in ~3 s (no test signing)
echo   3. Verify: run 40HXCheck.exe - check  and Gen2 lines
echo   4. Uninstall: run manual_uninstall.bat
echo ============================================
pause
