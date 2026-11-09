// SPDX-License-Identifier: GPL-3.0-only
// 移植自 tw93/Mole cmd/analyze/oplog.go（commit 6d1af75，GPL-3.0）：escapeLogValue（:76）和逐条追加写日志的做法。
// 改动：日志目录换成 ~/Library/Logs/Vole/（Env.LogDir），一个文件 operations.log，制表符分隔，超过 5MB 轮转一份。

package fsops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OpLog 是操作日志（清理、卸载共用 ~/Library/Logs/Vole/operations.log）。写失败不影响删除，但会记下第一次失败给调用方提示。
type OpLog struct {
	path    string
	session string
	mu      sync.Mutex
	err     error
}

const opLogMax = 5 << 20

// NewOpLog 开一个批次：dir 是日志目录，批次 ID 取 now（如 20261109-151342）。
func NewOpLog(dir string, now time.Time) *OpLog {
	return &OpLog{path: filepath.Join(dir, "operations.log"), session: now.Format("20060102-150405")}
}

// Err 是第一次写日志失败的错误。
func (l *OpLog) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Line 写一行：时间  会话  动作  结果  大小KB  规则  路径  原因
func (l *OpLog) Line(now time.Time, action, status string, sizeKB int64, rule, path, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fields := []string{now.Format(time.RFC3339), l.session, action, status, fmt.Sprint(sizeKB), rule,
		EscapeLogValue(path), EscapeLogValue(reason)}
	if err := appendRotating(l.path, strings.Join(fields, "\t")+"\n"); err != nil && l.err == nil {
		l.err = err
	}
}

func appendRotating(p, text string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(p); err == nil && info.Size()+int64(len(text)) > opLogMax {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

// EscapeLogValue 把控制字符写成 \n、\r、\t 或 \xHH，防止文件名伪造一条日志记录；反斜杠保持原样。
func EscapeLogValue(v string) string {
	var b strings.Builder
	for _, c := range []byte(v) {
		switch {
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02X`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
