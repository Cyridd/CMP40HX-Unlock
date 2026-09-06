// 40HXCheck — CMP 40HX 解锁独立诊断工具 v2.5
//
// 双击即诊, 只读为主; v2.5 起若发现"算力/Gen2 无法实测(驱动未运行)"且驱动文件
// 在包内, 会临时拉起 ThrottleStop + WinRing0 实测后自清理(用完即卸, 保持无痕):
//
//	① 最优先显示: 算力解锁状态 + PCIe Gen2 状态
//	② 其次: GPU/Secure Boot/GSP/测试签名
//	③ 明细与建议
//
// 安装器(40HXInstaller)负责"装", 本工具负责"查"。
//
// 实现共享 tools/40hxcore (与安装器同一份探测/诊断代码, 不会漂移)。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	hxcore "40hxcore"
	"golang.org/x/sys/windows"
)

const (
	appTitle     = "CMP 40HX 解锁诊断"
	logsDirName  = "40HXUnlock"              // %LOCALAPPDATA%\40HXUnlock\logs
	gen2TaskName = "40HX PCIe Gen2 Bring-up" // 与安装器 setupGen2Task 同名
)

var (
	procMsgBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

// ---- v2.5: 临时驱动管理 (ThrottleStop + WinRing0, 用完即卸) ----
const (
	fileTS = "ThrottleStop.sys"
	fileWR = "WinRing0x64.sys"
	svcTS  = "ThrottleStop"
	svcWR  = "WinRing0_1_2_0"
)

func sysDrvDir() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers")
}

// driverSrcDir: 在包结构中定位 drivers/ (v2.5: exe 旁 gen2/drivers / ProgramData 备份)。
func driverSrcDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	pd := filepath.Join(os.Getenv("ProgramData"), "40HXUnlock", "drivers") // 安装器留下的备份源
	for _, c := range []string{
		pd,
		filepath.Join(dir, "gen2", "drivers"),
		filepath.Join(dir, "drivers"),
	} {
		if _, e1 := os.Stat(filepath.Join(c, fileTS)); e1 == nil {
			if _, e2 := os.Stat(filepath.Join(c, fileWR)); e2 == nil {
				return c
			}
		}
	}
	return ""
}

func svcState(name string) string {
	_, _, st := hxcore.ServiceInfo(name)
	return st
}

