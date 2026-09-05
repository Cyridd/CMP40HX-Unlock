# CMP 40HX Windows 一键解锁工具 v2.4

**CMP 40HX (TU106) 矿卡 → Tensor Core 满速 + PCIe Gen2 解锁**
Windows 原生。装完开机自动：Tensor 解锁 → 无缝进系统 → 登录后 Gen2 自动跑。

- **实测成绩**（本工具开发机，v2.4 验证通过）：
  - Tensor（算力）：`SS0=0x88888888`，FP16 HGEMM **~51.7 TFLOPS**（锁定态 ~7.9T）
  - PCIe：Gen2 ×16
  - 驱动正常（无 Code 43）、GSP 启用、nvidia-smi 可读 `GSP Firmware`

---

## 包内容

| 文件 | 用途 |
|---|---|
| `40HXInstaller.exe` | **自动安装**（推荐，双击即可，需管理员） |
| `40HXUninstaller.exe` | 自动卸载（双击即可，需管理员） |
| `manual_install.bat` | **手动安装**（自动失败/被杀软拦时的兜底，右键管理员运行） |
| `manual_uninstall.bat` | 手动卸载（同上场景的兜底） |
| `README.md` | 本手册 |
| `files\40HXUNLK.EFI` | 解锁 EFI（V70 原版，sha1 `793d765e`） |
| `files\40hx_bridge.sys` | Gen2/BAR0 驱动（测试签名） |
| `files\WinRing0x64.sys` / `.dll` | PCIe 配置空间访问驱动 |

> **为什么要带裸文件？** 自动安装器被安全软件/组策略拦下时，你可以完全手动
> 完成同样的事——EFI 自己拷、驱动自己装。两条路装出来的效果完全一样。

---

## 0. 先确认前提（不满足会失败）

- **Windows 10/11 x64**，UEFI 引导（GPT），NVIDIA 驱动已装
- 显卡是 **CMP 40HX**（设备管理器看得到，`VEN_10DE&DEV_1F0B`）
- **Secure Boot 必须关闭** —— 未签名 EFI 会被固件拒之门外，这是第一失败原因
- 建议插在主板的 **PCIe x16** 槽（Gen2 解锁按此配置调校）

---

## 1. 自动安装（推荐）

1. **双击 `40HXInstaller.exe`**（弹 UAC，点"是"）
2. 安装器自动做：
   - 检测 GPU / Secure Boot / 测试签名
   - 启用 **GSP**（`EnableGpuFirmware=1`，解锁不黑屏的关键）
   - 复制驱动 + 注册服务
   - **双路部署 EFI**：`\EFI\40HX\40HXUNLK.EFI` + `\EFI\Boot\bootx64.efi`（标准回退路径）
   - 创建固件启动项 "40HX Unlock" 并**置顶 + 写回验证**
3. 全程弹窗指引——**照着弹窗做**，尤其是 Secure Boot 关闭步骤
4. **重启电脑**：应看到 40HX 解锁画面（V70 文字日志，约 10~30 秒）→ 自动进 Windows
5. 登录后 Gen2 自动静默解锁（已注册自启动）

所有输出写日志：`%TEMP%\40HX_installer.log`。出问题把这个文件发出来。

---

## 2. 自动安装失败？手动装（兜底方案）

自动装失败通常是三类：Secure Boot 没关、主板不认 BCD 启动项、杀软拦驱动。
手动方案与自动等价，任何一步都可以单独补做。

### 2A. 最简单：跑 `manual_install.bat`

右键 **`manual_install.bat` → 以管理员身份运行**，脚本自动按顺序执行：
1. 开启测试签名
2. 启用 GSP（EnableGpuFirmware=1）
3. 复制驱动 + 注册服务（40hx_bridge / WinRing0）
4. 挂载 ESP → 双路复制 EFI → 卸载
5. 创建固件启动项并置顶
6. 注册 Gen2 登录自启动

每步有提示。哪步红字失败就针对那步手动补（见下）。

### 2B. 手动装 EFI（脚本第 4~5 步失败时）

管理员 CMD 逐条执行：

