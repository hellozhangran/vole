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
	corestore "vole/internal/core/settings"
	corestatus "vole/internal/core/status"
	statusmole "vole/internal/core/status/mole"
	"vole/internal/core/sysenv"
	"vole/internal/features/analyze"
	"vole/internal/features/apps"
	"vole/internal/features/clean"
	"vole/internal/features/menubar"
	"vole/internal/features/settings"
	"vole/internal/features/status"
	"vole/internal/i18n"
	"vole/internal/module"
	"vole/internal/shell"
)

// Version 是 Vole 自己的版本号（显示在右上角）。
const Version = "0.2.1"

// Run 启动 Vole，阻塞到退出。
func Run() error {
	var src core.Source
	if bin, err := binloc.Locate(binloc.AnalyzeName, binloc.AnalyzeEnv); err == nil {
		src = &mole.Source{Binary: bin}
	} else {
		src = missing{err} // 找不到 analyze 时每次扫描都报这条错，界面上显示出来
	}
	env := sysenv.Probe()
	logDir := filepath.Join(env.Home, "Library", "Logs", "Vole") // 清理和卸载共用的操作日志目录
	go killOrphans(logDir)
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
	bar := &shell.MenuBar{Icon: menubar.TrayIcon, Width: menubar.Width,
		Panel: func(ctl shell.Controller) shell.Panel {
			return menubar.New(menubar.Options{Source: stat, Host: ctl, Login: loginItem(),
				Freed: func() fsops.Freed { return fsops.ReadFreed(logDir) }, Ports: sysenv.HardwarePorts})
		}}
	prefs := func(shell.Controller) shell.Settings {
		l := loginItem()
		return settings.New(settings.Options{
			Path:     corestore.DefaultPath(),
			FDA:      func() sysenv.Access { return sysenv.Probe().FullDiskAccess },
			OpenFDA:  func() { _ = mygo.Shell.OpenExternal(fdaSettingsURL) },
			Login:    settings.Login{Enabled: l.Enabled, Set: l.Set, Disabled: l.Disabled},
			Relaunch: mygo.App.Relaunch,
		})
	}
	return shell.Run(shell.Options{Name: "Vole", Version: Version, Modules: modules, DefaultID: start, MenuBar: bar, Settings: prefs})
}

// fdaSettingsURL 打开「系统设置 › 隐私与安全性 › 完全磁盘访问」。
const fdaSettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"

// loginItem 是「开机时启动」开关（菜单栏面板和设置面板共用）：只有从 .app 里运行时系统才让注册（开发时 go run 出来的不是 .app）。
func loginItem() menubar.Login {
	exe, _ := os.Executable()
	if !strings.Contains(exe, ".app/Contents/MacOS/") {
		return menubar.Login{Disabled: i18n.T("开发版不是 .app，系统不让注册登录项；打包后可用")}
	}
	return menubar.Login{Enabled: mygo.App.OpenAtLogin, Set: mygo.App.SetOpenAtLogin}
}

// killOrphans 结束上次 Vole 被 kill -9、崩溃后留下的 analyze / status（规则见 procs.KillOrphans），结束了哪些记进操作日志。
// 在后台跑，不拖慢启动；只认这次 Vole 自己会用的那两个可执行文件。
func killOrphans(logDir string) {
	var paths []string
	for _, b := range []struct{ name, env string }{{binloc.AnalyzeName, binloc.AnalyzeEnv}, {binloc.StatusName, binloc.StatusEnv}} {
		if p, err := binloc.Locate(b.name, b.env); err == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	found, err := procs.KillOrphans(ctx, paths)
	if err != nil || len(found) == 0 {
		return
	}
	log := fsops.NewOpLog(logDir, time.Now())
	for _, o := range found {
		log.Line(time.Now(), "orphan", "已结束", 0, fmt.Sprintf("PID %d", o.PID), o.Path, "上次 Vole 异常退出后留下的子进程")
	}
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
	return core.Report{}, &core.UnavailableError{Cause: s.err}
}
