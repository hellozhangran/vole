// SPDX-License-Identifier: GPL-3.0-only

package fsops

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Freed 是操作日志里累计的真实清理和卸载（不含 dry-run）。
type Freed struct {
	Bytes int64 // 累计移走的大小（清理是「结束」行的释放量，卸载是逐项「已移到废纸篓」行的大小之和）
	Times int   // 次数：每次清理、每次卸载（至少移走一项的批次）各算一次
}

// uninstallItems 是卸载逐项日志的原因列（core/apps/uninstall.go 的「app 本体」和 Kind.String()）。
// 卸载每个 app 结束时还会再记一行汇总（原因为空或「N 项关联文件没有移走」），按原因区分开，避免重复计算。
var uninstallItems = map[string]bool{"app 本体": true, "缓存和日志": true, "个人数据": true}

// ReadFreed 读 dir 下的 operations.log（连同轮转出的 operations.log.1）。读不到就是零值。
func ReadFreed(dir string) Freed {
	var f Freed
	for _, name := range []string{"operations.log.1", "operations.log"} {
		readFreed(filepath.Join(dir, name), &f)
	}
	return f
}

func readFreed(path string, f *Freed) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	batches := map[string]bool{} // 已计过次数的卸载批次
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		// 时间  会话  动作  结果  大小KB  规则  路径  原因
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 8 {
			continue
		}
		kb, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || kb < 0 {
			continue
		}
		switch {
		case fields[2] == "clean" && fields[3] == "结束":
			f.Bytes += kb * 1024
			f.Times++
		case fields[2] == "uninstall" && fields[3] == "已移到废纸篓" && uninstallItems[fields[7]]:
			f.Bytes += kb * 1024
			if !batches[fields[1]] {
				batches[fields[1]] = true
				f.Times++
			}
		}
	}
}
