// Package procs 用系统自带的 /bin/ps 采集进程列表，并提供「为什么在运行」和结束进程。
// 纯 Go，不依赖 UI；不引用任何第三方实现。
package procs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"vole/internal/i18n"
)

// Proc 是一个进程。
type Proc struct {
	PID, PPID int
	UID       int
	User      string    // 用户名（拿不到时为空）
	Name      string    // 可执行文件名
	Path      string    // 可执行文件完整路径（拿不到时为空）
	CPU       float64   // 最近一个采样间隔的 CPU 占用 %，单核满载 = 100，可超过 100
	MemBytes  uint64    // 常驻内存（RSS）
	Started   time.Time // 启动时间（秒级），和 PID 一起唯一标识一个进程，防 PID 复用
}

// Options 控制采集。
type Options struct {
	Top   int     // 取 CPU 最高的前几个
	Extra *PIDSet // 不在前 Top 里也要带上的进程（如用户固定的行）；可为 nil
}

// Source 是进程列表数据源。
type Source interface {
	// Watch 每 interval 采一次并调用 emit（在采集 goroutine 上），按 CPU 降序；阻塞到 ctx 取消或出错。
	Watch(ctx context.Context, interval time.Duration, opts Options, emit func([]Proc)) error
}

// PIDSet 是可在多个 goroutine 间共享的 PID 集合。
type PIDSet struct {
	mu   sync.Mutex
	pids map[int]bool
}

// Set 设置 pid 是否在集合里。
func (s *PIDSet) Set(pid int, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pids == nil {
		s.pids = map[int]bool{}
	}
	if on {
		s.pids[pid] = true
	} else {
		delete(s.pids, pid)
	}
}

// Has 报告 pid 是否在集合里。
func (s *PIDSet) Has(pid int) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pids[pid]
}

// runFunc 执行 /bin/ps 并返回 stdout；测试里替换成读 fixture。
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

func runPS(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "/bin/ps", args...).Output()
	// ps -p 查不到任何进程时退出码为 1 且输出为空：当作空结果，不算错误
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
	}
	return out, err
}

// PS 用 /bin/ps 采集。开销实测：一次全量扫描约 0.026s CPU；路径和用户名只对新进入列表的进程查一次并缓存。
type PS struct{}

var _ Source = PS{}

// Watch 见 Source。启动时隔 500ms 先采两次，第一帧就有 CPU 数据。
func (PS) Watch(ctx context.Context, interval time.Duration, opts Options, emit func([]Proc)) error {
	s := newSampler(runPS)
	if _, err := s.sample(ctx, time.Now(), opts); err != nil {
		return err
	}
	delay := 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		list, err := s.sample(ctx, time.Now(), opts)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		emit(list)
		delay = interval
	}
}

// scanRow 是全量扫描的一行。
type scanRow struct {
	pid, ppid, uid int
	rss            uint64 // KB
	cpu            time.Duration
	started        time.Time
}

type key struct {
	pid     int
	started time.Time
}

type detail struct{ user, path string }

// sampler 记住上一次各进程的累计 CPU 时间，用差值算瞬时占用；缓存路径和用户名。
type sampler struct {
	run     runFunc
	prev    map[key]time.Duration
	prevAt  time.Time
	details map[key]detail
}

func newSampler(run runFunc) *sampler {
	return &sampler{run: run, prev: map[key]time.Duration{}, details: map[key]detail{}}
}

// scanFields 是全量扫描的列：都是便宜字段（不含路径，路径要逐个读进程参数，贵）。
const scanFields = "pid=,ppid=,uid=,rss=,time=,lstart="

