package hxcore

// Defender 驱动排除管理 (v2.5.1)
//
// 背景: WinRing0x64.sys / ThrottleStop.sys 这类底层驱动会被 Windows Defender
// 实时防护当作 HackTool / 漏洞驱动删除或拦载 → 用户装完 Gen2 自启失败。
// 解决: 把【我们自己的】驱动文件/备份目录加进 Defender 排除列表
// (Add-MpPreference -ExclusionPath), 杀软不再误删。这是 ThrottleStop/
// OpenRGB 等硬件工具的通行做法 — 只精确排除, 不关闭任何系统防护,
// 反作弊画像不受影响(排除项 ≠ 关闭 Defender)。
//
// 卸载时用 Remove-MpPreference 清理, 不留系统残留。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Gen2DrvFiles: v2.5 BYOVD 用到的两个驱动文件名。
func Gen2DrvFiles() []string {
	return []string{"ThrottleStop.sys", "WinRing0x64.sys"}
}

// ExclusionPaths: 需要加白的具体路径(精确到文件/目录)。
//   - System32\drivers 下的两个驱动文件(加载窗口期存在)
//   - %ProgramData%\40HXUnlock\drivers 备份源(诊断/Gen2 临时部署从这取)
//   - 安装器 exe 所在目录(防止杀软把整个发布包当 HackTool 删)
func ExclusionPaths() []string {
	var ps []string
	sys := os.Getenv("SystemRoot")
	if sys == "" {
		sys = `C:\Windows`
	}
	for _, f := range Gen2DrvFiles() {
		ps = append(ps, filepath.Join(sys, "System32", "drivers", f))
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	ps = append(ps, filepath.Join(pd, "40HXUnlock", "drivers"))
	if exe, err := os.Executable(); err == nil {
		ps = append(ps, filepath.Dir(exe))
	}
	return ps
}

// psArray: 拼 PowerShell 数组字面量 @('a','b'), 单引号包裹路径。
func psArray(ps []string) string {
	q := make([]string, 0, len(ps))
	for _, p := range ps {
		q = append(q, "'"+strings.ReplaceAll(p, "'", "''")+"'")
	}
	return "@(" + strings.Join(q, ",") + ")"
}

// AddDefenderExclusions: 安装时调用 — 把驱动文件/备份目录加进 Defender 排除。
// 失败只返回错误不 panic; 驱动能正常加载时不影响, 仅作防误删增强。
func AddDefenderExclusions() error {
	ps := ExclusionPaths()
	if len(ps) == 0 {
		return fmt.Errorf("无排除路径")
	}
	_, err := RunOut("powershell.exe", "-NoProfile", "-NonInteractive",
		"-Command", "Add-MpPreference -ExclusionPath "+psArray(ps))
	if err != nil {
		return fmt.Errorf("Add-MpPreference 失败: %v", err)
	}
	return nil
}

// RemoveDefenderExclusions: 卸载时调用 — 清理本工具加过的排除项(精确同名移除)。
func RemoveDefenderExclusions() error {
	ps := ExclusionPaths()
	if len(ps) == 0 {
		return nil
	}
	_, err := RunOut("powershell.exe", "-NoProfile", "-NonInteractive",
		"-Command", "Remove-MpPreference -ExclusionPath "+psArray(ps))
	return err
}