```bat
:: 1. 挂载 EFI 分区
mountvol Y: /S

:: 2. 建目录并复制 EFI（主路径）
mkdir Y:\EFI\40HX
copy /Y files\40HXUNLK.EFI Y:\EFI\40HX\40HXUNLK.EFI

:: 3. 双路：标准回退路径（原文件先备份）
mkdir Y:\EFI\Boot
if exist Y:\EFI\Boot\bootx64.efi (
    move /Y Y:\EFI\Boot\bootx64.efi Y:\EFI\Boot\bootx64.efi.40hx.bak
)
copy /Y files\40HXUNLK.EFI Y:\EFI\Boot\bootx64.efi

:: 4. 卸载 ESP
mountvol Y: /D

:: 5. 创建固件启动项并置顶（管理员）
bcdedit /copy {bootmgr} /d "40HX Unlock"
::     ↑ 记下返回的 {xxxx-...}，下面填进去
bcdedit /set {xxxx-...} device partition=Y:
bcdedit /set {xxxx-...} path \EFI\40HX\40HXUNLK.EFI
bcdedit /set {fwbootmgr} displayorder {xxxx-...} /addfirst

:: 6. 验证置顶成功
bcdedit /enum firmware
::    确认 "40HX Unlock" 在 displayorder 第一位
```

### 2C. 手动装驱动（脚本第 3 步失败 / 杀软拦时）

```bat
:: 1. 开测试签名（首次需重启一次才生效）
bcdedit /set testsigning on

:: 2. 复制驱动文件
copy /Y files\40hx_bridge.sys  %SystemRoot%\System32\drivers\
copy /Y files\WinRing0x64.sys  %SystemRoot%\System32\drivers\
copy /Y files\WinRing0x64.dll  %SystemRoot%\System32\

:: 3. 注册服务（demand 启动，登录时自动拉起）
sc create 40hx_bridge type= kernel start= demand binPath= \SystemRoot\System32\drivers\40hx_bridge.sys
sc create WinRing0_1_2_0 type= kernel start= demand binPath= \SystemRoot\System32\drivers\WinRing0x64.sys
```

**如果重启后 `sc query 40hx_bridge` 是 STOPPED 且手动 `sc start` 报错** →
大概率驱动被安全软件隔离了。检查：
- Windows 安全中心 → 病毒和威胁防护 → 保护历史记录 → 恢复被隔离文件并允许
- 或临时关闭实时保护（360/火绒/卡巴同理），再重跑上面 2C

### 2D. 手动启用 GSP（脚本第 2 步失败时）

> GSP 不启用 = 解锁后驱动不认 → **黑屏/Code 43**。必须做。

```bat
:: 找 40HX 的 Class 子键（0000/0001/...，看 AdapterString 含 "CMP 40HX" 的）
reg query "HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}" /s /v "HardwareInformation.AdapterString"

:: 对命中的子键（如 ...\0001）设置：
reg add "HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}\0001" /v EnableGpuFirmware /t REG_DWORD /d 1 /f
```

---

## 3. 重启后怎么判断成没成功

| 现象 | 含义 |
|---|---|
| 开机见 V70 文字日志 → `UNLOCKED` → `chainload` → 进 Windows | ✅ EFI 解锁成功 |
| 直接进 Windows，无解锁画面 | ❌ EFI 没跑。见第 5 节 |
| 进 Windows 后驱动正常、无黑屏 | ✅ GSP 正常（黑屏 = GSP 没开） |
| 进系统后 `-status` 显示 `SS0=0x88888888` | ✅ Tensor 解锁生效 |

**验证命令**：运行 `40HXInstaller.exe -status`（弹框诊断报告），或看：
- GPU-Z / HWiNFO 的 Link Speed = 2.0
- 跑 FP16 推理，应达 ~40+ TFLOPS
- `nvidia-smi -q | findstr GSP` → `GSP Firmware: 615.65.06`（不是 N/A）

---

## 4. 状态诊断弹窗（-status）怎么读

```
GPU 40HX: ✓    Secure Boot: 关闭(OK)
测试签名: ✓    GSP: ✓
40hx_bridge: ✓  WinRing0: ✓
PCIe: Gen2    SS0: 0x88888888
```
每一项后面会跟处置建议。逐项看是哪种组合：

- **测试签名 ✗** → 运行安装器自动开启，重启一次
- **GSP ✗** → 运行安装器自动设置（见 2D 手动）
- **驱动 ✗** → 服务没起来，多半被杀软拦（见 2C）
- **SS0 不是 88888888** → EFI 没跑成功（见第 5 节）

---

## 5. 排障：重启直接进 Windows（没跑解锁）

按顺序检查，90% 在此解决：

1. **Secure Boot 关了吗？**（第 1 大原因）
   重启按 `Del/F2` 进 BIOS → Security/Boot → **Secure Boot = Disabled** →
   保存退出。若灰显：先恢复默认安全设置或关 CSM 再改。

