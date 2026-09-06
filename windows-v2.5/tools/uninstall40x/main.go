// 40HX 一键卸载工具 v2.4.5 (CMP 40HX Windows Unlock Uninstaller)
// GUI 无窗口版: 双击不弹黑框, 输出入 %TEMP%\40HX_uninstaller.log, 结束弹消息框
// 移除: 开机计划任务(旧版弹窗源) / Gen2 Run 键 / 固件启动项 "40HX Unlock"
//
//	/ ESP 解锁 EFI(含 \EFI\Boot\bootx64.efi 回退副本并还原 .bak)
//	/ 驱动服务与文件 / EnableGpuFirmware(GSP 恢复默认关)
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const bootDesc = "40HX Unlock"

// v2.3: GSP 启用注册表 (卸载时需删除恢复默认)
// v2.4.2: findGpuClassKey 改用 Enum\PCI Driver 反查(伪装驱动免疫),
// 伪装名(2070/2060S)兜底 — 保证卸载时能找到伪装卡的 GSP 键并还原。
const (
	gpuClassPath  = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	gpuClassGUID  = `{4d36e968-e325-11ce-bfc1-08002be10318}`
	gpuEnumBase   = `SYSTEM\CurrentControlSet\Enum\PCI`
	gpuVenDev     = "VEN_10DE&DEV_1F0B"
	gpuEnableFw   = "EnableGpuFirmware"
	gpuAdapterStr = "HardwareInformation.AdapterString"
	gpuAdapter40  = "CMP 40HX"
)

// findGpuClassKey: 找 40HX 的显示适配器 Class 子键 (0000/0001/...)
// v2.4.2: Enum Driver 反查优先 + 名字兜底(兼容伪装成 2070/2060S 的驱动)
func findGpuClassKey() string {
	if key := findGpuClassKeyByEnum(); key != "" {
		return key
	}
	return findGpuClassKeyByName()
}