// ensureDrivers: 确保 TS/WinRing0 服务 RUNNING。已运行→不管(外部管理);
// 否则从包 drivers 部署+启动。返回 (selfManaged 是否由本工具拉起, ok 是否就绪)。
func ensureDrivers() (bool, bool) {
	src := driverSrcDir()
	allRunning := svcState(svcTS) == "RUNNING" && svcState(svcWR) == "RUNNING"
	if allRunning {
		return false, true
	}
	if src == "" {
		return false, false
	}
	self := false
	for _, d := range []struct{ svc, file string }{
		{svcTS, fileTS}, {svcWR, fileWR},
	} {
		dst := filepath.Join(sysDrvDir(), d.file)
		if _, err := os.Stat(dst); err != nil {
			if b, e := os.ReadFile(filepath.Join(src, d.file)); e == nil {
				os.WriteFile(dst, b, 0o644)
			}
		}
		if svcState(d.svc) != "RUNNING" {
			hxcore.RunOut("sc.exe", "create", d.svc, "type=", "kernel",
				"start=", "demand", "binPath=", `\SystemRoot\System32\drivers\`+d.file)
			hxcore.RunOut("sc.exe", "start", d.svc)
			self = true
		}
	}
	time.Sleep(400 * time.Millisecond)
	ok := svcState(svcTS) == "RUNNING" && svcState(svcWR) == "RUNNING"
	return self && ok, ok
}

// cleanupDrivers: 自清理 — 停服务、删服务、删驱动文件。
func cleanupDrivers() {
	for _, d := range []struct{ svc, file string }{
		{svcTS, fileTS}, {svcWR, fileWR},
	} {
		hxcore.RunOut("sc.exe", "stop", d.svc)
		hxcore.RunOut("sc.exe", "delete", d.svc)
		os.Remove(filepath.Join(sysDrvDir(), d.file))
	}
}

func msgbox(text string, icon uint) {
	t, _ := syscall.UTF16PtrFromString(appTitle)
	b, _ := syscall.UTF16PtrFromString(text)
	procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(icon))
}

// isAdmin: 与安装器同款实现 (TokenElevation 在受限环境可能误报 0, 再试 SCM 全权)
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
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		windows.CloseServiceHandle(scm)
		return true
	}
	return false
}

// selfElevate: 非管理员时 ShellExecute runas 提权重启(诊断要挂 ESP 读 40hx_log)
func selfElevate() {
	exe, _ := os.Executable()
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-elevated")
	params, _ := syscall.UTF16PtrFromString(strings.Join(args, " "))
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	r, _, _ := proc.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), 0, 1)
	if r <= 32 {
		msgbox("需要管理员权限才能读取 EFI 解锁日志(40hx_log.txt)。\n请右键本程序 -> 以管理员身份运行。", 0x30)
	}
	os.Exit(0)
}

// logsDir: %LOCALAPPDATA%\40HXUnlock\logs (统一日志收集目录, 用户好找)
func logsDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, logsDirName, "logs")
	os.MkdirAll(d, 0o755)
	return d
}

// collectLogs: 把相关日志汇集到固定目录, 返回目录路径
func collectLogs(diagSnapshot string) string {
	dir := logsDir()
	// 1. 安装器/Gen2 日志 (%TEMP%\40HX_installer.log)
	if b, err := os.ReadFile(filepath.Join(os.TempDir(), "40HX_installer.log")); err == nil {
		os.WriteFile(filepath.Join(dir, "installer.log"), b, 0o644)
	}
	// 2. EFI 解锁链日志 (ESP 根 40hx_log.txt) — 管理员下可读
	if esp := hxcore.MountESP(); esp != "" {
		if b, err := os.ReadFile(esp + ":\\40hx_log.txt"); err == nil {
			os.WriteFile(filepath.Join(dir, "40hx_log.txt"), b, 0o644)
		}
		hxcore.UnmountESP(esp)
	}
	// 3. 本次诊断快照
	os.WriteFile(filepath.Join(dir, "diagnose.txt"), []byte(diagSnapshot), 0o644)
	return dir
}

// indentLines: 多行文本统一加 4 空格缩进(状态文件内容展示用)
func indentLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = "    " + ln
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard: PowerShell Set-Clipboard(失败静默 — 仅增强, 不阻塞)
func copyToClipboard(s string) bool {
	f, err := os.CreateTemp("", "40hx_clip_*.txt")
	if err != nil {
		return false
	}
	p := f.Name()
	f.WriteString(s)
	f.Close()
	defer os.Remove(p)
	out, err := hxcore.RunOut("powershell.exe", "-NoProfile", "-Command",
		"Get-Content -LiteralPath '"+p+"' -Raw -Encoding UTF8 | Set-Clipboard")
	return err == nil && !strings.Contains(out, "denied")
}

func check() {
	var sb strings.Builder
	w := func(format string, a ...interface{}) { sb.WriteString(fmt.Sprintf(format, a...)) }
	var tips []string
	// v2.5.1: 版本标题写入 sb → 弹窗/diagnose.txt 都可见 (此前 fmt.Println 只进 log)
	w("==============================================\n")
	w("  CMP 40HX 解锁诊断  v2.5   %s\n", time.Now().Format("2006-01-02 15:04:05"))
	w("==============================================\n")
	gpuOK := hxcore.FindGPU()
	sbOn := hxcore.SecureBootOn()
	tsOn := hxcore.TestSigningOn()
	gsOn := hxcore.GspEnabled()
	sub, adapter, fw := hxcore.GspDiag()

	// --- A. 主判定: 算力 + Gen2 最优先 (v2.5) ---
	selfM, drvOK := ensureDrivers()
	st := hxcore.ReadUnlockStateV2(6, 800)
	bar := strings.Repeat("=", 46)
	w("\n%s\n", bar)
	state := "无法实测 (驱动未就绪)"
	switch {
	case st.SS0OK && st.Unlocked && st.Speed >= 2:
		state = "算力满血 + Gen2 达成"
	case st.SS0OK && st.Unlocked:
		state = "算力满血, Gen2 未达成"
	case st.SS0OK:
		state = "未解锁 (SS0 锁定)"
	}
	w("  解锁状态 : %s\n", state)
	comp := "不可读"
	if st.SS0OK {
		comp = fmt.Sprintf("%s (SS0=0x%08X SS1=0x%08X)",
			map[bool]string{true: "✓ 满血", false: "✗ 锁定"}[st.Unlocked], st.SS0, st.SS1)
	}
	spd := "不可读"
	if st.Speed >= 1 {
		names := map[uint32]string{1: "Gen1 (2.5 GT/s)", 2: "Gen2 (5.0 GT/s)", 3: "Gen3 (8.0 GT/s)", 4: "Gen4 (16 GT/s)"}
		spd = names[st.Speed]
		if spd == "" {
			spd = fmt.Sprintf("Gen%d", st.Speed)
		}
	}
	w("  算力     : %s\n", comp)
	w("  PCIe     : %s\n", spd)
	w("%s\n", bar)

	// --- B. 基础状态 ---
	w("GPU 40HX: %s   Secure Boot: %s   GSP(EnableGpuFirmware): %s\n",
		map[bool]string{true: "✓", false: "✗"}[gpuOK],
		map[bool]string{true: "开启(需关闭!)", false: "关闭(OK)"}[sbOn],
		map[bool]string{true: "✓", false: "✗"}[gsOn])
	w("测试签名: %s  (v2.5 不需要, 建议关闭)\n",
		map[bool]string{true: "已开启", false: "关闭"}[tsOn])
	if sub != "" {
		w("GSP 键: Class\\%s (fw=%d)  Adapter: %s\n", sub, fw, adapter)
	} else if adapter != "" {
		w("GSP: %s\n", adapter)
	}

	// --- C. 明细: 实测通道/Gen2 任务/历史记录 (v2.5: 仅 TS + WinRing0) ---
	w("实测通道   : TS(ThrottleStop)=%s  WinRing0=%s",
		map[bool]string{true: "✓", false: "✗"}[st.TSOK],
		map[bool]string{true: "✓", false: "✗"}[st.WinRingOK])
	if selfM {
		w("  (本次诊断临时拉起, 测完即卸)")
	} else if st.TSOK || st.WinRingOK {
		w("  (已在运行)")
	}
	w("\n")
	taskOK, taskStatus, taskResult := hxcore.TaskInfo(gen2TaskName)
	w("Gen2 任务: %s\n",
		map[bool]string{true: "已注册 (" + taskStatus + ", 上次结果: " + taskResult + ")",
			false: "未注册 (跑 manual_install.bat 注册, 登录自动解锁)"}[taskOK])
	// gen2_status.txt 是上次 Gen2 任务写入的"历史快照", 不是本次实测:
	// 只有驱动实测不可用时才作为参考展示, 且明确标注为历史记录,
	// 避免"卸载后还显示 ✅ Gen2"的误导。
	if !st.SS0OK || st.Speed < 2 {
		if gs := hxcore.ReadGen2Status(); gs != "" {
			w("  注: 存在上次 Gen2 任务的历史记录(非本次实测):\n%s\n", indentLines(gs))
			if !drvOK {
				w("       ↑ 驱动当前未运行 — 此为历史残留, 不代表当前状态\n")
			}
		}
	}
	if drvOK && !st.SS0OK {
		w("(驱动已运行但读不到算力寄存器 — 异常)\n")
	}

	// --- D. 结论与建议 ---
	verdict := ""
	switch {
	case st.Unlocked && st.Speed >= 2:
		verdict = ">>> 解锁成功: Tensor 满血 + Gen2 x16"
	case st.Unlocked:
		verdict = ">>> 算力满血; Gen2 未达成 — 管理员跑 gen2\\40hx_gen2_byovd.py --selfclean"
	case st.SS0OK:
		verdict = ">>> 本次开机未解锁 (SS0 锁定)"
	default:
		if gpuOK && gsOn && !drvOK {
			verdict = ">>> 驱动不可用 — 请从 40HXUnlock_v2.5 完整目录(含 gen2\\drivers)运行本工具"
		} else {
			verdict = ">>> 无法完成解锁判定 (见上方分项)"
		}
	}
	w("\n%s\n", verdict)
	if st.SS0OK && !st.Unlocked {
		if reason := hxcore.AnalyzeEfiLog(); reason != "" {
			w("%s\n", reason)
		}
	}

	if !gpuOK {
		tips = append(tips, "· 未检测到 40HX: 确认显卡已插且驱动已装")
	}
	if sbOn {
		tips = append(tips, "· Secure Boot 开启: 进 BIOS 关闭 (否则解锁 EFI 被拒)")
	}
	if tsOn {
		tips = append(tips, "· 测试签名已开启 (v2.5 不需要): bcdedit /set testsigning off 可关闭")
	}
	if !gsOn {
		tips = append(tips, "· GSP 未启用: 跑 manual_install.bat (自动设 EnableGpuFirmware=1)")
	}
	if gpuOK && st.SS0OK && !st.Unlocked {
		tips = append(tips, "· EFI 算力解锁未生效: 确认开机走了 40HX Unlock 启动项")
	}
	if len(tips) > 0 {
		w("\n建议:\n%s\n", strings.Join(tips, "\n"))
	}

	out := sb.String()
	fmt.Println(out)

	// v2.5: 本工具拉起过驱动则用完即卸 (保持系统无第三方驱动)
	if selfM {
		cleanupDrivers()
	}

	dir := collectLogs(out)
	copied := copyToClipboard(out)
	note := ""
	if copied {
		note = "\n\n诊断摘要已复制到剪贴板 — 直接粘贴到 issue 即可。"
	}
	ok := st.Unlocked && st.Speed >= 2
	msgbox(out+"\n\n日志已收集到:\n"+dir+note, map[bool]uint{true: 0x40, false: 0x30}[ok])
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "-elevated" {
		if !isAdmin() {
			selfElevate()
			return
		}
	}
	// 输出镜像到统一日志目录
	dir := logsDir()
	if f, err := os.Create(filepath.Join(dir, "40HXCheck.log")); err == nil {
		os.Stdout = f
		os.Stderr = f
		fmt.Fprintf(f, "==== 40HXCheck %s ====\n", time.Now().Format("2006-01-02 15:04:05"))
	}
	check()
}
