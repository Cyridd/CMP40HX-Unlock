# CMP 40HX Windows 一键解锁工具 v2.0

**CMP 40HX (TU106) 矿卡 → Tensor Core 满速 + PCIe Gen2 解锁**
Windows 原生一键安装。零依赖（单 exe，资源全部内嵌）。

## 它能干什么

| 解锁项 | 机制 | 效果 |
|---|---|---|
| **Tensor Core (算力)** | **启用 GSP** + 安装解锁 EFI（V70）到系统 EFI 分区并设为**开机启动链第一** | 每次开机自动跑：SEC2 注入 → SS0=0x88888888 → 无缝接力进 Windows → FP16 算力从 ~0.39T 提到 **~42T**（~100 倍） |
| **PCIe Gen2** | 安装 40hx_bridge + WinRing0 驱动，注册登录自启动 | 登录自动 retrain 到 **Gen2 ×16**（5GT/s），无需任何操作 |
| 全部自动 | GSP 注册表 + EFI 启动链 + Run 键 | **开机零手动**：Tensor + Gen2 自动满血 |

## v2.0 重大修复（本次更新，关键！）

> ⚠️ 若你之前任何版本（v1.x）解锁后**黑屏 / 掉驱动（Code43）**——v2.0 解决了。

- ✅ **自动启用 GSP（EnableGpuFirmware=1）—— 解锁不黑屏的关键！**
  **根因**：40HX 的 Windows 驱动**默认 GSP 关**（CPU-RM 模式）。EFI 越权跑过 SEC2
  booter 解锁后，CPU-RM 模式的 nvlddmkm 发现 SEC2 状态不符 → **Code43 黑屏掉驱动**。
  **解法**：注册表设备键设 `EnableGpuFirmware=1` → 驱动用 **GSP-RM 模式**接管 GPU，
  GSP-RM 管理 SEC2/booter 生命周期 → **接受解锁状态** → 正常启动 + 驱动正常！
  （与 Linux GSP 模式 + 驱动内注入成功同理。）
  nvidia-smi 验证：`GSP Firmware: 615.65.06`（从 N/A 变为版本号）。
- ✅ **解锁 EFI 升级为 V70**（实测解锁成功：SS0=0x88888888 + FP16 42.45 TFLOPS +
  FP32 7.16 TFLOPS + 驱动正常）。
- ✅ 卸载器自动删除 `EnableGpuFirmware`（恢复 GSP 默认关）。

## 使用方法

1. **双击** `40HXInstaller.exe`（自动弹 UAC，点是）
2. 安装自动完成（检测 GPU / Secure Boot / 测试签名 / **GSP 启用** / 驱动 / ESP 部署 / 启动项置顶）
   **注意：安装阶段不重训 PCIe、不加载驱动**（避免与运行中的 nvlddmkm 争用 GPU），
   Gen2 与驱动加载推迟到**重启后登录时**自动执行
3. **重启电脑** —— 固件自动跑解锁 EFI（屏幕闪 `v70` 字样，可见 `UNLOCKED` →
   `chainload`）→ 自动进 Windows
4. 登录后 Gen2 自动静默解锁。完事。

所有过程输出写入日志：`%TEMP%\40HX_installer.log` / `%TEMP%\40HX_uninstaller.log`
（遇问题把这两个文件发出来即可）。

## 验证解锁生效

运行 `40HXInstaller.exe -status`（弹框），或看日志：
```
GSP 启用 (EnableGpuFirmware=1): true     ← GSP 已启用（v2.0 新增）
PCIe 链路: Gen2                          ← Gen2 解锁生效
SS0(算力): 0x88888888 (已解锁)           ← Tensor 解锁生效
```
GPU-Z 看 Link Speed 2.0；跑 FP16 推理对比 ~42 TFLOPS。
nvidia-smi -q 看 `GSP Firmware: 615.65.06`（非 N/A）。

## 参数

```
40HXInstaller.exe            安装（双击即可，需管理员）
40HXInstaller.exe -gen2      立即执行 Gen2 解锁（幂等；登录自启动即调它，静默）
40HXInstaller.exe -status    状态检查（GPU/SecureBoot/测试签名/GSP/链路/SS0）
40HXInstaller.exe -uninstall 卸载（同 40HXUninstaller.exe）
40HXUninstaller.exe          一键卸载（双击即可，需管理员）
```

## 卸载（想还原时）

双击 **`40HXUninstaller.exe`**（自动 UAC），自动移除：
1. 旧版开机计划任务（弹窗源）
2. Gen2 登录自启动（Run 键）
3. 固件启动项 "40HX Unlock"（BIOS 里不再出现）
4. ESP 分区上的解锁 EFI 文件
5. 驱动服务（40hx_bridge / WinRing0）与驱动文件
6. **EnableGpuFirmware 注册表（GSP 恢复默认关）** ← v2.0 新增
7. 残留检查

重启后 GPU 回到出厂状态。若 BIOS 菜单仍显示旧项，进 BIOS 删除即可。

## 系统要求 / 前提

- **Windows 10/11 x64**，UEFI 引导（GPT），NVIDIA 驱动已装（实测 616.56）
- 显卡为 **CMP 40HX**（PCI\VEN_10DE&DEV_1F0B）
- **Secure Boot 必须关闭**（未签名 EFI 会被固件拒绝；BIOS → Security → Secure Boot → Disabled）
- 管理员权限 + **测试签名模式**（工具会自动开启 `bcdedit /set testsigning on`，若首次开启需重启一次）
- 40HX 需插在主板的 **PCIe x16** 槽（Gen2 解锁面向此配置）

## 安全与回退

- **解锁 EFI 失败不会卡死**：解锁失败时自动回退到 Windows Boot Manager（正常启动）
- 只对 DEV_1F0B (40HX) 生效，不影响其他显卡
- **万一出现异常**：进 BIOS 把 Windows Boot Manager 设回第一启动项，
  或用 `40HXUninstaller.exe` 清理后重启即恢复
- **GSP 相关异常**（如 HDCP 问题）：卸载器删除 EnableGpuFirmware 即恢复默认

## 版本记录

- v2.0 (2026-09-04)：**启用 GSP（EnableGpuFirmware=1）修复解锁后黑屏/Code43**。
  根因 = 40HX 默认 GSP 关（CPU-RM 模式）拒绝 EFI 解锁的 SEC2；GSP-RM 模式接受。
  解锁 EFI 换 V70（实测 SS0=888 + FP16 42.45T + 驱动正常）。
- v1.3 (2026-09-03)：EFI 换 v80-NOFLR（移除 FLR，修复 v1.2 算力失效）。
- v1.2 (2026-09-03)：EFI 换 v79-FLR（CF8/CFC 通路修复）。实测 Windows 正常但 FLR 冲 SS0。
- v1.1 (2026-09-03)：GUI 无窗口化根治弹窗；单实例；卸载清理计划任务。
- v1.0 (2026-09-03)：EFI 启动链解锁 + 原生 Gen2 + 一键安装/卸载/状态。

## 内含资源

- `40HXUNLK.EFI` → V70（内嵌，585184B，实测解锁成功）
- `40hx_bridge.sys`（动态 BAR0 映射）
- `WinRing0x64.sys` / `WinRing0x64.dll`（PCIe 配置空间访问）