// findGpuClassKeyByEnum: Enum\PCI\VEN_10DE&DEV_1F0B 实例的 Driver 值反查
func findGpuClassKeyByEnum() string {
	base, err := registry.OpenKey(registry.LOCAL_MACHINE, gpuEnumBase,
		registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer base.Close()
	devs, err := base.ReadSubKeyNames(-1)
	if err != nil {
		return ""
	}
	for _, d := range devs {
		if !strings.Contains(d, gpuVenDev) {
			continue
		}
		dk, err := registry.OpenKey(registry.LOCAL_MACHINE, gpuEnumBase+`\`+d,
			registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		insts, _ := dk.ReadSubKeyNames(-1)
		dk.Close()
		for _, inst := range insts {
			ik, err := registry.OpenKey(registry.LOCAL_MACHINE,
				gpuEnumBase+`\`+d+`\`+inst, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			drv, _, _ := ik.GetStringValue("Driver")
			ik.Close()
			if strings.Contains(drv, gpuClassGUID) {
				sub := drv[strings.LastIndex(drv, `\`)+1:]
				return gpuClassPath + `\` + sub
			}
		}
	}
	return ""
}

// findGpuClassKeyByName: AdapterString/DriverDesc 名字兜底 (兼容伪装名)
func findGpuClassKeyByName() string {
	alias := []string{gpuAdapter40, "2070", "2060", "2060 SUPER", "2060 super"}
	base, err := registry.OpenKey(registry.LOCAL_MACHINE, gpuClassPath,
		registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer base.Close()
	names, err := base.ReadSubKeyNames(-1)
	if err != nil {
		return ""
	}
	for _, n := range names {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, gpuClassPath+`\`+n,
			registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		adapter, _, _ := k.GetStringValue(gpuAdapterStr)
		desc, _, _ := k.GetStringValue("DriverDesc")
		hay := adapter + " " + desc
		k.Close()
		for _, a := range alias {
			if strings.Contains(hay, a) {
				return gpuClassPath + `\` + n
			}
		}
	}
	return ""
}

// delGspKey: 删除 EnableGpuFirmware (恢复 GSP 默认关)
func delGspKey() bool {
	key := findGpuClassKey()
	if key == "" {
		return false
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	if err := k.DeleteValue(gpuEnableFw); err != nil {
		return false
	}
	return true
}

var silent bool

// ---- GUI helpers (v2.3: 无 console, 消息框 + 日志) ----

const (
	mbIconInfo  = 0x40
	mbIconError = 0x10
)

var (
	procMsgBoxW       = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	procCreateMutex   = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	procShellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
)

func msgbox(title, text string, icon uint) {
	// -y / -silent(自动化) 时不弹框
	if silent {
		return
	}
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(icon))
}

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

// selfElevate: 非管理员时 ShellExecute "runas" 重启自身(触发 UAC), 父进程退出
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
		msgbox("40HX 卸载工具", fmt.Sprintf("提权失败(错误码 %d)。\n请右键本程序 -> 以管理员身份运行。", r), mbIconError)
	}
	os.Exit(0)
}

func main() {
	for _, a := range os.Args {
		if a == "-y" || a == "-silent" {
			silent = true
		}
	}
	// GUI 版: 输出镜像到日志
	setupLog("40HX_uninstaller.log")
	fmt.Println("==============================================")
	fmt.Println("  CMP 40HX Windows Unlock 卸载工具 v2.5")
	fmt.Println("  移除: 开机计划任务 / 解锁启动项 / ESP EFI / Gen2 自启动 / 驱动")
	fmt.Println("==============================================")
	if !isAdmin() {
		if hasArg("-elevated") {
			// 已提权过一次仍失败(静默提权策略/受限token) -> 禁止再循环, 直接报错
			msgbox("40HX 卸载工具", "提权失败：当前账户无法获得管理员权限。\n请右键本程序 -> 以管理员身份运行。", mbIconError)
			return
		}
		selfElevate()
		return
	}
	if lockOnce(`Local\40HXUninstaller_v1`) == nil {
		msgbox("40HX 卸载工具", "卸载程序已在运行, 请勿重复点击。", mbIconInfo)
		return
	}

	// 1. 旧版开机计划任务(早期方案遗留, 会反复弹命令窗口!)
	fmt.Print("[1/8] 删除旧开机计划任务 ... ")
	if delTasks() {
		fmt.Println("完成")
	} else {
		fmt.Println("未找到(跳过)")
	}

	// 2. Gen2 Run 键
	fmt.Print("[2/8] 删除 Gen2 登录自启动 ... ")
	delRunKey()
	fmt.Println("完成")

	// 3. 固件启动项
	fmt.Print("[3/8] 删除固件启动项 '40HX Unlock' ... ")
	if delBootEntry() {
		fmt.Println("完成")
	} else {
		fmt.Println("未找到(可能已移除)")
	}

	// 4. ESP 解锁 EFI 文件
	fmt.Print("[4/8] 删除 ESP 解锁 EFI ... ")
	if delEspEfi() {
		fmt.Println("完成")
	} else {
		fmt.Println("未找到/跳过")
	}

	// 5. 驱动服务
	fmt.Println("[5/8] 停止并删除驱动服务...")
	delService("ThrottleStop") // v2.5 BYOVD
	delService("40hx_bridge")
	delService("40hx_early")
	delService("WinRing0_1_2_0")
	delService("WinRing0x64")
	delService("WinRing0")

	// 6. 驱动文件
	fmt.Println("[6/8] 删除驱动文件...")
	delDriverFile("ThrottleStop.sys") // v2.5 BYOVD
	delDriverFile("40hx_bridge.sys")
	delDriverFile("40hx_early-d.sys")
	delDriverFile("40hx_early.sys")
	delDriverFile("WinRing0x64.sys")
	delSystemFile("WinRing0x64.dll")

	// 6.5 GSP 注册表 (v2.3 安装器设的 EnableGpuFirmware=1, 卸载需恢复默认)
	fmt.Print("[6.5/8] 删除 EnableGpuFirmware (恢复 GSP 默认关) ... ")
	if delGspKey() {
		fmt.Println("完成")
	} else {
		fmt.Println("未找到(跳过)")
	}

	// 6.6 ProgramData 残留: gen2_status.txt(诊断会当"结果"显示!) + drivers 备份源
	fmt.Print("[6.6/8] 清理 %ProgramData%\\40HXUnlock ... ")
	delProgramData()
	fmt.Println("完成")

	// 6.7 Defender 排除清理(安装时加的白名单, 卸载需移除不留残留)
	fmt.Print("[6.7/8] 清理 Defender 排除项 ... ")
	if err := removeDefenderExclusions(); err != nil {
		fmt.Println("未执行(可忽略):", err)
	} else {
		fmt.Println("完成")
	}

	// 7. 状态确认
	fmt.Println("[7/8] 检查残留...")
	leftover := checkLeftover()

	fmt.Println()
	fmt.Println("卸载完成。建议重启电脑。")
	if silent {
		return
	}
	icon := uint(mbIconInfo)
	txt := "卸载完成。\n建议重启电脑。\n"
	if leftover != "" {
		icon = mbIconError
		txt += "\n仍有残留:\n" + leftover
	}
	txt += "\n详细日志: " + filepath.Join(os.TempDir(), "40HX_uninstaller.log")
	msgbox("40HX 卸载工具", txt, icon)
}

func argIndex(name string) int {
	for i, a := range os.Args {
		if a == name {
			return i
		}
	}
	return -1
}

func hasArg(name string) bool {
	for _, a := range os.Args {
		if a == name {
			return true
		}
	}
	return false
}

func isAdmin() bool {
	t, err := windows.OpenCurrentProcessToken()
	if err == nil {
		defer t.Close()
		var buf [4]byte
		var need uint32
		if err = windows.GetTokenInformation(t, windows.TokenElevation,
			&buf[0], uint32(len(buf)), &need); err == nil && buf[0] != 0 {
			return true
		}
		// TokenElevation 可能因受限环境(沙箱/服务)误报 0, 再试 SCM 全权
	}
	// 兜底: 能以 ALL_ACCESS 打开服务控制管理器 = 真管理员
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		windows.CloseServiceHandle(scm)
		return true
	}
	return false
}

func runOut(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---- 1. 旧开机计划任务(早期方案会弹窗!) ----
func delTasks() bool {
	removed := false
	for _, tn := range []string{"40HXGen2", "40HX PCIe Gen2 Bring-up", "40HXGspEnsure"} {
		out, err := runOut("schtasks.exe", "/delete", "/tn", tn, "/f")
		if err == nil || strings.Contains(out, "成功") || strings.Contains(strings.ToLower(out), "success") {
			fmt.Printf("  已删除计划任务 %s\n", tn)
			removed = true
		}
	}
	return removed
}

// ---- 2. Run 键 ----
func delRunKey() {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err == nil {
		k.DeleteValue("40HXGen2")
		k.Close()
	}
}

// ---- 3. 固件启动项 ----
func delBootEntry() bool {
	out, err := runOut("bcdedit.exe", "/enum", "firmware")
	if err != nil {
		return false
	}
	lines := strings.Split(out, "\n")
	curGuid := ""
	re := regexp.MustCompile(`\{([0-9a-fA-F-]{36})\}`)
	removed := false
	for _, ln := range lines {
		if m := re.FindStringSubmatch(ln); len(m) > 1 {
			// "标识符 {guid}" 行
			if strings.Contains(ln, "{") && !strings.Contains(ln, "displayorder") &&
				!strings.Contains(ln, "bootsequence") {
				curGuid = m[1]
			}
		}
		if strings.Contains(ln, bootDesc) && curGuid != "" {
			runOut("bcdedit.exe", "/delete", "{"+curGuid+"}", "/f")
			fmt.Printf("  已删除启动项 %s\n", curGuid)
			removed = true
			curGuid = ""
		}
	}
	return removed
}

// ---- 4. ESP EFI ----
func mountESP() string {
	for _, c := range []string{"Y", "X", "W", "V", "U", "T", "S"} {
		letter := c + ":"
		out, _ := runOut("mountvol.exe", letter, "/S")
		if strings.Contains(out, "错误") || strings.Contains(out, "denied") {
			continue
		}
		if _, err := os.Stat(letter); err == nil {
			return c
		}
	}
	return ""
}

func delEspEfi() bool {
	esp := mountESP()
	if esp == "" {
		return false
	}
	defer runOut("mountvol.exe", esp+":", "/D")
	removed := false
	// v2.4: 主路径 \EFI\40HX\40HXUNLK.EFI
	target := esp + ":\\EFI\\40HX\\40HXUNLK.EFI"
	if err := os.Remove(target); err == nil {
		removed = true
	}
	// 目录若空则删
	dir := esp + ":\\EFI\\40HX"
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
	// v2.5.1: 回退路径还原改用"覆盖写 + 校验", 不用 删→rename(有中间窗口:
	// 删除成功但 rename 失败会导致 bootx64.efi 永久缺失, 机器起不来)。
	std := esp + ":\\EFI\\Boot\\bootx64.efi"
	bak := esp + ":\\EFI\\Boot\\bootx64.efi.40hx.bak"
	if data, berr := os.ReadFile(bak); berr == nil {
		// 1. 先把原文件内容写入 std(覆盖当前的 40HX 副本) — 任何时刻 std 都存在
		if werr := os.WriteFile(std, data, 0o644); werr != nil {
			fmt.Println("  [!] 还原 bootx64.efi 写失败:", werr)
			fmt.Println("      原备份仍保留在 bootx64.efi.40hx.bak, 可手动还原")
			return removed
		}
		// 2. 读回校验
		if rb, rerr := os.ReadFile(std); rerr == nil && len(rb) == len(data) {
			// 3. 校验通过才删备份
			os.Remove(bak)
			fmt.Println("    已还原原 bootx64.efi (来自 .40hx.bak, 校验 OK)")
		} else {
			fmt.Println("  [!] bootx64.efi 还原后校验不一致 — 保留 .bak 供手动处理")
		}
		removed = true
	}
	return removed
}

// ---- 5. 服务 ----
func delService(name string) {
	runOut("sc.exe", "stop", name)
	time.Sleep(300 * time.Millisecond)
	out, err := runOut("sc.exe", "delete", name)
	if err == nil || strings.Contains(strings.ToLower(out), "success") ||
		strings.Contains(out, "成功") {
		fmt.Printf("  服务 %s 已删除\n", name)
	} else if strings.Contains(out, "不存在") || strings.Contains(out, "not") ||
		strings.Contains(out, "1060") {
		fmt.Printf("  服务 %s 不存在(跳过)\n", name)
	} else {
		fmt.Printf("  服务 %s 删除失败: %s\n", name, strings.TrimSpace(out))
	}
}

// ---- 6. 文件 ----
func delDriverFile(name string) {
	p := os.Getenv("SystemRoot") + "\\System32\\drivers\\" + name
	if err := os.Remove(p); err != nil {
		if _, statErr := os.Stat(p); statErr == nil {
			fmt.Printf("  %s 删除失败(可能被占用, 重启后自动可删)\n", name)
		}
	} else {
		fmt.Printf("  已删除 %s\n", name)
	}
}

func delSystemFile(name string) {
	p := os.Getenv("SystemRoot") + "\\System32\\" + name
	os.Remove(p)
}

// ---- 6.6 ProgramData 残留 (v2.5 安装器备份源 + gen2_status 历史缓存) ----
func delProgramData() {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	dir := base + "\\40HXUnlock"
	// gen2_status.txt: 诊断工具 ReadGen2Status 会把它当"上次 Gen2 结果"显示;
	// 卸载后必须删除, 否则用户看到残留的 "✅ Gen2" 误以为解锁仍生效!
	_ = os.Remove(dir + "\\gen2_status.txt")
	// drivers/: 安装器留的 ThrottleStop/WinRing0 备份源
	_ = os.RemoveAll(dir + "\\drivers")
	// 目录空则删
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}

// ---- 6.7 Defender 排除清理 (与安装器 Add-MpPreference 对称) ----
func removeDefenderExclusions() error {
	var ps []string
	sys := os.Getenv("SystemRoot")
	if sys == "" {
		sys = `C:\Windows`
	}
	for _, f := range []string{"ThrottleStop.sys", "WinRing0x64.sys"} {
		ps = append(ps, sys+"\\System32\\drivers\\"+f)
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	ps = append(ps, pd+"\\40HXUnlock\\drivers")
	if exe, err := os.Executable(); err == nil {
		ps = append(ps, filepath.Dir(exe))
	}
	if len(ps) == 0 {
		return nil
	}
	q := make([]string, 0, len(ps))
	for _, p := range ps {
		q = append(q, "'"+strings.ReplaceAll(p, "'", "''")+"'")
	}
	_, err := runOut("powershell.exe", "-NoProfile", "-NonInteractive",
		"-Command", "Remove-MpPreference -ExclusionPath @("+strings.Join(q, ",")+")")
	return err
}

// ---- 7. 残留检查 ----
func checkLeftover() string {
	var rem []string
	if out, _ := runOut("bcdedit.exe", "/enum", "firmware"); strings.Contains(out, bootDesc) {
		rem = append(rem, "- 固件启动项 '40HX Unlock'(BIOS 手动删除)")
		fmt.Println("  [!] 启动项仍有残留: bcdedit /delete {guid} /f (见 BIOS 菜单)")
	} else {
		fmt.Println("  启动项: 已清理")
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE); err == nil {
		if _, _, e := k.GetStringValue("40HXGen2"); e == nil {
			rem = append(rem, "- Run 键 40HXGen2")
			fmt.Println("  [!] Run 键仍有残留")
		}
		k.Close()
	}
	if _, err := runOut("schtasks.exe", "/query", "/tn", "40HXGen2"); err == nil {
		rem = append(rem, "- 计划任务 40HXGen2")
		fmt.Println("  [!] 计划任务 40HXGen2 仍有残留")
	} else {
		fmt.Println("  计划任务: 已清理")
	}
	esp := mountESP()
	if esp != "" {
		if _, err := os.Stat(esp + ":\\EFI\\40HX\\40HXUNLK.EFI"); err == nil {
			rem = append(rem, "- ESP 解锁 EFI 文件")
			fmt.Println("  [!] ESP 解锁 EFI 仍有残留")
		} else {
			fmt.Println("  ESP 解锁 EFI: 已清理")
		}
		// v2.4: bootx64 回退副本残留检查
		if _, err := os.Stat(esp + ":\\EFI\\Boot\\bootx64.efi.40hx.bak"); err == nil {
			rem = append(rem, "- bootx64.efi.40hx.bak 备份未还原")
			fmt.Println("  [!] bootx64.efi.40hx.bak 备份仍存在")
		}
		runOut("mountvol.exe", esp+":", "/D")
	}
	// ProgramData 残留检查
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	pdDir := base + "\\40HXUnlock"
	if _, err := os.Stat(pdDir + "\\gen2_status.txt"); err == nil {
		rem = append(rem, "- ProgramData\\40HXUnlock\\gen2_status.txt (诊断缓存)")
		fmt.Println("  [!] gen2_status.txt 仍有残留")
	}
	if _, err := os.Stat(pdDir + "\\drivers"); err == nil {
		rem = append(rem, "- ProgramData\\40HXUnlock\\drivers (驱动备份)")
		fmt.Println("  [!] drivers 备份仍有残留")
	}
	fmt.Println("  Gen2/算力状态将在重启后回到出厂(需重新安装才解锁)。")
	return strings.Join(rem, "\n")
}