func (s *sampler) sample(ctx context.Context, now time.Time, opts Options) ([]Proc, error) {
	out, err := s.run(ctx, "-axo", scanFields)
	if err != nil {
		return nil, fmt.Errorf(i18n.T("读取进程列表失败：%w"), err)
	}
	rows := parseScan(out)
	elapsed := now.Sub(s.prevAt)

	type cand struct {
		row scanRow
		cpu float64
	}
	cands := make([]cand, 0, len(rows))
	next := make(map[key]time.Duration, len(rows))
	for _, r := range rows {
		k := key{r.pid, r.started}
		next[k] = r.cpu
		c := cand{row: r}
		if before, ok := s.prev[k]; ok && elapsed > 0 && r.cpu >= before {
			c.cpu = float64(r.cpu-before) / float64(elapsed) * 100
		}
		cands = append(cands, c)
	}
	s.prev, s.prevAt = next, now

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].cpu != cands[j].cpu {
			return cands[i].cpu > cands[j].cpu
		}
		return cands[i].row.rss > cands[j].row.rss
	})
	var picked []cand
	for i, c := range cands {
		if i < opts.Top || opts.Extra.Has(c.row.pid) {
			picked = append(picked, c)
		}
	}

	// 只给新进入列表的进程查路径和用户名
	var missing []string
	for _, c := range picked {
		if _, ok := s.details[key{c.row.pid, c.row.started}]; !ok {
			missing = append(missing, strconv.Itoa(c.row.pid))
		}
	}
	if len(missing) > 0 {
		out, err := s.run(ctx, "-o", "pid=,user=,comm=", "-p", strings.Join(missing, ","))
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		got := parseDetails(out) // 失败或部分进程已退出：缺的就留空
		for _, c := range picked {
			k := key{c.row.pid, c.row.started}
			if _, ok := s.details[k]; !ok {
				s.details[k] = got[c.row.pid]
			}
		}
	}
	for k := range s.details {
		if _, alive := next[k]; !alive {
			delete(s.details, k)
		}
	}

	list := make([]Proc, 0, len(picked))
	for _, c := range picked {
		d := s.details[key{c.row.pid, c.row.started}]
		name := filepath.Base(d.path)
		if d.path == "" {
			name = fmt.Sprintf("PID %d", c.row.pid)
		}
		list = append(list, Proc{
			PID: c.row.pid, PPID: c.row.ppid, UID: c.row.uid, User: d.user, Name: name, Path: d.path,
			CPU: c.cpu, MemBytes: c.row.rss * 1024, Started: c.row.started,
		})
	}
	return list, nil
}

// lstartLayout 是 ps lstart 的格式，如 "Thu Oct  8 17:01:30 2026"（本地时区）。
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// parseScan 解析 "pid ppid uid rss time lstart(5 段)"，坏行跳过。
func parseScan(out []byte) []scanRow {
	var rows []scanRow
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 10 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		uid, e3 := strconv.Atoi(f[2])
		rss, e4 := strconv.ParseUint(f[3], 10, 64)
		cpu, e5 := parseCPUTime(f[4])
		started, e6 := time.ParseInLocation(lstartLayout, strings.Join(f[5:10], " "), time.Local)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil {
			continue
		}
		rows = append(rows, scanRow{pid: pid, ppid: ppid, uid: uid, rss: rss, cpu: cpu, started: started})
	}
	return rows
}

// parseDetails 解析 "pid user comm"；comm 是完整路径，可能含空格，取剩余整段。
func parseDetails(out []byte) map[int]detail {
	got := map[int]detail{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		pidStr, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		user, path, _ := strings.Cut(strings.TrimSpace(rest), " ")
		got[pid] = detail{user: user, path: strings.TrimSpace(path)}
	}
	return got
}

// parseCPUTime 解析 ps 的累计 CPU 时间："M:SS.ss"（分钟可超过 60），也兼容 "H:MM:SS.ss" 和 "D-H:MM:SS"。
func parseCPUTime(s string) (time.Duration, error) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, err
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf(i18n.T("无法解析 CPU 时间 %q"), s)
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, err
	}
	total := sec
	mult := 60.0
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, err
		}
		total += float64(n) * mult
		mult *= 60
	}
	total += float64(days) * 86400
	return time.Duration(total * float64(time.Second)), nil
}

// AppBundle 返回可执行文件所在的最外层 .app 路径（不在 .app 里时为空）。
// 例：/Applications/X.app/Contents/Frameworks/X Helper.app/Contents/MacOS/X Helper → /Applications/X.app
func AppBundle(path string) string {
	i := strings.Index(path, ".app/")
	if i < 0 {
		return ""
	}
	return path[:i+len(".app")]
}

// AppName 是 .app 的显示名（文件名去掉 .app）；bundle 为空时返回空。
func AppName(bundle string) string {
	if bundle == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(bundle), ".app")
}
