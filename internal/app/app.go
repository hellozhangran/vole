// Package app 只做装配：建依赖、按 tab 顺序注册模块、启动外壳。不放状态。
package app

import (
	"context"
	"fmt"
	"os"
	"time"

	core "vole/internal/core/analyze"
	"vole/internal/core/analyze/mole"
	"vole/internal/core/binloc"
	"vole/internal/core/procs"
	corestatus "vole/internal/core/status"
	statusmole "vole/internal/core/status/mole"
	"vole/internal/core/sysenv"
	"vole/internal/features/analyze"
	"vole/internal/features/apps"
	"vole/internal/features/clean"
	"vole/internal/features/soon"
	"vole/internal/features/status"
	"vole/internal/kit"
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

	// 新增功能 = 在这里加一行
	modules := []module.Module{
		clean.New(clean.NewBackend()),
		apps.New(apps.NewBackend()),
		soon.New("optimize", "优化", kit.Teal()),
		analyze.New(src, env),
		status.New(statusSource(), procs.PS{}),
	}
	// 开发 / 实测用：VOLE_START_TAB=status 启动时直接停在状态 tab
	start := "analyze"
	if v := os.Getenv("VOLE_START_TAB"); v != "" {
		start = v
	}
	return shell.Run(shell.Options{Name: "Vole", Version: Version, Modules: modules, DefaultID: start})
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
	return &corestatus.ExitError{ExitCode: -1, Err: fmt.Errorf("%w：%w", corestatus.ErrUnavailable, s.err)}
}

// missing 是找不到 analyze 二进制时的数据源。
type missing struct{ err error }

func (s missing) Scan(context.Context, core.Request) (core.Report, error) {
	return core.Report{}, fmt.Errorf("%w：%w", core.ErrScannerUnavailable, s.err)
}
