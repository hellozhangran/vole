// Package app 只做装配：建依赖、按 tab 顺序注册模块、启动外壳。不放状态。
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	core "vole/internal/core/analyze"
	"vole/internal/core/analyze/mole"
	"vole/internal/core/binloc"
	"vole/internal/core/fsops"
	"vole/internal/core/procs"
	corestatus "vole/internal/core/status"
	statusmole "vole/internal/core/status/mole"
	"vole/internal/core/sysenv"
	"vole/internal/features/analyze"
	"vole/internal/features/apps"
	"vole/internal/features/clean"
	"vole/internal/features/menubar"
	"vole/internal/features/status"
	"vole/internal/i18n"
	"vole/internal/module"
	"vole/internal/shell"
)

// Version 是 Vole 自己的版本号（显示在右上角）。
const Version = "0.1.0"

// Run 启动 Vole，阻塞到退出。
func Run() error {
	var src core.Source
	if bin, err := binloc.Locate(binloc.AnalyzeName, binloc.AnalyzeEnv); err == nil {
		src = &mole.Source{Binary: bin}
	} else {
		src = missing{err} // 找不到 analyze 时每次扫描都报这条错，界面上显示出来
	}
	env := sysenv.Probe()
	// 状态 tab 和菜单栏面板共用一个 status 采集进程：谁先打开谁启动，都关了才停
	stat := corestatus.NewShared(statusSource())

	// 新增功能 = 在这里加一行
	modules := []module.Module{
		clean.New(clean.NewBackend()),
		apps.New(apps.NewBackend()),
		// 「优化」发版前不做，先隐藏（作者 2026-11-10 决定）
		analyze.New(src, env),
		status.New(stat, procs.PS{}),
	}
	// 开发 / 实测用：VOLE_START_TAB=status 启动时直接停在状态 tab
	start := "analyze"
	if v := os.Getenv("VOLE_START_TAB"); v != "" {
		start = v
	}
	logDir := filepath.Join(env.Home, "Library", "Logs", "Vole") // 清理和卸载共用的操作日志目录
	bar := &shell.MenuBar{Icon: menubar.TrayIcon, Width: menubar.Width,
		Panel: func(ctl shell.Controller) shell.Panel {
			return menubar.New(menubar.Options{Source: stat, Host: ctl, Login: loginItem(),
				Freed: func() fsops.Freed { return fsops.ReadFreed(logDir) }, Ports: sysenv.HardwarePorts})
		}}
	return shell.Run(shell.Options{Name: "Vole", Version: Version, Modules: modules, DefaultID: start, MenuBar: bar})
}

// loginItem 是「开机时启动」开关：只有从 .app 里运行时系统才让注册（开发时 go run 出来的不是 .app）。
func loginItem() menubar.Login {
	exe, _ := os.Executable()
	if !strings.Contains(exe, ".app/Contents/MacOS/") {
		return menubar.Login{Disabled: i18n.T("开发版不是 .app，系统不让注册登录项；打包后可用")}
	}
	return menubar.Login{Enabled: mygo.App.OpenAtLogin, Set: mygo.App.SetOpenAtLogin}
}

// statusSource 定位打包的 status 二进制；找不到时每次 Watch 都报「状态组件不可用」，界面上显示出来。
func statusSource() corestatus.Source {
	bin, err := binloc.Locate(binloc.StatusName, binloc.StatusEnv)
	if err != nil {
		return missingStatus{err}
	}
	return &statusmole.Source{Binary: bin}
}

// missingStatus 是找不到 status 二进制时的数据源。
type missingStatus struct{ err error }

func (s missingStatus) Watch(context.Context, time.Duration, func(corestatus.Snapshot)) error {
	return &corestatus.ExitError{ExitCode: -1, Err: fmt.Errorf(i18n.T("%w：%w"), corestatus.ErrUnavailable, s.err)}
}

// missing 是找不到 analyze 二进制时的数据源。
type missing struct{ err error }

func (s missing) Scan(context.Context, core.Request) (core.Report, error) {
	return core.Report{}, fmt.Errorf(i18n.T("%w：%w"), core.ErrScannerUnavailable, s.err)
}
