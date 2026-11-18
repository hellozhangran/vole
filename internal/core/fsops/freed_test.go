// SPDX-License-Identifier: GPL-3.0-only

package fsops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFreed(t *testing.T) {
	dir := t.TempDir()
	if f := ReadFreed(dir); f != (Freed{}) {
		t.Fatalf("没有日志应为零值：%+v", f)
	}
	// 轮转出的旧文件：一次清理
	old := "2026-11-01T10:00:00+08:00\t20261101-100000\tclean\t结束\t1000\t\t\t释放 1000 KB\n"
	cur := "" +
		// dry-run 不算
		"2026-11-09T10:00:00+08:00\t20261109-100000\tdry-run\t结束\t0\t\t\t\n" +
		"2026-11-09T10:00:00+08:00\t20261109-100000\tdry-run\t将清理\t500\tnpm\t/x\t\n" +
		// 清理：逐项不算，只算「结束」行
		"2026-11-09T12:00:00+08:00\t20261109-120000\tclean\t已清理\t300\tnpm\t/a\t\n" +
		"2026-11-09T12:00:01+08:00\t20261109-120000\tclean\t结束\t300\t\t\t释放 300 KB\n" +
		// 卸载两个 app：逐项算，每个 app 的汇总行不算；同一批次只算一次
		"2026-11-10T06:00:41+08:00\t20261110-060041\tuninstall\t开始\t0\t\t\t2 个 app\n" +
		"2026-11-10T06:00:42+08:00\t20261110-060041\tuninstall\t已移到废纸篓\t50\tcom.a\t/Applications/A.app\tapp 本体\n" +
		"2026-11-10T06:00:42+08:00\t20261110-060041\tuninstall\t已移到废纸篓\t5\tcom.a\t/c\t缓存和日志\n" +
		"2026-11-10T06:00:42+08:00\t20261110-060041\tuninstall\t已移到废纸篓\t55\tcom.a\t/Applications/A.app\t\n" +
		"2026-11-10T06:00:43+08:00\t20261110-060041\tuninstall\t已移到废纸篓\t20\tcom.b\t/Applications/B.app\tapp 本体\n" +
		"2026-11-10T06:00:43+08:00\t20261110-060041\tuninstall\t失败\t7\tcom.b\t/d\tPermission denied\n" +
		"2026-11-10T06:00:43+08:00\t20261110-060041\tuninstall\t已移到废纸篓\t20\tcom.b\t/Applications/B.app\t1 项关联文件没有移走\n" +
		"2026-11-10T06:00:43+08:00\t20261110-060041\tuninstall\t结束\t0\t\t\t\n" +
		// v0.2.1 起的格式：汇总行状态是「完成」，不算；逐项照算
		"2026-11-18T09:00:00+08:00\t20261118-090000\tuninstall\t已移到废纸篓\t30\tcom.c\t/Applications/C.app\tapp 本体\n" +
		"2026-11-18T09:00:00+08:00\t20261118-090000\tuninstall\t完成\t30\tcom.c\t/Applications/C.app\t汇总：共 1 项\n" +
		// 卸载的 dry-run 不算
		"2026-11-10T07:00:00+08:00\t20261110-070000\tuninstall-dry-run\t将卸载\t99\tcom.c\t/Applications/C.app\t\n" +
		"坏行\n"
	if err := os.WriteFile(filepath.Join(dir, "operations.log.1"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "operations.log"), []byte(cur), 0o600); err != nil {
		t.Fatal(err)
	}
	want := Freed{Bytes: (1000 + 300 + 50 + 5 + 20 + 30) * 1024, Times: 4}
	if got := ReadFreed(dir); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
