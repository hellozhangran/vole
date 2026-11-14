// Package binloc 定位随 Vole 打包的外部二进制。
// 顺序：环境变量覆盖（开发期）→ 可执行文件旁的 ../Resources/<name>（.app 内）。
// 不查 PATH：用户不需要自己装 mole（analyze、status 随 app 打包）。
package binloc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"vole/internal/i18n"
)

// 打包二进制的文件名与开发期覆盖用的环境变量。
const (
	AnalyzeName = "analyze"
	AnalyzeEnv  = "VOLE_ANALYZE_BIN"
	StatusName  = "status"
	StatusEnv   = "VOLE_STATUS_BIN"
)

// NotFoundError 列出已经找过的位置，方便用户/开发者排查。
type NotFoundError struct {
	Name  string
	Tried []string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf(i18n.T("找不到 %s 二进制，已查找：%s"), e.Name, strings.Join(e.Tried, i18n.T("；")))
}

// Locate 返回可执行二进制的绝对路径。
func Locate(name, envVar string) (string, error) {
	exe, _ := os.Executable()
	return locate(name, envVar, os.Getenv(envVar), exe)
}

// locate 是可测试的纯逻辑：env 为环境变量值，exe 为当前可执行文件路径。
func locate(name, envVar, env, exe string) (string, error) {
	var tried []string
	// 环境变量一旦设置就以它为准，不再回落，避免开发期悄悄用错二进制。
	if env != "" {
		if p, err := filepath.Abs(env); err == nil && isExecutable(p) {
			return p, nil
		}
		return "", &NotFoundError{Name: name, Tried: []string{envVar + "=" + env}}
	}
	if exe != "" {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		p := filepath.Join(filepath.Dir(exe), "..", "Resources", name)
		p = filepath.Clean(p)
		if isExecutable(p) {
			return p, nil
		}
		tried = append(tried, p)
	}
	tried = append(tried, envVar+i18n.T("（未设置）"))
	return "", &NotFoundError{Name: name, Tried: tried}
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
