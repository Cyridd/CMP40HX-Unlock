@echo off
setlocal EnableDelayedExpansion
title 40HX Unlock - Manual Uninstall v2.4

:: ============================================================
::  CMP 40HX Windows Unlock - 手动卸载 v2.4
::  用途: 卸载器(40HXUninstaller.exe)失败/被拦截时的兜底方案
::  用法: 右键本文件 -> 以管理员身份运行
::  移除: 启动项 / ESP EFI(双路+还原bak) / 驱动服务与文件 / GSP / Run键
:: ============================================================

net session >nul 2>&1
if %errorlevel% neq 0 (
    echo [!] 需要管理员权限。
    echo     请右键本文件 - 以管理员身份运行。
    pause
    exit /b 1
)

echo ============================================
echo   CMP 40HX Windows Unlock 手动卸载 v2.4
echo ============================================
echo.

echo [1/6] 删除 Gen2 登录自启动 ...
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v 40HXGen2 /f >nul 2>&1
for %%T in ("40HXGen2" "40HX PCIe Gen2 Bring-up" "40HXGspEnsure") do (
    schtasks /delete /tn %%T /f >nul 2>&1
)
echo      完成。

echo [2/6] 删除固件启动项 '40HX Unlock' ...
bcdedit /enum firmware > "%TEMP%\40hx_fw.txt" 2>&1
set GUID=
for /f "tokens=*" %%a in (%TEMP%\40hx_fw.txt) do (
    set LN=%%a
    for /f "tokens=1 delims={" %%p in ("!LN!") do set PRE=%%p
    set "PRET=!PRE: =!"
    if defined PRET (
        echo !LN! | findstr /i "displayorder" >nul 2>&1
        if errorlevel 1 (
            echo !LN! | findstr /r "\{[0-9a-fA-F-]*\}" >nul 2>&1
            if not errorlevel 1 (
                for /f "tokens=2 delims={}" %%g in ("!LN!") do set GUID={%%g}
            )
        )
    )
    echo !LN! | findstr /i "40HX Unlock" >nul 2>&1
    if not errorlevel 1 (
        if defined GUID (
            bcdedit /delete !GUID! /f >nul 2>&1
            echo      已删除启动项 !GUID!
        )
    )
)
del /f /q "%TEMP%\40hx_fw.txt" >nul 2>&1
echo      启动项清理完成。

echo [3/6] 删除 ESP 上的解锁 EFI (双路, 还原备份) ...
set ESP=
for %%L in (Y X W V U T S) do (
    if not defined ESP (
        mountvol %%L: /S >nul 2>&1
        if not errorlevel 1 if exist %%L:\ (
            set ESP=%%L
        )
    )
)
if defined ESP (
    del /f /q "!ESP!:\EFI\40HX\40HXUNLK.EFI" >nul 2>&1
    rmdir "!ESP!:\EFI\40HX" >nul 2>&1
    if exist "!ESP!:\EFI\Boot\bootx64.efi.40hx.bak" (
        del /f /q "!ESP!:\EFI\Boot\bootx64.efi" >nul 2>&1
        move /y "!ESP!:\EFI\Boot\bootx64.efi.40hx.bak" "!ESP!:\EFI\Boot\bootx64.efi" >nul 2>&1
        echo      已还原原 bootx64.efi
    )
    mountvol !ESP!: /D >nul 2>&1
    echo      ESP EFI 已清理。
) else (
    echo      [!!] 无法挂载 ESP, 跳过(可手动: mountvol S: /S)
)

echo [4/6] 停止并删除驱动服务 ...
for %%S in (ThrottleStop 40hx_bridge 40hx_early 40hx_early-d WinRing0_1_2_0 WinRing0x64 WinRing0) do (
    sc stop %%S >nul 2>&1
    sc delete %%S >nul 2>&1
)
echo      服务已删除。

echo [5/6] 删除驱动文件 ...
for %%F in (ThrottleStop.sys 40hx_bridge.sys 40hx_early-d.sys 40hx_early.sys WinRing0x64.sys) do (
    del /f /q "%SystemRoot%\System32\drivers\%%F" >nul 2>&1
)
del /f /q "%SystemRoot%\System32\WinRing0x64.dll" >nul 2>&1
echo      驱动文件已删除。

echo [5.5/6] 清理 ProgramData 残留 (gen2_status 诊断缓存 + 驱动备份) ...
if exist "%ProgramData% HXUnlock\gen2_status.txt" (
    del /f /q "%ProgramData% HXUnlock\gen2_status.txt" >nul 2>&1
    echo      已删除 gen2_status.txt (诊断历史缓存)
)
if exist "%ProgramData% HXUnlock\drivers" (
    rmdir /s /q "%ProgramData% HXUnlock\drivers" >nul 2>&1
    echo      已删除驱动备份目录
)
rmdir "%ProgramData% HXUnlock" >nul 2>&1

echo [6/6] 删除 EnableGpuFirmware (GSP 恢复默认关) ...
set CLASS=HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}
for /L %%i in (0,1,9) do (
    reg query "%CLASS%\000%%i" /v "HardwareInformation.AdapterString" 2>nul | findstr /i "40HX" >nul
    if not errorlevel 1 (
        reg delete "%CLASS%\000%%i" /v EnableGpuFirmware /f >nul 2>&1
        echo      已删除子键 000%%i 的 EnableGpuFirmware
    )
)

echo.
echo ============================================
echo   手动卸载完成!
echo   1. 建议重启电脑
echo   2. 若 BIOS 菜单仍显示 '40HX Unlock': 进 BIOS 手动删除
echo   3. 若重启后算力仍在/异常: 这是 GSP-RM 缓存, 重启即可回出厂
echo ============================================
echo.
pause
