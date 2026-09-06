package hxcore

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Gen2StatusPath: %ProgramData%\40HXUnlock\gen2_status.txt
// 选 ProgramData(而非 LOCALAPPDATA) 是因为 Gen2 由 SYSTEM 计划任务执行:
// SYSTEM 的 LOCALAPPDATA 指向 SystemProfile, 用户态读不到; ProgramData 是
// 系统级公共目录, SYSTEM 可写、用户可读, 适合做"任务 → 用户"的状态通道。
func Gen2StatusPath() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "40HXUnlock", "gen2_status.txt")
}

// WriteGen2Status: Gen2 执行结果写入状态文件(供 40HXCheck 展示)。
// 权限不足时静默失败(不影响解锁主流程)。
func WriteGen2Status(text string) error {
	p := Gen2StatusPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	head := "==== 40HX Gen2 任务状态 " + time.Now().Format("2006-01-02 15:04:05") + " ====\n"
	return os.WriteFile(p, []byte(head+text+"\n"), 0o644)
}

// ReadGen2Status: 读状态文件; 不存在/读不到返回 ""
func ReadGen2Status() string {
	b, err := os.ReadFile(Gen2StatusPath())
	if err != nil {
		return ""
	}
	return string(b)
}

// ServiceInfo: 查询内核驱动服务。
// 返回 (是否存在, 启动类型 DEMAND/AUTO/DISABLED, 状态 RUNNING/STOPPED/…)
func ServiceInfo(name string) (bool, string, string) {
	out, err := RunOut("sc.exe", "query", name)
	if err != nil || !strings.Contains(out, "STATE") {
		return false, "", ""
	}
	state := "UNKNOWN"
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "STATE") {
			t := strings.ToUpper(strings.TrimSpace(ln))
			switch {
			case strings.Contains(t, "RUNNING"):
				state = "RUNNING"
			case strings.Contains(t, "STOPPED"):
				state = "STOPPED"
			case strings.Contains(t, "START_PENDING"):
				state = "START_PENDING"
			}
		}
	}
	// 启动类型需 sc qc
	stype := "UNKNOWN"
	if qc, err := RunOut("sc.exe", "qc", name); err == nil {
		t := strings.ToUpper(qc)
		switch {
		case strings.Contains(t, "DEMAND_START"):
			stype = "DEMAND"
		case strings.Contains(t, "AUTO_START"):
			stype = "AUTO"
		case strings.Contains(t, "DISABLED"):
			stype = "DISABLED"
		case strings.Contains(t, "BOOT_START"):
			stype = "BOOT"
		case strings.Contains(t, "SYSTEM_START"):
			stype = "SYSTEM"
		}
	}
	return true, stype, state
}

// TaskInfo: 查询计划任务。
// 返回 (是否存在, 状态 Ready/Running/Disabled, 上次运行结果 "0"/"0x1"…)
func TaskInfo(name string) (bool, string, string) {
	out, err := RunOut("schtasks.exe", "/query", "/tn", name, "/fo", "LIST", "/v")
	if err != nil {
		return false, "", ""
	}
	status, lastResult := "", ""
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(strings.ToLower(t), "status:") {
			status = strings.TrimSpace(t[len("status:"):])
		}
		if strings.HasPrefix(strings.ToLower(t), "last result:") {
			lastResult = strings.TrimSpace(t[len("last result:"):])
		}
	}
	if status == "" && lastResult == "" && !strings.Contains(out, "TaskName") {
		return false, "", ""
	}
	return true, status, lastResult
}