2. **BIOS 里 "40HX Unlock" 是第一位吗？**
   进 BIOS → 启动顺序/Boot Priority → 把 **40HX Unlock** 拖到第一。
   若列表只有 "Windows Boot Manager"：
   - 关闭 **CSM**（纯 UEFI 模式），重启项通常就出现了
   - 或关闭 **Fast Boot**（部分主板会跳过非系统启动项）

3. **重启时按 F11/F12 手动选启动设备**：选 `UEFI: <硬盘>`（走 bootx64 回退路径，
   就是安装器双路部署的第二条路）。

4. **测试签名/驱动**：首次开测试签名后必须重启一次，驱动才加载。

5. **还是不行？** 把 `%TEMP%\40HX_installer.log` 和
   ESP 根目录的 `40hx_log.txt`（若有）发出来——里面有解锁链每步日志。

---

## 6. 预期管理（社区最容易踩的认知坑）

⚠️ **重要，先读再看"是不是失效了"：**

1. **解锁是易失的，不是写死的。** SS0 是 RAM 寄存器值，**每次开机都由 EFI
   重新注入**。某次开机 EFI 没接力上（快速启动跳过、CMOS 重置清了启动项），
   本次开机算力就回到 ~7.9T——**先重启一次再判断**，别急着重装驱动/骂娘。
2. **驱动版本建议锁定**（实测 616.56 / 32.0.16.1656）。升级 NVIDIA 驱动后
   算力若回锁，属预期，**重跑一次安装器即可**（GSP 键可能被驱动重写）。
3. **升级驱动 ≠ 永久解锁**：驱动更新可能改变 GSP 固件/设备键，装完新驱动后
   建议重跑 `40HXInstaller.exe`（幂等，不会重复装）。
4. **杀软会咬无签名驱动**：Defender/360/火绒可能隔离 `40hx_bridge.sys`，
   表现 = 驱动服务起不来 + Gen2 不生效。放行/临时关闭后重装驱动即可。
   （隔离不影响 Tensor 解锁，Tensor 是 EFI 干的；但 Gen2 需要驱动。）

---

## 7. 卸载（想还原时）

- **自动**：双击 `40HXUninstaller.exe`
- **手动**：右键 `manual_uninstall.bat` → 以管理员身份运行
- 两者都会：删计划任务 / Run 键 / 固件启动项 / ESP 双路 EFI（还原 bootx64 备份）/
  驱动服务与文件 / `EnableGpuFirmware`（GSP 恢复默认）
- 重启后 GPU 回到出厂。BIOS 菜单若还显示旧项，进 BIOS 手动删。

---

## 8. 已知边界（工具救不了的部分）

| 情况 | 表现 | 对策 |
|---|---|---|
| 企业机/学校机组策略禁测试签名 | 驱动永远加载不了 | 无解，需 IT 放行或换环境 |
| BIOS 设了密码/Secure Boot 锁死 | 安装器无法自动关 | 只能清 CMOS 或找 BIOS 密码 |
| BitLocker 加密 ESP | EFI 写入被还原 | 先暂停 BitLocker 保护再装 |
| 主板硬忽略非标准启动项 | 双路都不认 | BIOS 手动加启动项（见 2B 第 5 步） |
| 不同 vbios 版本的卡时序差异 | 解锁链断在特定步 | 发 `40hx_log.txt` 给作者迭代 |

---

## 版本记录

- **v2.4 (2026-09-04)**：社区"重启没解锁"专项修复。EFI 回 V70 原版
  （`793d765e`，实测 51.7T）；ESP 双路部署（`\EFI\Boot\bootx64.efi` 标准回退）；
  启动项写回验证；Secure Boot/Fast Boot/CSM/置顶全弹窗指引；`-status` 诊断弹窗；
  日志 `(0 bytes)` 假象修复。本包新增 `files\` 裸资源 + `manual_install/uninstall.bat`。
- v2.0 (2026-09-04)：启用 GSP（`EnableGpuFirmware=1`）修复解锁后黑屏/Code43。
- v1.3 / v1.2 / v1.1 / v1.0：早期版本（详见历史）。

---

*遇到问题发日志：`%TEMP%\40HX_installer.log`（安装器）、`%TEMP%\40HX_uninstaller.log`
（卸载器）、ESP 根目录 `40hx_log.txt`（EFI 解锁链）。*
