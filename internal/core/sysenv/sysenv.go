// Package sysenv 探测运行环境，主要用来解释扫描结果里的 partial / unavailable。
package sysenv

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"vole/internal/i18n"
)

// Access 是某项系统授权的状态。
type Access int

const (
	AccessUnknown Access = iota
	AccessGranted
	AccessDenied
)

func (a Access) String() string {
	switch a {
	case AccessGranted:
		return i18n.T("已授权")
	case AccessDenied:
		return i18n.T("未授权")
	}
	return i18n.T("未知")
}

// Info 是一次环境探测的结果。
type Info struct {
	OSVersion      string
	Arch           string
	Home           string
	FullDiskAccess Access
}

// Probe 探测当前进程的环境。不会弹任何系统授权框。
func Probe() Info {
	home, _ := os.UserHomeDir()
	ver, _ := syscall.Sysctl("kern.osproductversion")
	return Info{
		OSVersion:      ver,
		Arch:           runtime.GOARCH,
		Home:           home,
		FullDiskAccess: fullDiskAccess(home),
	}
}

// fullDiskAccess 用用户级 TCC.db 判断：它只有拿到完全磁盘访问的进程才能读。
// 权限被拒 = 未授权；能打开 = 已授权；文件不存在等其他情况 = 未知。
func fullDiskAccess(home string) Access {
	if home == "" {
		return AccessUnknown
	}
	f, err := os.Open(filepath.Join(home, "Library", "Application Support", "com.apple.TCC", "TCC.db"))
	if err == nil {
		f.Close()
		return AccessGranted
	}
	if errors.Is(err, fs.ErrPermission) {
		return AccessDenied
	}
	return AccessUnknown
}
