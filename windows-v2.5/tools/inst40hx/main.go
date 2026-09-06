// 40HX 一键安装工具 v2.4.5 (CMP 40HX Windows Unlock Installer)
// 功能:
//
//	(默认) 安装: GSP 启用(EnableGpuFirmware=1) + ESP 双路部署 40HXUNLK.EFI (V70)
//	      + BootOrder 置顶 + 驱动 + Gen2 自启动
//	-gen2        立即执行 Gen2 解锁(供登录自启动调用, 幂等)
//	-uninstall   卸载(移除启动项/Run键/驱动服务/EnableGpuFirmware)
//	-status      状态检查
//
// 资源 embed (v2.5): 40HXUNLK.EFI (V70 解锁版) / ThrottleStop.sys / WinRing0x64.sys
// v2.4 关键变更(社区兼容):
//  1. embed EFI 回到 V70 原版 (793d765e, 用户实测解锁成功) — v2.1/v2.2 精简版失败教训
//  2. ESP 双路部署: \EFI\40HX\40HXUNLK.EFI (BCD 主路径)
//     + \EFI\Boot\bootx64.efi (UEFI 标准 fallback, 原文件备份 .40hx.bak)
//     解决部分主板不认非标准 EFI 路径/忽略 BCD displayorder 导致"装完重启没反应"
//  3. BootOrder 写入后从固件读回验证, 不在首位时明确弹窗提示 BIOS 手动置顶
//  4. 关键 BIOS 操作全部进消息框 (社区用户不看 README/日志)
//
// v2.3 关键: EnableGpuFirmware=1 启用 GSP — 40HX 默认 GSP 关(CPU-RM 模式)时,
//
//	EFI 解锁后 nvlddmkm 拒绝 SEC2 状态 -> Code43 黑屏; GSP-RM 模式能接受解锁.
package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"40hxcore"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed embed/*
var embedded embed.FS

const (
	gpuVenDev = "VEN_10DE&DEV_1F0B"
	efiDir    = "\\EFI\\40HX"
	efiFile   = "40HXUNLK.EFI"
	bootDesc  = "40HX Unlock"
	// v2.4: UEFI 标准回退路径 (固件 BootOrder 全部无效/未签名时自动尝试此路径;
	// 解决部分主板忽略 BCD displayorder / 不认非标准 \EFI\40HX 目录)
	efiStdDir = "\\EFI\\Boot"
	efiStdF   = "bootx64.efi"
	efiBakExt = ".40hx.bak" // bootx64.efi.40hx.bak 原文件备份
	// v2.3: GSP 启用注册表 (EnableGpuFirmware=1) — 解锁不黑屏的关键!
	// 40HX 的显示适配器 Class 子键 (0001 = 40HX; 多卡时需按 AdapterString 找)
	gpuClassPath  = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	gpuClassGUID  = `{4d36e968-e325-11ce-bfc1-08002be10318}` // Driver 值反查用
	gpuEnableFw   = "EnableGpuFirmware"
	gpuAdapterStr = "HardwareInformation.AdapterString"
	gpuAdapter40  = "CMP 40HX"
	// v2.4.6: Gen2 的 SYSTEM 计划任务名(卸载时按名字删除)
	gen2TaskName = "40HX PCIe Gen2 Bring-up"
)

func main() {
	// GUI 无窗口版(v1.1): 输出全部镜像到日志(默认 %TEMP%\40HX_installer.log, 可 -log 指定)
	setupLog("40HX_installer.log")
	// install/-uninstall 需管理员: 非提升时自动 ShellExecute runas 弹 UAC 重启
	needAdmin := true
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-gen2", "-gspensure", "-status", "-h", "-help", "--help":
			needAdmin = false
		}
		// -task 需管理员(GUI 双击自动 UAC; gen2/status 等只读或 SYSTEM 任务调用无需)
		if os.Args[1] == "-task" {
			needAdmin = true
		}
	}
	if needAdmin && !isAdmin() {
		if hasArg("-elevated") {
			// 已提权过一次仍失败(如静默提权策略下受限token) -> 禁止再循环, 直接报错
			msgbox("40HX 安装器", "提权失败：当前账户无法获得管理员权限。\n请右键本程序 -> 以管理员身份运行。", mbIconError)
			return
		}
		selfElevate()
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-gen2":
			gen2Main()
			return
		case "-gspensure":
			gspEnsureMain()
			return
		case "-uninstall":
			uninstall()
			return
		case "-status":
			status()
			return
		case "-task":
			// 仅注册 Gen2 登录自启任务(供 manual_install.bat 调用;
			// 由 Go 构造 /TR 引号, 避免 bat 内嵌引号解析出错/闪退)
			regTaskOnly()
			return
		case "-h", "-help", "--help":
			printHelp()
			return
		}
	}
	install()
}

// regTaskOnly: 只注册 Gen2 SYSTEM 任务(不安装驱动/EFI/GSP)。
// manual_install.bat 的 [5/5] 调用本模式 — Go 处理引号, bat 零引号嵌套。
func regTaskOnly() {
	if !isAdmin() {
		fmt.Println("[!] 注册计划任务需要管理员权限。")
		msgbox("40HX 安装器", "注册计划任务需要管理员权限。\n请以管理员身份运行。", mbIconError)
		return
	}
	setupGen2Task()
	// Run 键兜底(同 install 逻辑, 双保险)
	setRunKey()
	msgbox("40HX 安装器", "Gen2 登录自启任务已注册。\n登录后会自动执行 Gen2 解锁(静默, 用完即卸)。", mbIconInfo)
}

// selfElevate: 非管理员时 ShellExecute "runas" 重启自身(触发 UAC), 父进程退出
// GUI 子系统下无黑窗; 提权失败以消息框提示
func selfElevate() {
	exe, _ := os.Executable()
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	// 追加 -elevated 标记: 新实例若仍非管理员则禁止再次提权(防无限循环)
	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-elevated")
	params, _ := syscall.UTF16PtrFromString(strings.Join(args, " "))
	r, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), 0, 1)
	if r <= 32 {
		msgbox("40HX 安装器", fmt.Sprintf("提权失败(错误码 %d)。\n请右键本程序 -> 以管理员身份运行。", r), mbIconError)
	}
	os.Exit(0)
}

var (
	procShellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
)

const (
	mbIconInfo  = 0x40
	mbIconError = 0x10
	mbYesNo     = 0x04 // MB_YESNO → 返回 IDYES=6 / IDNO=7
)

var (
	procMsgBoxW     = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	procCreateMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
)

func msgbox(title, text string, icon uint) {
	// -y / -silent(自动化/自启动) 时不弹框
	if hasArg("-y") || hasArg("-silent") {
		return
	}
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(icon))
}

// msgboxYesNo: 是/否询问。自动模式: -y→true(全自动继续), -silent→false(不打扰)。
func msgboxYesNo(title, text string) bool {
	if hasArg("-y") {
		return true
	}
	if hasArg("-silent") {
		return false
	}
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(mbYesNo|mbIconInfo))
	return r == 6 // IDYES
}

// setupLog: 输出镜像到日志文件(默认 %TEMP%/<name>, 命令行 -log <file> 优先)
func setupLog(defName string) {
	p := filepath.Join(os.TempDir(), defName)
	if i := argIndex("-log"); i >= 0 && i+1 < len(os.Args) {
		p = os.Args[i+1]
	}
	if f, err := os.Create(p); err == nil {
		os.Stdout = f
		os.Stderr = f
		fmt.Fprintf(f, "==== 40HX tool %s ====\n", time.Now().Format("2006-01-02 15:04:05"))
	}
}

// lockOnce: 单实例互斥; 返回 nil 表示已有实例在跑
func lockOnce(name string) func() {
	n, _ := syscall.UTF16PtrFromString(name)
	h, _, e := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return nil
	}
	if e == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(syscall.Handle(h))
		return nil
	}
	return func() { syscall.CloseHandle(syscall.Handle(h)) }
}

func hasArg(name string) bool {
	for _, a := range os.Args {
		if a == name {
			return true
		}
	}
	return false
}

func argIndex(name string) int {
	for i, a := range os.Args {
		if a == name {
			return i
		}
	}
	return -1
}

func printHelp() {
	fmt.Println("CMP 40HX Windows 解锁一键安装工具")
	fmt.Println("  用法: 40HXInstaller.exe            # 安装(需管理员)")
	fmt.Println("       40HXInstaller.exe -gen2      # 立即执行 Gen2 解锁")
	fmt.Println("       40HXInstaller.exe -uninstall # 卸载")
	fmt.Println("       40HXInstaller.exe -status    # 状态")
}

// ===================== 底层 =====================

func isAdmin() bool {
	var t windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &t)
	if err == nil {
		defer t.Close()
		var e uint32
		var n uint32
		if err = windows.GetTokenInformation(t, windows.TokenElevation,
			(*byte)(unsafe.Pointer(&e)), uint32(unsafe.Sizeof(e)), &n); err == nil && e != 0 {
			return true
		}
		// TokenElevation 可能因受限环境(沙箱/服务)误报 0, 再试 SCM 全权
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		windows.CloseServiceHandle(scm)
		return true
	}
	return false
}

// enableGsp: 设 EnableGpuFirmware=1 (需管理员)
func enableGsp() error {
	key := hxcore.FindGpuClassKey()
	if key == "" {
		return errors.New("找不到 40HX 的设备注册表键 (Class 子键)")
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(gpuEnableFw, 1)
}

// disableGsp: 删 EnableGpuFirmware (卸载用, 恢复默认关)
func disableGsp() {
	key := hxcore.FindGpuClassKey()
	if key == "" {
		return
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.DeleteValue(gpuEnableFw)
}

// ensureGspSilent: 确保 GSP 启用 (EnableGpuFirmware=1)。
// 供 -gen2(登录自启动)调用: 若 GSP 被改回(≠1)则重新启用。
// 写 HKLM 需管理员: 当前是管理员直接写; 否则注册一次性 SYSTEM 计划任务
// (SYSTEM 权限写 HKLM 无需 UAC, 无窗口)。
// 返回 true = GSP 已启用或已安排重设。
func ensureGspSilent() bool {
	if hxcore.GspEnabled() {
		return true // 已启用
	}
	fmt.Println("[GSP] EnableGpuFirmware 被改回, 重新启用...")
	if isAdmin() {
		if err := enableGsp(); err != nil {
			fmt.Println("[GSP] 重设失败:", err)
			return false
		}
		fmt.Println("[GSP] 已重设 EnableGpuFirmware=1 (重启后 GSP-RM 生效)")
		return true
	}
	// 非管理员: 用 SYSTEM 计划任务一次性重设 (无 UAC 弹窗)
	exe, _ := os.Executable()
	abs, _ := filepath.Abs(exe)
	tn := "40HXGspEnsure"
	if out, err := hxcore.RunOut("schtasks.exe", "/create", "/tn", tn,
		"/tr", fmt.Sprintf("\"%s\" -gspensure -silent", abs),
		"/sc", "once", "/st", "00:00", "/ru", "SYSTEM", "/f"); err != nil {
		fmt.Printf("[GSP] 计划任务创建失败: %s\n", strings.TrimSpace(out))
		return false
	}
	hxcore.RunOut("schtasks.exe", "/run", "/tn", tn)
	hxcore.RunOut("schtasks.exe", "/delete", "/tn", tn, "/f")
	fmt.Println("[GSP] 已通过 SYSTEM 任务重设 EnableGpuFirmware=1")
	return true
}

// gspEnsureMain: -gspensure 模式 (SYSTEM 计划任务调用, 只重设 GSP 后退出)
func gspEnsureMain() {
	if isAdmin() {
		if err := enableGsp(); err != nil {
			fmt.Println("[GSP] gspensure 重设失败:", err)
			return
		}
		fmt.Println("[GSP] gspensure: EnableGpuFirmware=1 已设置")
	}
}

func copyEmbedTo(target string, src string) error {
	data, err := embedded.ReadFile("embed/" + src)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

// deployEspEfi: 双路部署 40HXUNLK.EFI 到已挂载的 ESP <esp>。
//
//	A. \EFI\40HX\40HXUNLK.EFI   — BCD 启动项引用路径
//	B. \EFI\Boot\bootx64.efi    — UEFI 标准回退路径 (固件无条件尝试的最后手段;
//	   解决社区大量"装完重启直接进 Windows 没跑解锁"——主板忽略非标准目录)
//
// 备份规则: 若目标 bootx64.efi 存在且不是本工具部署过的副本, 先备份为
//
//	bootx64.efi.40hx.bak (卸载时恢复)。已部署过(.bak 已存在)则直接覆盖。
//
// 返回 fallback 是否新备份了原文件。
func deployEspEfi(esp string) (backedUp bool, err error) {
	// 读取 embed 一次, 两个路径共用
	data, rerr := embedded.ReadFile("embed/40HXUNLK.EFI")
	if rerr != nil {
		return false, rerr
	}
	// 写盘前校验 embed 数据本身完整 (PE 头 + 长度合理, 防 embed 损坏)
	if len(data) < 0x2000 { // < 8KB 的 EFI 文件必为损坏
		return false, fmt.Errorf("内嵌 40HXUNLK.EFI 数据异常 (%d bytes)", len(data))
	}
	if !bytes.HasPrefix(data, []byte("MZ")) {
		return false, errors.New("内嵌 40HXUNLK.EFI 不是有效 PE 镜像(缺 MZ 头)")
	}

	// A. 主路径
	dirA := esp + ":" + efiDir // Y:\EFI\40HX
	if merr := os.MkdirAll(dirA, 0o644); merr != nil {
		return false, merr
	}
	pA := filepath.Join(dirA, efiFile)
	if werr := writeVerified(pA, data); werr != nil {
		// 写失败或校验不一致 → 删掉可能半截的文件, 避免被 BCD 引用成坏引导
		os.Remove(pA)
		return false, werr
	}
	fmt.Printf("    [A] %s  (%d bytes, 校验 OK)\n", "\\EFI\\40HX\\"+efiFile, len(data))

	// B. 标准回退路径
	dirB := esp + ":" + efiStdDir // Y:\EFI\Boot
	if merr := os.MkdirAll(dirB, 0o644); merr != nil {
		return false, merr
	}
	pB := filepath.Join(dirB, efiStdF) // bootx64.efi
	pBak := pB + efiBakExt             // bootx64.efi.40hx.bak
	if _, berr := os.Stat(pBak); berr != nil {
		// 无备份记录 → 若目标存在且不是我们已部署的副本, 先备份
		if old, oerr := os.ReadFile(pB); oerr == nil && !bytes.Equal(old, data) {
			if cerr := os.Rename(pB, pBak); cerr != nil {
				return false, fmt.Errorf("备份原 %s 失败: %v", pB, cerr)
			}
			fmt.Printf("    [B] 原 %s 已备份为 %s\n", efiStdF, efiStdF+efiBakExt)
			backedUp = true
		} else if oerr != nil {
			// 目标不存在: 无备份(本来就是空位)
		}
	}
	if werr := writeVerified(pB, data); werr != nil {
		os.Remove(pB)
		return backedUp, werr
	}
	fmt.Printf("    [B] %s  (%d bytes, 校验 OK)\n", "\\EFI\\Boot\\"+efiStdF, len(data))
	return backedUp, nil
}

// writeVerified: 写文件后立即读回比对 — 防止写入中断/半截导致引导损坏。
// 不一致则删除并返回错误(调用方据此中止, 不让坏文件留在引导路径)。
func writeVerified(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	rb, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("写后校验读取失败 %s: %v", path, err)
	}
	if !bytes.Equal(rb, data) {
		return fmt.Errorf("写后校验不一致 %s (%d ≠ %d bytes)", path, len(rb), len(data))
	}
	return nil
}

// alreadyInstalled: 检测是否已安装过(避免无意义/重复的覆盖安装)。
// 判据: ① 固件启动项 "40HX Unlock" 存在; ② ESP 上已有 \EFI\40HX\40HXUNLK.EFI。
// 任一命中即认为装过 — 用于重入提示(不会因此阻止用户, 仅弹确认)。
func alreadyInstalled() bool {
	// ① bcdedit 固件枚举(不挂 ESP, 快速)
	if out, _ := hxcore.RunOut("bcdedit.exe", "/enum", "firmware"); strings.Contains(out, bootDesc) {
		return true
	}
	// ② ESP 文件
	esp := hxcore.MountESP()
	if esp == "" {
		return false // 挂不上 ESP 时保守视为未装(后面 [5/8] 会报错引导)
	}
	defer hxcore.UnmountESP(esp)
	if _, err := os.Stat(esp + ":" + efiDir + "\\" + efiFile); err == nil {
		return true
	}
	return false
}

// verifyBootEntry: 读回 {fwbootmgr} displayorder, 确认 40HX Unlock 是否在首位。
// 返回 (exists, isFirst, displayOrder描述)。
// 用 bcdedit /enum firmware 读固件 NVRAM — 若固件忽略 bcdedit 的写入,
// 这里会如实反映(不在列表/不在首位), 从而让安装器给出 BIOS 手动指引。
// 注意: bcdedit 输出为 GBK, 中文系统"标识符/说明"是乱码; 但字段值
// (guid / displayorder / 40HX Unlock / path) 均为 ASCII, 按块解析可靠。
func verifyBootEntry() (bool, bool, string) {
	out, err := hxcore.RunOut("bcdedit.exe", "/enum", "firmware")
	if err != nil {
		return false, false, "(bcdedit 读取失败: " + err.Error() + ")"
	}
	lines := strings.Split(out, "\r\n")
	if len(lines) < 2 {
		lines = strings.Split(out, "\n")
	}

	// 1. 收集 displayorder 下的 GUID 序列(固件实际启动顺序)
	var order []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "displayorder") {
			// 首个 GUID 可能同行: "displayorder {guid}"
			if m := guidRe().FindString(t); m != "" {
				order = append(order, strings.Trim(m, "{}"))
			}
			// 后续缩进行 {guid}
			for j := i + 1; j < len(lines); j++ {
				s := strings.TrimSpace(lines[j])
				if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
					order = append(order, strings.Trim(s, "{}"))
				} else if s != "" {
					break
				}
			}
			break // displayorder 只在 {fwbootmgr} 段, 取首个即可
		}
	}

	// 2. 找 description 为 "40HX Unlock" 的块的 GUID
	target := ""
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "description") &&
			strings.Contains(lines[i], bootDesc) {
			// 往上找最近的 {guid} 行 = 该块 identifier
			for j := i - 1; j >= 0 && j > i-6; j-- {
				if m := guidRe().FindString(lines[j]); m != "" {
					target = strings.Trim(m, "{}")
					break
				}
			}
			break
		}
	}
	if target == "" {
		joined := strings.Join(order, " > ")
		if joined == "" {
			joined = "(固件无 displayorder 条目)"
		}
		return false, false, joined
	}
	if len(order) == 0 {
		return true, false, "(displayorder 为空)"
	}
	isFirst := order[0] == target
	return true, isFirst, strings.Join(order, " > ")
}

var _guidRe = regexp.MustCompile(`\{([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}`)

func guidRe() *regexp.Regexp { return _guidRe }

// ===================== 安装 =====================

func install() {
	fmt.Println("==============================================")
	fmt.Println("  CMP 40HX Windows Unlock Installer v2.4.5")
	fmt.Println("  Tensor 解锁(EFI V70 + GSP 启用) + PCIe Gen2 + 自启动")
	fmt.Println("==============================================")

	if !isAdmin() {
		fmt.Println("[!] 需要管理员权限。")
		msgbox("40HX 安装器", "需要管理员权限。\n请右键本程序 -> 以管理员身份运行。", mbIconError)
		return
	}
	if lockOnce(`Local\40HXInstaller_v1`) == nil {
		msgbox("40HX 安装器", "安装器已在运行, 请勿重复点击。", mbIconInfo)
		return
	}

	// 0. 重入检测: 已装过(固件启动项/GSP 键已存在) → 确认后再覆盖,
	//    避免用户误以为需要反复安装、或在不知情下覆盖现有部署。
	if alreadyInstalled() {
		fmt.Println("[!] 检测到 40HX 解锁已安装过(启动项/GSP 键存在)。")
		if !msgboxYesNo("40HX 安装器",
			"检测到 40HX 解锁已安装过。\n\n"+
				"再次安装会覆盖现有部署(驱动与启动项会更新, 不会损坏系统引导)。\n"+
				"如果是想修复异常/升级, 选\"是\"继续;\n"+
				"如果只是误打开, 选\"否\"保持现状即可。\n\n"+
				"继续重新安装?") {
			fmt.Println("已取消 — 保持现有安装不变。")
			return
		}
		fmt.Println("    用户确认, 继续覆盖安装。")
	}

	// 1. GPU 检测
	fmt.Print("[1/8] 检测 GPU ... ")
	if !hxcore.FindGPU() {
		fmt.Println("未找到 " + gpuVenDev)
		fmt.Println("[!] 未检测到 CMP 40HX。中止。")
		msgbox("40HX 安装器", "未检测到 CMP 40HX 显卡 (VEN_10DE&DEV_1F0B)。\n安装中止。", mbIconError)
		return
	}
	fmt.Println("CMP 40HX 已找到")

	// 2. Secure Boot
	fmt.Print("[2/8] Secure Boot 检查 ... ")
	if hxcore.SecureBootOn() {
		fmt.Println("开启!")
		fmt.Println("[!] Secure Boot 开启时, 未签名 EFI(40HXUNLK) 会被固件拒绝。")
		msgbox("40HX 安装器 (需要关闭 Secure Boot)",
			"检测到 Secure Boot 开启, 未签名的解锁 EFI 会被固件拒绝。\n\n"+
				"请重启进 BIOS 关闭后再运行本安装器:\n"+
				"  1. 重启, 开机按 Del / F2(部分主板 F1/F10/F12)进 BIOS\n"+
				"  2. 找 Security / Boot / 启动 选项卡\n"+
				"  3. 将 Secure Boot 设为 Disabled\n"+
				"     (若灰显, 先设 CSM/兼容模式 或恢复默认安全设置)\n"+
				"  4. 保存退出(F10)后重新运行本程序\n\n"+
				"这是解锁必需的: 40HX 解锁 EFI 无微软签名。",
			mbIconError)
		return
	}
	fmt.Println("关闭/不可用(OK)")

	// 3. 测试签名 (v2.5 不需要 — BYOVD 预签名驱动普通模式即可加载)
	fmt.Print("[3/8] 测试签名 ... ")
	if hxcore.TestSigningOn() {
		fmt.Println("已开启 — v2.5 不需要, 装完可 bcdedit /set testsigning off 关闭")
	} else {
		fmt.Println("关闭(OK) — v2.5 全程免测试签名")
	}

	// 3.5 GSP 启用 (v2.3: 解锁不黑屏的关键!)
	// 40HX 默认 GSP 关(CPU-RM 模式) -> EFI 解锁后 nvlddmkm 拒绝 -> Code43 黑屏
	// EnableGpuFirmware=1 -> GSP-RM 管理 SEC2/booter -> 接受解锁状态
	fmt.Print("[3.5/8] 启用 GSP (EnableGpuFirmware) ... ")
	if hxcore.GspEnabled() {
		if sub, _, fw := hxcore.GspDiag(); sub != "" {
			fmt.Printf("已启用(OK) — Class\\%s EnableGpuFirmware=%d\n", sub, fw)
		} else {
			fmt.Println("已启用(OK)")
		}
	} else {
		if err := enableGsp(); err != nil {
			// v2.4.1: 附带 AdapterString 诊断 — 伪装驱动(雨糖识别成2070等)会命中此分支
			_, adapterDiag, _ := hxcore.GspDiag()
			fmt.Println("设置失败:", err)
			if adapterDiag != "" && !strings.Contains(adapterDiag, "无 CMP 40HX") {
				fmt.Println("    [!] 实际 AdapterString:", adapterDiag)
			} else if adapterDiag != "" {
				fmt.Println("    [!]", adapterDiag)
			}
			fmt.Println("    [!] 若驱动是伪装版(识别成 2070 等): 换未伪装版驱动或手动设 GSP")
			msgbox("40HX 安装器", "设置 EnableGpuFirmware=1 失败(需管理员)。\n解锁后可能黑屏/掉驱动。\n错误: "+err.Error()+"\n若驱动是伪装版(识别成2070等),请换未伪装驱动或用 -status 查 AdapterString。", mbIconError)
			return
		}
		fmt.Println("已设 EnableGpuFirmware=1 (重启生效)")
		fmt.Println("    [!] GSP 必需: 否则 EFI 解锁后驱动不认 -> Code43 黑屏")
	}

	// 4. 驱动安装
	fmt.Println("[4/8] 准备 Gen2 BYOVD 驱动(ThrottleStop + WinRing0)...")
	installDrivers()

	// 4.5 Defender 精确排除(防杀软误删驱动文件导致 Gen2 自启失败)
	//     只加我们自己的驱动/备份/发布目录, 不关任何系统防护。
	fmt.Print("[4.5/8] Defender 排除(防误删) ... ")
	if err := hxcore.AddDefenderExclusions(); err != nil {
		fmt.Println("未执行(可忽略):", err)
	} else {
		fmt.Println("已加白 ThrottleStop/WinRing0 驱动文件与备份目录")
	}

	// 5. ESP 部署 (v2.4: 双路)
	//    主路径  \EFI\40HX\40HXUNLK.EFI  — BCD 启动项引用
	//    fallback \EFI\Boot\bootx64.efi   — UEFI 标准回退路径, 解决部分主板
	//    忽略 BCD displayorder / 不认非标准目录(社区"装完重启没反应"主因)。
	//    原 bootx64.efi 备份为 bootx64.efi.40hx.bak, 卸载时恢复。
	fmt.Println("[5/8] 部署解锁 EFI 到系统 EFI 分区(双路)...")
	esp := hxcore.MountESP()
	if esp == "" {
		fmt.Println("[!] 无法挂载 EFI 分区(mountvol /S 失败)")
		fmt.Println("    可手动: mountvol S: /S, 复制 40HXUNLK.EFI 到 S:\\EFI\\40HX\\, mountvol S: /D")
		msgbox("40HX 安装器", "无法挂载 EFI 分区 (mountvol /S 失败)。\n请以管理员身份运行, 或手动部署 EFI(见日志)。", mbIconError)
		return
	}
	fmt.Printf("    ESP 挂载于 %s: \\\n", esp)
	fb, err := deployEspEfi(esp)
	hxcore.UnmountESP(esp)
	if err != nil {
		fmt.Println("[!] 复制 EFI 失败:", err)
		msgbox("40HX 安装器 (EFI 写入失败)",
			"复制解锁 EFI 到 ESP 失败(已做写后校验, 坏文件不会残留):\n"+err.Error()+
				"\n\n系统引导未受影响, 重启应能正常进 Windows。\n\n"+
				"如需手动部署, 见同目录《EFI应急修复指南.md》中\n"+
				"“手动部署”一节(或用 manual_install.bat)。", mbIconError)
		return
	}
	if fb {
		fmt.Println("    [!] 检测到原 bootx64.efi, 已备份为 bootx64.efi.40hx.bak")
	}

	// 6. BootOrder (v2.4: 写回验证 + BIOS 指引弹框)
	fmt.Println("[6/8] 设置固件启动项(40HX Unlock 置顶)...")
	bootOK := false
	if err := setupBootEntry(); err != nil {
		fmt.Println("[!] 自动设置启动项失败:", err)
	} else {
		if ex, first, ord := verifyBootEntry(); ex {
			bootOK = first
			if first {
				fmt.Println("    启动项已置顶并验证通过 (固件 displayorder 首位)")
			} else {
				fmt.Println("    [!] 启动项已创建, 但不在 displayorder 首位:")
				fmt.Println("        当前固件顺序: " + ord)
				fmt.Println("        请进 BIOS 手动将 '40HX Unlock' 设为第一启动项(见弹窗)")
			}
		} else {
			fmt.Println("    [!] 未能在固件启动列表中找到 '40HX Unlock' 项")
			fmt.Println("        (部分主板忽略 BCD 写入, 请进 BIOS 手动添加/置顶)")
		}
	}
	if !bootOK {
		// BIOS 指引弹窗 (社区用户不看日志/README 的关键一步)
		msgbox("40HX 安装器 (重要: 请按提示操作)",
			"自动启动项未被固件接受。\n"+
				"请重启并按 Del/F2 进 BIOS, 完成以下设置(否则不解锁):\n\n"+
				"1. 关闭 Secure Boot(已开则未签名 EFI 会被拒)\n"+
				"2. 关闭 Fast Boot / 快速启动(若有)\n"+
				"3. 在 [启动顺序/Boot Priority] 中把 '40HX Unlock' 设为第一项\n"+
				"   或手动从启动设备选择 \\EFI\\40HX\\40HXUNLK.EFI\n"+
				"4. 若列表只有 Windows Boot Manager:\n"+
				"   - 部分主板需关闭 CSM(纯 UEFI)后才会出现该启动项\n"+
				"   - 或直接选 UEFI 盘符启动(走 bootx64 回退)\n\n"+
				"安装器已把解锁 EFI 同时部署到:\n"+
				"  \\EFI\\40HX\\40HXUNLK.EFI  (BCD 路径)\n"+
				"  \\EFI\\Boot\\bootx64.efi    (标准回退路径)\n\n"+
				"详细日志: "+filepath.Join(os.TempDir(), "40HX_installer.log"),
			mbIconError)
	}

	// 7. Gen2 自启动(安装时不 retrain!)
	// 重要: 安装过程中绝不执行 Gen2 PCIe 重训。此时 nvlddmkm 正占用 GPU,
	// 强行 retrain 会让 GPU/链路进入异常状态, 导致下次开机 EFI 接力或
	// nvlddmkm 初始化失败(实测: 设备报 code19 / Windows 启动异常进安全模式)。
	// 正确时机 = 重启后登录时执行(与手动方案一致, 已验证稳定)。
	// v2.4.6: 增加 SYSTEM 计划任务(权限足够 sc start); Run 键保留作兜底
	// (普通权限, 驱动已由任务加载时只做 retrain, 没加载则静默跳过)。
	fmt.Println("[7/8] 注册 Gen2 登录自启动(SYSTEM 任务 + Run 键兜底)...")
	setRunKey()
	setupGen2Task()

	fmt.Println()
	fmt.Println("安装完成!")
	fmt.Println("  下次重启: 固件将自动运行 40HX Unlock (Tensor 解锁) -> 自动进 Windows")
	fmt.Println("  GSP 已启用: 驱动以 GSP-RM 模式接管 GPU, 解锁后不再黑屏/掉驱动")
	fmt.Println("  登录后: Gen2 自动解锁 (已注册自启动, 无窗口静默)")
	fmt.Println("  [!] 安装时不重训 PCIe, 重启后登录时才执行(避免与显卡驱动冲突)")
	fmt.Println("  重启后验证: 双击 40HXCheck.exe 查看解锁状态(SS0=0x88888888 即成功)")
	fmt.Println("  若 testsigning 刚开启: 请先重启一次使驱动可加载")
	// v2.4: 完成弹框含关键 BIOS/重启指引(社区用户不依赖 README 也能操作)
	msgbox("40HX 安装器 (安装完成)",
		"✅ 安装完成! 重启后将自动执行解锁。\n\n"+
			"重启时请注意:\n"+
			"  · 若黑屏/显示 40HX 文字日志约 10~30 秒, 属正常(正在解锁)\n"+
			"  · 解锁完成后会自动进入 Windows\n\n"+
			"重启进系统后:\n"+
			"  · 双击同目录的 40HXCheck.exe 验证 — 显示\n"+
			"    '解锁成功: Tensor 满血(SS0=0x88888888)' 即完成\n"+
			"  · 若提示未解锁, 它会给下一步(如开 Above 4G)\n\n"+
			"若重启后直接进了 Windows(没跑解锁), 请进 BIOS(Del/F2):\n"+
			"  1. 关闭 Secure Boot(未签名 EFI 需要)\n"+
			"  2. 关闭 Fast Boot\n"+
			"  3. 把 '40HX Unlock' 设为第一启动项\n"+
			"     (若列表只有 Windows Boot Manager, 关 CSM 后再看)\n\n"+
			"· 测试签名若刚开启: 先重启一次驱动才可加载\n"+
			"· GSP 已启用(EnableGpuFirmware=1): 解锁不黑屏的关键\n"+
			"· 登录后 Gen2 自动解锁(静默)\n\n"+
			"详细日志: "+filepath.Join(os.TempDir(), "40HX_installer.log"),
		mbIconInfo)
}

func installDrivers() {
	sysDir := os.Getenv("SystemRoot") + "\\System32\\drivers"
	svcRunning := func(name string) bool {
		out, _ := hxcore.RunOut("sc.exe", "query", name)
		return strings.Contains(out, "RUNNING")
	}
	// v2.5: 不再常驻 40hx_bridge(需测试签名)。Gen2 改 BYOVD:
	//   ThrottleStop(任意物理内存写, EV 预签名) + WinRing0(PCI config) —
	//   两者普通模式(testsigning off)即可加载。安装阶段仅放好驱动文件 +
	//   注册 demand 服务; 真正的加载与自清理由登录后的 -gen2(SYSTEM 任务)
	//   完成 → 用完即卸, 游戏时系统无第三方驱动。
	for _, d := range []struct{ name, file string }{
		{"ThrottleStop", "ThrottleStop.sys"},
		{"WinRing0_1_2_0", "WinRing0x64.sys"},
	} {
		dst := filepath.Join(sysDir, d.file)
		if svcRunning(d.name) {
			fmt.Printf("  %s 已在运行, 跳过覆盖(保持当前状态)\n", d.name)
			continue
		}
		hxcore.RunOut("sc.exe", "stop", d.name)
		// 留一份到 %ProgramData%\40HXUnlock\drivers 作为持久备份源
		// (40HXCheck 实测/Gen2 临时部署都从这里取; System32 的会被用完即卸删除)
		pdDir := filepath.Join(os.Getenv("ProgramData"), "40HXUnlock", "drivers")
		os.MkdirAll(pdDir, 0o755)
		copyEmbedTo(filepath.Join(pdDir, d.file), d.file)
		if err := copyEmbedTo(dst, d.file); err != nil {
			if _, statErr := os.Stat(dst); statErr != nil {
				fmt.Printf("  [!] 复制 %s 失败: %v\n", d.file, err)
				continue
			}
		} else {
			fmt.Printf("  已复制 %s\n", d.file)
		}
		ensureService(d.name, d.file)
	}
	fmt.Println("  Gen2 驱动文件已就绪(demand), 登录后由 SYSTEM 任务临时加载并自清理")
}

// ensureService: 仅注册(或更新)驱动服务, 不在此处加载。
// 安装阶段加载 40hx_bridge(映射 GPU BAR0)会与正在运行的 nvlddmkm 争用硬件,
// 实测导致 40HX 设备报 code19 / 后续启动异常。加载推迟到重启后登录时的 -gen2。
// v2.4.6 关键修复(社区 #1/#2 根因):
//
//	驱动服务注册为 start=demand(手动), 需在登录后由 -gen2 拉起。
//	而 -gen2 走 Run 键以普通用户权限运行 → sc start 需要管理员 →
//	"[SC] StartService: OpenService 失败 5: 拒绝访问" → 驱动永远起不来
//	→ Gen2 永远失败(用户现象: 算力解锁 OK 但 Gen2 ✗)。
//	正解 = 保持 demand(不改成 auto! 详见下), 并把 -gen2 的执行权限升到
//	SYSTEM: 注册 SYSTEM 计划任务(登录时触发 + 延迟 30s)跑 -gen2 -silent,
//	既不需要 UAC 弹窗, 又保留"登录后才加载驱动"的安全时序。
//
// 为什么不改成 start=auto: type=kernel auto 驱动在开机早期由 SCM 加载,
// 会与随后初始化的 nvlddmkm 争用 GPU BAR0 — 历史上实测导致 40HX 报
// code19 / Windows 启动异常进安全模式。demand + 登录后加载是经过验证的时序。
func ensureService(name string, sysFile string) {
	bin := fmt.Sprintf("\\SystemRoot\\System32\\drivers\\%s", sysFile)
	// 创建(已存在会失败, 忽略); 启动类型 demand — 由 SYSTEM 任务登录后拉起
	hxcore.RunOut("sc.exe", "create", name, "type=", "kernel", "start=", "demand", "binPath=", bin)
	out, err := hxcore.RunOut("sc.exe", "query", name)
	if err != nil || !strings.Contains(out, "STATE") {
		fmt.Printf("  [!] 注册服务 %s 失败: %s\n", name, strings.TrimSpace(out))
		return
	}
	// 纠正被安全软件/策略改错的启动类型(Disabled 会导致 Gen2 永远拉不起)
	startType := ""
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "STATE") {
			startType = strings.TrimSpace(ln)
		}
	}
	if strings.Contains(startType, "DISABLED") {
		hxcore.RunOut("sc.exe", "config", name, "start=", "demand")
		startType = strings.Replace(startType, "DISABLED", "DEMAND", 1)
	}
	fmt.Printf("  服务 %s 已注册 (%s), 登录后由 SYSTEM 任务加载\n", name, startType)
}

// ensureSvcLoaded: 确保驱动服务已注册并加载。
// v2.4.6: 由 SYSTEM 任务(或管理员手动)调用时 sc start 才有权限;
// 普通权限(Run 键兜底)下失败属预期 — 静默交给 SYSTEM 任务处理。
func ensureSvcLoaded(name string, sysFile string) {
	if out, _ := hxcore.RunOut("sc.exe", "query", name); strings.Contains(out, "RUNNING") {
		return // 已运行
	}
	bin := fmt.Sprintf("\\SystemRoot\\System32\\drivers\\%s", sysFile)
	hxcore.RunOut("sc.exe", "create", name, "type=", "kernel", "start=", "demand", "binPath=", bin)
	out, err := hxcore.RunOut("sc.exe", "start", name)
	if err != nil {
		fmt.Printf("[Gen2] 启动服务 %s: %s\n", name, strings.TrimSpace(out))
		if !isAdmin() {
			fmt.Println("[Gen2] 当前非管理员 — 交给 SYSTEM 计划任务处理(无需操作)")
		}
	}
}

func setupBootEntry() error {
	// 幂等: 已存在 "40HX Unlock" 项则跳过 (用全量 firmware 枚举, 描述在项详情)
	if out, _ := hxcore.RunOut("bcdedit.exe", "/enum", "firmware"); strings.Contains(out, bootDesc) {
		fmt.Println("    启动项已存在, 跳过")
		return nil
	}
	// 1. copy {bootmgr} 作模板
	out, err := hxcore.RunOut("bcdedit.exe", "/copy", "{bootmgr}", "/d", bootDesc)
	if err != nil {
		return fmt.Errorf("bcdedit copy: %v", err)
	}
	re := regexp.MustCompile(`\{([0-9a-fA-F-]{36})\}`)
	m := re.FindStringSubmatch(out)
	if len(m) < 2 {
		return errors.New("无法解析 bcdedit 输出: " + out)
	}
	guid := m[1]
	cleanup := func() { hxcore.RunOut("bcdedit.exe", "/delete", "{"+guid+"}", "/f") }

	// 2. 找 ESP 盘符 (mountvol 重挂)
	esp := hxcore.MountESP()
	if esp == "" {
		cleanup()
		return errors.New("无法挂载 ESP")
	}
	defer hxcore.UnmountESP(esp)

	// 3. set device + path
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{"+guid+"}", "device", "partition="+esp+":"); err != nil {
		cleanup()
		return err
	}
	path := efiDir + "\\" + efiFile // \EFI\40HX\40HXUNLK.EFI
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{"+guid+"}", "path", path); err != nil {
		cleanup()
		return err
	}
	// 4. displayorder addfirst
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{fwbootmgr}", "displayorder", "{"+guid+"}", "/addfirst"); err != nil {
		cleanup()
		return err
	}
	fmt.Printf("    启动项 %s 已置顶\n", guid)
	return nil
}

func setRunKey() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("  [!] 无法获取 exe 路径:", err)
		return
	}
	abs, _ := filepath.Abs(exe)
	val := fmt.Sprintf("\"%s\" -gen2 -silent", abs)
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		k, _, err = registry.CreateKey(registry.CURRENT_USER,
			`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	}
	if err != nil {
		fmt.Println("  [!] Run 键写入失败:", err)
		return
	}
	defer k.Close()
	if err := k.SetStringValue("40HXGen2", val); err != nil {
		fmt.Println("  [!] Run 键设置失败:", err)
		return
	}
	fmt.Println("  Gen2 登录自启动已注册 -> " + abs)
}

// setupGen2Task: v2.4.6 核心 — 注册 SYSTEM 计划任务, 登录时(延迟 30s)以
// 最高权限静默执行 -gen2。
//
// 为什么需要它: 驱动服务是 demand 启动, 登录后需 sc start 拉起, 而 sc start
// 需要管理员。Run 键以普通用户权限跑 → "OpenService 失败 5: 拒绝访问" →
// 驱动永远起不来 → Gen2 永远失败(社区 #1/#2 的真实根因)。
// 为什么不用 UAC 提权: 每次登录弹 UAC 体验差, 且 UAC 关闭时静默降权仍失败。
// SYSTEM 任务 = 无声的管理员: 权限最高、无弹窗、时机仍在登录后(安全)。
// 注意保持 demand: 若改 auto 会在开机早期加载驱动, 与 nvlddmkm 争用 BAR0
// (历史实测 code19 / 启动异常), demand + 登录后加载才是验证过的时序。
func setupGen2Task() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("  [!] 无法获取 exe 路径:", err)
		return
	}
	abs, _ := filepath.Abs(exe)
	tn := gen2TaskName
	out, err := hxcore.RunOut("schtasks.exe", "/create", "/tn", tn,
		"/tr", fmt.Sprintf("\"%s\" -gen2 -silent", abs),
		"/sc", "onlogon", "/ru", "SYSTEM", "/delay", "0000:30", "/f")
	if err != nil {
		fmt.Printf("  [!] Gen2 计划任务创建失败: %s\n", strings.TrimSpace(out))
		fmt.Println("      可手动: 以管理员运行 40HXInstaller.exe -gen2")
		return
	}
	fmt.Println("  Gen2 任务已注册: 登录时 SYSTEM 权限执行(延迟30s, 静默)")
}

// ===================== Gen2 解锁 (原生, 无 python) =====================

func gen2Main() {
	// 幂等; -silent(登录自启动调用)时全程无窗口静默
	// v2.5: BYOVD (ThrottleStop + WinRing0) — 免测试签名; 用完即卸(自清理)
	ensureGspSilent()
	defer cleanupByovd() // 注册最早→最后执行(在句柄 Close 后), 失败也清理

	// 驱动文件可能被上次"用完即卸"删除, 每次从 embed 重新放好
	sysDir := os.Getenv("SystemRoot") + "\\System32\\drivers"
	for _, df := range []string{"ThrottleStop.sys", "WinRing0x64.sys"} {
		if _, err := os.Stat(filepath.Join(sysDir, df)); err != nil {
			copyEmbedTo(filepath.Join(sysDir, df), df) // 占用中忽略错误
		}
	}
	ensureSvcLoaded("ThrottleStop", "ThrottleStop.sys")
	ensureSvcLoaded("WinRing0_1_2_0", "WinRing0x64.sys")

	th, err := hxcore.OpenThrottleStop()
	if err != nil {
		if !isAdmin() {
			fmt.Println("[Gen2] ThrottleStop 未加载且当前非管理员 — 交给 SYSTEM 任务处理, 静默退出")
			gen2StatusFail("ThrottleStop 驱动未加载(由 SYSTEM 任务负责拉起)")
			return
		}
		fmt.Println("[Gen2] ThrottleStop 驱动未运行。请重跑安装器(管理员)后重启。")
		gen2StatusFail("ThrottleStop 驱动未运行 (需管理员重跑安装器)")
		gen2Notify("ThrottleStop 驱动未运行。\n请右键安装程序 -> 以管理员身份运行, 再重启。")
		return
	}
	defer hxcore.CloseHandle(th)

	wh, err := hxcore.OpenDevice(`\\.\WinRing0_1_2_0`)
	if err != nil {
		if !isAdmin() {
			fmt.Println("[Gen2] WinRing0 未加载且当前非管理员 — 交给 SYSTEM 任务处理, 静默退出")
			gen2StatusFail("WinRing0 驱动未加载, 且当前为普通权限(由 SYSTEM 任务负责拉起)")
			return
		}
		fmt.Println("[Gen2] WinRing0 驱动未运行。")
		gen2StatusFail("WinRing0 驱动未运行 (需管理员重跑安装器)")
		gen2Notify("WinRing0 驱动未运行。\n请右键安装程序 -> 以管理员身份运行, 再重启。")
		return
	}
	defer hxcore.CloseHandle(wh)

	// 定位 40HX (VEN_10DE&DEV_1F0B), 不硬编码 BDF
	gpuBDF, gpuFound := hxcore.FindGPUPCI(wh)
	if !gpuFound {
		fmt.Println("[Gen2] 未能定位 40HX (VEN_10DE&DEV_1F0B)。请发日志。")
		gen2StatusFail("未能在 PCI 总线上定位 40HX (VEN_10DE&DEV_1F0B)")
		gen2Notify("未能在 PCI 总线上找到 40HX。\n请确认显卡已插好且驱动已装。")
		return
	}
	gpuBus := (gpuBDF >> 8) & 0xFF
	fmt.Printf("[Gen2] 40HX 位于 %02x:%02x.%x\n", gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7)
	cur := hxcore.LinkSpeed(wh, gpuBDF)
	fmt.Printf("[Gen2] 当前链路: Gen%d\n", cur)
	if cur >= 2 {
		fmt.Println("[Gen2] 已是 Gen2, 无需操作。")
		hxcore.WriteGen2Status(fmt.Sprintf("✅ Gen2 无需操作: 当前链路已是 Gen%d\n运行身份: %s\n40HX 位置: %02x:%02x.%x\n",
			cur, map[bool]string{true: "管理员/SYSTEM", false: "普通用户(受限)"}[isAdmin()],
			gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7))
		gen2Notify("PCIe 已是 Gen" + fmt.Sprint(cur) + ", 无需操作。")
		return
	}

	// 1. PL0 writes (BAR0) — 经 ThrottleStop 物理内存写
	fmt.Println("[Gen2] 写 XVE/链路寄存器 (ThrottleStop)...")
	pl0 := []struct {
		off  uint64
		val  uint32
		name string
	}{
		{0x8872C, 0x6, "XVE_OVR=6"},
		{0x8C040, 0x80085800, "LINK_CONFIG_0"},
		{0x8841C, 0xE0B42D00, "PRIV_MISC_1"},
		{0x8C2C0, 0x068731B3, "CYA_0"},
	}
	bar0raw, _ := hxcore.PciRd(wh, gpuBDF, 0x10)
	if bar0raw == 0 || bar0raw == 0xFFFFFFFF {
		bar0raw = 0xF6000000
	}
	bar0Phys := uint64(bar0raw & 0xFFFFFFF0)
	fmt.Printf("[Gen2] BAR0 = 0x%08X\n", bar0Phys)
	for _, p := range pl0 {
		if werr := hxcore.TSWrite(th, bar0Phys+p.off, p.val); werr != nil {
			fmt.Printf("  [!] %s 写失败: %v\n", p.name, werr)
			continue
		}
		if rb, rerr := hxcore.TSRead(th, bar0Phys+p.off); rerr != nil || rb != p.val {
			fmt.Printf("  [warn] %s 读回 0x%08x (期望 0x%08x)\n", p.name, rb, p.val)
		} else {
			fmt.Printf("  %s OK (0x%08X)\n", p.name, rb)
		}
	}

	// 2. LNKCTL2 TLS=2 (GPU + root)
	root := hxcore.FindRootPort(wh, gpuBus)
	if root == 0xFFFFFFFF {
		fmt.Println("[Gen2] 未找到 root port, 用 GPU retrain fallback")
	}
	fmt.Printf("[Gen2] root port = 00:%02x.%x\n", (root>>3)&0x1F, root&7)
	for _, b := range []struct {
		bdf uint32
		tag string
	}{{gpuBDF, "GPU"}, {root, "ROOT"}} {
		if b.bdf == 0xFFFFFFFF {
			continue
		}
		cap := hxcore.PcieCap(wh, b.bdf)
		if cap == 0 {
			continue
		}
		l2 := []byte{2, 0}
		hxcore.PciWr(wh, b.bdf, cap+0x30, l2)
		rb, _ := hxcore.PciRd(wh, b.bdf, cap+0x30)
		fmt.Printf("  %s LNKCTL2 TLS=2 rb=0x%04X\n", b.tag, rb&0xFFFF)
	}

	// 3. UPGRADE retrain: 清位→置位脉冲 (只置位在 40HX 上不生效)
	retrain := func(bdf uint32) {
		cap := hxcore.PcieCap(wh, bdf)
		if cap == 0 {
			return
		}
		ctl, _ := hxcore.PciRd(wh, bdf, cap+0x10)
		lo := uint16(ctl & 0xFFFF)
		buf := []byte{byte(lo & 0xFF), byte((lo >> 8) & 0xFF)}
		buf[0] &^= 0x20 // clear bit5
		hxcore.PciWr(wh, bdf, cap+0x10, buf)
		time.Sleep(300 * time.Millisecond)
		ctl2, _ := hxcore.PciRd(wh, bdf, cap+0x10)
		lo2 := uint16(ctl2 & 0xFFFF)
		buf2 := []byte{byte(lo2 & 0xFF), byte((lo2 >> 8) & 0xFF)}
		buf2[0] |= 0x20 // set bit5
		hxcore.PciWr(wh, bdf, cap+0x10, buf2)
	}
	if root != 0xFFFFFFFF {
		retrain(root)
	} else {
		retrain(gpuBDF)
	}
	fmt.Println("[Gen2] 等待链路重训...")
	time.Sleep(4 * time.Second)
	cur = hxcore.LinkSpeed(wh, gpuBDF)

	gen2Verdict := ""
	if cur >= 2 {
		fmt.Printf("[Gen2] *** GEN2 ACHIEVED (Gen%d) ***\n", cur)
		gen2Verdict = fmt.Sprintf("✅ Gen2 成功: 当前链路 Gen%d", cur)
	} else {
		fmt.Printf("[Gen2] 仍在 Gen%d, 解锁失败。请发日志。\n", cur)
		gen2Verdict = fmt.Sprintf("❌ Gen2 失败: 仍在 Gen%d", cur)
	}
	st := fmt.Sprintf("结论: %s\n运行身份: %s\n40HX 位置: %02x:%02x.%x\nRoot Port: %02x:%02x.%x\n"+
		"驱动: ThrottleStop=✓ WinRing0=✓ (BYOVD, 用完即卸)\n",
		gen2Verdict,
		map[bool]string{true: "管理员/SYSTEM", false: "普通用户(受限)"}[isAdmin()],
		gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7,
		(root>>8)&0xFF, (root>>3)&0x1F, root&7)
	if wErr := hxcore.WriteGen2Status(st); wErr != nil {
		fmt.Printf("[Gen2] 状态文件写入失败(不影响解锁): %v\n", wErr)
	}
	if !hasArg("-silent") && !hasArg("-y") {
		icon := uint(mbIconInfo)
		txt := fmt.Sprintf("PCIe 链路: Gen%d\n", cur)
		if cur >= 2 {
			txt += "=== GEN2 解锁成功 ==="
		} else {
			txt += "仍在 Gen1, 解锁失败(详见日志)。"
			icon = mbIconError
		}
		msgbox("40HX Gen2", txt, icon)
	}
}

// cleanupByovd: v2.5 用完即卸 — 停止并删除 ThrottleStop/WinRing0 服务与驱动文件。
// 在 gen2Main 末尾(defers)执行, 游戏时系统无第三方驱动残留。
func cleanupByovd() {
	for _, d := range []struct{ name, file string }{
		{"ThrottleStop", "ThrottleStop.sys"},
		{"WinRing0_1_2_0", "WinRing0x64.sys"},
	} {
		hxcore.RunOut("sc.exe", "stop", d.name)
		hxcore.RunOut("sc.exe", "delete", d.name)
		os.Remove(filepath.Join(os.Getenv("SystemRoot")+"\\System32\\drivers", d.file))
	}
}

// gen2Notify: 失败提示; 静默模式不弹框
func gen2Notify(txt string) {
	if !hasArg("-silent") && !hasArg("-y") {
		msgbox("40HX Gen2", txt, mbIconError)
	}
}

// gen2StatusFail: v2.4.6 — 把 Gen2 未执行/失败的原因写入状态文件,
// 供 40HXCheck 展示(SYSTEM 任务在 Session 0 无法弹窗给用户看)。
func gen2StatusFail(reason string) {
	ident := map[bool]string{true: "管理员/SYSTEM", false: "普通用户(受限)"}[isAdmin()]
	hxcore.WriteGen2Status("❌ Gen2 未执行: " + reason + "\n运行身份: " + ident + "\n")
}

// ===================== 卸载/状态 =====================

func uninstall() {
	if !isAdmin() {
		fmt.Println("[!] 需要管理员权限。")
		msgbox("40HX 安装器", "需要管理员权限。\n请右键本程序 -> 以管理员身份运行。", mbIconError)
		return
	}
	if lockOnce(`Local\40HXUninstaller_v1`) == nil {
		msgbox("40HX 安装器", "卸载程序已在运行, 请勿重复点击。", mbIconInfo)
		return
	}
	fmt.Println("=== 卸载 40HX 解锁 ===")
	// Run key
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err == nil {
		k.DeleteValue("40HXGen2")
		k.Close()
		fmt.Println("[1] 已删除 Gen2 登录自启动")
	}
	// 旧版开机计划任务(会弹窗)
	for _, tn := range []string{"40HXGen2", "40HX PCIe Gen2 Bring-up"} {
		hxcore.RunOut("schtasks.exe", "/delete", "/tn", tn, "/f")
	}
	fmt.Println("[1b] 已清理旧开机计划任务(如有)")
	// 启动项
	out, _ := hxcore.RunOut("bcdedit.exe", "/enum", "{fwbootmgr}")
	// 找我们添加的项(displayorder 第一且描述含 40HX)——简化: 枚举删除所有 "40HX Unlock"
	re := regexp.MustCompile(`\{([0-9a-fA-F-]{36})\}`)
	fmt.Println("[2] 尝试删除固件启动项 '40HX Unlock'...")
	// 需要先找 GUID: 直接查 fwbootmgr displayorder 第一个
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, bootDesc) || (i > 0 && strings.Contains(lines[i-1], bootDesc)) {
			m := re.FindStringSubmatch(ln)
			if len(m) > 1 {
				hxcore.RunOut("bcdedit.exe", "/delete", "{"+m[1]+"}", "/f")
			}
		}
	}
	// 驱动
	hxcore.RunOut("sc.exe", "stop", "40hx_bridge")
	hxcore.RunOut("sc.exe", "delete", "40hx_bridge")
	hxcore.RunOut("sc.exe", "stop", "WinRing0_1_2_0")
	hxcore.RunOut("sc.exe", "delete", "WinRing0_1_2_0")
	fmt.Println("[3] 驱动服务已删除")
	fmt.Println("卸载完成。EFI 文件与 BIOS 启动项请手动处理(如有残留)。")
	msgbox("40HX 安装器", "卸载完成。\nEFI 文件与 BIOS 启动项如有残留请手动处理(见日志)。", mbIconInfo)
}

func status() {
	fmt.Println("=== 40HX 解锁状态 ===")
	gpuOK := hxcore.FindGPU()
	sb := hxcore.SecureBootOn()
	ts := hxcore.TestSigningOn()
	gs := hxcore.GspEnabled()
	fmt.Printf("GPU 40HX 检测: %v\n", gpuOK)
	fmt.Printf("Secure Boot: %v\n", sb)
	fmt.Printf("测试签名: %v\n", ts)
	fmt.Printf("GSP 启用 (EnableGpuFirmware=1): %v\n", gs)
	// v2.4.1: GSP 定位诊断 — 伪装/魔改驱动会 AdapterString≠"CMP 40HX"
	if sub, adapter, fw := hxcore.GspDiag(); sub != "" {
		fmt.Printf("  GSP 键: Class\\%s (fw=%d)\n", sub, fw)
		fmt.Printf("  AdapterString: %s\n", adapter)
	} else {
		fmt.Println("  [!] " + adapter) // 无匹配时 hxcore.GspDiag 返回诊断串
	}
	// 驱动与解锁实测: 主判据 = 设备实际可打开(不依赖 sc.exe — 部分安全环境禁用它)
	// v2.5: TS(ThrottleStop) + WinRing0 BYOVD, 不再需要 40hx_bridge
	st := hxcore.ReadUnlockStateV2(5, 800)
	tsRun := st.TSOK
	winringRun := st.WinRingOK
	fmt.Printf("ThrottleStop: %v\n", tsRun)
	fmt.Printf("WinRing0: %v\n", winringRun)
	if tsRun && winringRun {
		fmt.Printf("PCIe 链路: Gen%d\n", st.Speed)
		if st.SS0OK {
			fmt.Printf("SS0(算力): 0x%08x %s\n", st.SS0, map[bool]string{true: "(已解锁)", false: "(锁定)"}[st.Unlocked])
		}
	} else {
		fmt.Println("驱动未运行(装好后 Gen2/状态可用)")
	}
	ss0 := st.SS0
	ss0ok := st.SS0OK
	speed := st.Speed
	// v2.4: 弹窗带诊断与处置建议(社区用户不依赖日志)
	diag := []string{}
	if !gpuOK {
		diag = append(diag, "· 未检测到 40HX —— 请确认显卡已插入且驱动已装")
	}
	if sb {
		diag = append(diag, "· Secure Boot 开启: 需进 BIOS 关闭, 否则解锁 EFI 被拒")
	}
	if !ts {
		diag = append(diag, "· 测试签名已开启 — v2.5 不需要, 可 bcdedit /set testsigning off 关闭")
	}
	if !gs {
		diag = append(diag, "· GSP 未启用: 解锁后可能黑屏。运行安装器(自动设 EnableGpuFirmware=1)")
	}
	if !tsRun || !winringRun {
		diag = append(diag, "· 驱动未运行: 重启后登录会自动拉起; 或手动运行 40HXInstaller.exe -gen2")
	}
	if tsRun && winringRun {
		if !ss0ok {
			diag = append(diag, "· 驱动已运行但读不到算力寄存器(异常)")
		} else if ss0 == 0x88888888 {
			diag = append(diag, fmt.Sprintf("· SS0=0x%08x: 算力已解锁! PCIe Gen%d", ss0, speed))
		} else {
			diag = append(diag, fmt.Sprintf("· SS0=0x%08x: 算力仍锁定 —— 重启时 40HX Unlock EFI 未成功执行", ss0))
			// v2.4.4: 读 EFI 解锁日志(40hx_log.txt)做自动诊断, 不再需要人工看日志
			efiDiag := hxcore.AnalyzeEfiLog()
			if efiDiag != "" {
				diag = append(diag, efiDiag)
			}
		}
	}
	msg := "40HX 解锁状态\n========================\n"
	msg += fmt.Sprintf("GPU 40HX: %v    Secure Boot: %v\n", map[bool]string{true: "✓", false: "✗"}[gpuOK], map[bool]string{true: "开启!", false: "关闭(OK)"}[sb])
	msg += fmt.Sprintf("测试签名: %v    GSP: %v\n", map[bool]string{true: "✓", false: "✗"}[ts], map[bool]string{true: "✓", false: "✗"}[gs])
	msg += fmt.Sprintf("ThrottleStop: %v  WinRing0: %v\n", map[bool]string{true: "✓", false: "✗"}[tsRun], map[bool]string{true: "✓", false: "✗"}[winringRun])
	if tsRun && winringRun {
		msg += fmt.Sprintf("PCIe: Gen%d    SS0: 0x%08x\n", speed, ss0)
	}
	msg += "\n诊断:\n" + strings.Join(diag, "\n")
	if len(diag) == 0 {
		msg += "· 一切正常"
	}
	msg += "\n\n详细日志: " + filepath.Join(os.TempDir(), "40HX_installer.log")
	msgbox("40HX 状态", msg, mbIconInfo)
	fmt.Println("=== 状态结束 ===")
}

func pause() {
	// GUI 版: 无需按 Enter; 输出已入日志, 交互收尾用消息框
}
