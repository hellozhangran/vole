// SPDX-License-Identifier: GPL-3.0-only
// 移植自 tw93/Mole cmd/analyze/oplog.go（commit 6d1af75，GPL-3.0）：escapeLogValue（:76）和逐条追加写日志的做法。
// 改动：日志目录换成 ~/Library/Logs/Vole/（Env.LogDir），一个文件 operations.log，制表符分隔，超过 5MB 轮转一份。

package clean

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// opLog 是清理的操作日志。写失败不影响删除，但会记下第一次失败给调用方提示。
type opLog struct {
	path    string
	session string
	mu      sync.Mutex
	err     error
}

const opLogMax = 5 << 20

func newOpLog(env *Env) *opLog {
	return &opLog{path: filepath.Join(env.LogDir, "operations.log"), session: env.Now().Format("20060102-150405")}
}

// line 写一行：时间  会话  动作  结果  大小KB  规则  路径  原因
func (l *opLog) line(now time.Time, action, status string, sizeKB int64, rule, path, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fields := []string{now.Format(time.RFC3339), l.session, action, status, fmt.Sprint(sizeKB), rule,
		escapeLogValue(path), escapeLogValue(reason)}
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

// escapeLogValue 把控制字符写成 \n、\r、\t 或 \xHH，防止文件名伪造一条日志记录；反斜杠保持原样。
func escapeLogValue(v string) string {
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

// LastClean 从操作日志读出上次真正清理（不含 dry-run）的结束时间和释放字节数；没有记录时 ok 为 false。
func LastClean(env *Env) (at time.Time, freed int64, ok bool) {
	f, err := os.Open(filepath.Join(env.LogDir, "operations.log"))
	if err != nil {
		return time.Time{}, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 5 || fields[2] != "clean" || fields[3] != "结束" {
			continue
		}
		t, err1 := time.Parse(time.RFC3339, fields[0])
		kb, err2 := strconv.ParseInt(fields[4], 10, 64)
		if err1 == nil && err2 == nil {
			at, freed, ok = t, kb*1024, true
		}
	}
	return at, freed, ok
}
