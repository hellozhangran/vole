// SPDX-License-Identifier: GPL-3.0-only
// 移植自 tw93/Mole cmd/analyze/oplog.go（commit 6d1af75，GPL-3.0）：escapeLogValue（:76）和逐条追加写日志的做法。
// 改动：日志目录换成 ~/Library/Logs/Vole/（Env.LogDir），一个文件 operations.log，制表符分隔，超过 5MB 轮转一份。

package clean

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vole/internal/core/fsops"
)

// 日志的写入搬到了 core/fsops（卸载也写同一个文件），这里保留清理用到的名字。

func newOpLog(env *Env) *fsops.OpLog { return fsops.NewOpLog(env.LogDir, env.Now()) }

func escapeLogValue(v string) string { return fsops.EscapeLogValue(v) }

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
