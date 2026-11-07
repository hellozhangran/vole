package procs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Ancestor 是父进程链上的一环。
type Ancestor struct {
	PID  int
	Name string
	Path string
}

// LaunchJob 是启动这个进程（或它的某个祖先）的 launchd 任务。
type LaunchJob struct {
	Label       string
	Domain      string // 如 "system"、"gui/501"
	PID         int    // 任务的进程（可能是要查的进程本身，也可能是它的祖先）
	Plist       string // plist 路径；运行时提交的任务没有
	SubmittedBy string // 没有 plist 时，launchctl 给出的提交者说明，如 "runningboardd.161"
}

// Explanation 是「为什么在运行」能查到的信息。全部不需要 root；查不到的字段留空，不推测。
type Explanation struct {
	Chain []Ancestor // 从父进程一直上溯到 launchd（PID 1），近的在前
	Job   *LaunchJob // 查不到时为 nil
}

// Explain 查进程的父进程链和所属 launchd 任务。
// 父进程链来自 ps；launchd 任务来自 `launchctl print system` 和 `launchctl print gui/<uid>` 列出的带 PID 的任务，
// 先找进程自己、再沿父进程链往上找第一个是 launchd 任务的祖先；plist 路径来自 `launchctl print <domain>/<label>`。
func Explain(ctx context.Context, p Proc, uid int) (Explanation, error) {
	return explain(ctx, p, uid, runPS, runLaunchctl)
}

func runLaunchctl(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "/bin/launchctl", args...).Output()
}

func explain(ctx context.Context, p Proc, uid int, ps, launchctl runFunc) (Explanation, error) {
	var ex Explanation
	out, err := ps(ctx, "-axo", "pid=,ppid=,comm=")
	if err != nil {
		return ex, fmt.Errorf("读取进程列表失败：%w", err)
	}
	type node struct {
		ppid int
		path string
	}
	all := map[int]node{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		if e1 != nil || e2 != nil {
			continue
		}
		// comm 可能含空格：取前两列之后的整段
		_, rest, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		_, path, _ := strings.Cut(strings.TrimSpace(rest), " ")
		all[pid] = node{ppid: ppid, path: strings.TrimSpace(path)}
	}
	seen := map[int]bool{p.PID: true}
	for cur := p.PPID; cur > 0 && !seen[cur] && len(ex.Chain) < 64; {
		seen[cur] = true
		n, ok := all[cur]
		a := Ancestor{PID: cur, Path: n.path, Name: baseName(n.path, cur)}
		ex.Chain = append(ex.Chain, a)
		if !ok || cur == 1 {
			break
		}
		cur = n.ppid
	}

	// launchd 任务：自己优先，再沿父进程链往上（不含 launchd 本身）
	jobs := map[int]LaunchJob{}
	for _, domain := range []string{fmt.Sprintf("gui/%d", uid), "system"} {
		out, err := launchctl(ctx, "print", domain)
		if err != nil {
			continue // 这个域查不了就跳过，不报错
		}
		for pid, label := range parseServices(out) {
			if _, dup := jobs[pid]; !dup {
				jobs[pid] = LaunchJob{Label: label, Domain: domain, PID: pid}
			}
		}
	}
	candidates := []int{p.PID}
	for _, a := range ex.Chain {
		if a.PID != 1 {
			candidates = append(candidates, a.PID)
		}
	}
	for _, pid := range candidates {
		job, ok := jobs[pid]
		if !ok {
			continue
		}
		if out, err := launchctl(ctx, "print", job.Domain+"/"+job.Label); err == nil {
			job.Plist, job.SubmittedBy = parseJobPath(out)
		}
		ex.Job = &job
		break
	}
	return ex, nil
}

func baseName(path string, pid int) string {
	if path == "" {
		return fmt.Sprintf("PID %d", pid)
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// parseServices 解析 `launchctl print <domain>` 里 services 块中正在运行（PID > 0）的任务："PID  状态  标签"。
func parseServices(out []byte) map[int]string {
	got := map[int]string{}
	in := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "services = {":
			in = true
			continue
		case in && line == "}":
			return got
		case !in:
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil && pid > 0 {
			got[pid] = f[len(f)-1]
		}
	}
	return got
}

// parseJobPath 从 `launchctl print <domain>/<label>` 取第一行 "path = ..."：
// 是 .plist 路径就返回 plist；形如 "(submitted by X)" 就返回提交者 X。
func parseJobPath(out []byte) (plist, submittedBy string) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		v, ok := strings.CutPrefix(line, "path = ")
		if !ok {
			continue
		}
		if strings.HasPrefix(v, "/") && strings.HasSuffix(v, ".plist") {
			return v, ""
		}
		if by, ok := strings.CutPrefix(v, "(submitted by "); ok {
			return "", strings.TrimSuffix(by, ")")
		}
		return "", ""
	}
	return "", ""
}
