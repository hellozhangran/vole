// SPDX-License-Identifier: GPL-3.0-only
// 关键路径名单移植自 tw93/Mole lib/core/file_ops.sh _mole_is_critical_deletion_path（:1044，commit 6d1af75，GPL-3.0）。

package fsops

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// CriticalPath 报告绝对路径是否是绝不能删的系统关键路径（含家目录本身）。
func CriticalPath(p, home string) bool {
	if strings.HasPrefix(p, "/usr/local/") || strings.HasPrefix(p, "/opt/homebrew/") {
		return false
	}
	switch p {
	case "/", "/bin", "/dev", "/sbin", "/usr", "/System", "/Library", "/Library/Apple", "/Library/Application Support",
		"/Library/Extensions", "/Library/Keychains", "/Applications", "/Volumes", "/opt", "/opt/homebrew",
		"/Users", "/Users/Shared", "/Users/Guest", "/private", "/private/tmp", "/etc", "/private/etc",
		"/var", "/var/db", "/var/audit", "/var/root", "/private/var", "/private/var/tmp", "/private/var/folders",
		"/private/var/db", "/private/var/audit", "/private/var/root", "/Network", "/cores", "/home", "/net", "/tmp":
		return true
	}
	for _, root := range []string{"/bin/", "/dev/", "/sbin/", "/usr/", "/System/", "/Library/Apple/", "/Library/Extensions/",
		"/Library/Keychains/", "/Applications/Finder.app", "/Applications/Safari.app", "/Users/Guest/", "/etc/",
		"/private/etc/", "/var/db/", "/var/audit/", "/private/var/db/", "/private/var/audit/"} {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	// /Users/<名字> 本身（任何用户的家目录）
	if strings.HasPrefix(p, "/Users/") && !strings.Contains(strings.TrimPrefix(p, "/Users/"), "/") {
		return true
	}
	return home != "" && p == home
}

// CheckPath 是删除前的通用路径校验（清理、卸载共用；语义参考 Mole validate_path_for_deletion，比它更严）：
//   - 绝对路径、已规范化（不含 .. 和多余的 /）、不含控制字符；
//   - 必须在 under 目录下（通常是家目录），且不是关键系统路径；
//   - 每一级上级目录都不能是 symlink：用真实路径比对，防止目录被链到别处后删到别的地方。
func CheckPath(p, under string) error {
	if p == "" || !filepath.IsAbs(p) {
		return fmt.Errorf("不是绝对路径")
	}
	if p != filepath.Clean(p) {
		return fmt.Errorf("路径不规范")
	}
	if strings.ContainsFunc(p, unicode.IsControl) {
		return fmt.Errorf("路径含控制字符")
	}
	if !strings.HasPrefix(p, under+"/") {
		return fmt.Errorf("不在家目录下")
	}
	if CriticalPath(p, under) {
		return fmt.Errorf("系统关键路径")
	}
	parent := filepath.Dir(p)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("无法确认上级目录：%v", err)
	}
	if real != parent {
		return fmt.Errorf("上级目录经过符号链接（实际指向 %s）", real)
	}
	return nil
}
