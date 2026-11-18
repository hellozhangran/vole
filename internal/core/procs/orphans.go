package procs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Orphan 是上次 Vole 异常退出后留下的子进程。
type Orphan struct {
	PID, PGID int
	Path      string
}

// KillOrphans 结束上次 Vole 被 kill -9、崩溃后留下的 analyze / status 子进程，启动时调一次。
//
// 只结束同时满足下面三条的进程，不会碰用户自己跑的 mole 或别的程序：
//   - 可执行文件就是 paths 里的某一个（这次 Vole 用的 analyze、status 的绝对路径：.app 里的，或开发期 VOLE_*_BIN 指定的）；
//   - 父进程已经是 launchd（PPID 为 1）：原来的 Vole 已经不在了，正在运行的 Vole 起的子进程父进程是它自己；
//   - 属于当前用户。
//
// 这些子进程是 Vole 用 Setpgid 起的进程组组长，连同组里的 du 等一起结束（SIGKILL，和取消扫描时一样）。
// status 其实不会残留（Vole 一死它的 stdout 管道就断了，下一次写入时被 SIGPIPE 结束）；analyze 只在扫完时才写输出，会一直扫到结束，所以要清。
func KillOrphans(ctx context.Context, paths []string) ([]Orphan, error) {
	out, err := runPS(ctx, "-axo", "pid=,ppid=,pgid=,uid=,comm=")
	if err != nil {
		return nil, err
	}
	found := findOrphans(out, paths, os.Getuid(), os.Getpid())
	for _, o := range found {
		target := o.PID
		if o.PGID == o.PID {
			target = -o.PID // 组长：整组一起
		}
		_ = syscall.Kill(target, syscall.SIGKILL)
	}
	return found, nil
}

// findOrphans 从 ps -axo pid=,ppid=,pgid=,uid=,comm= 的输出里挑出要结束的进程。
func findOrphans(out []byte, paths []string, uid, self int) []Orphan {
	want := map[string]bool{}
	for _, p := range paths {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		want[filepath.Clean(p)] = true
		if real, err := filepath.EvalSymlinks(p); err == nil {
			want[real] = true
		}
	}
	var found []Orphan
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		var n [4]int
		ok := true
		for i := range n {
			v, err := strconv.Atoi(f[i])
			if err != nil {
				ok = false
				break
			}
			n[i] = v
		}
		// 路径里可能有空格：前四列之后的都是路径
		path := strings.TrimSpace(line)
		for range 4 {
			path = strings.TrimSpace(path[strings.IndexAny(path, " \t"):])
		}
		pid, ppid, pgid, owner := n[0], n[1], n[2], n[3]
		if !ok || ppid != 1 || owner != uid || pid == self || !want[filepath.Clean(path)] {
			continue
		}
		found = append(found, Orphan{PID: pid, PGID: pgid, Path: path})
	}
	return found
}
