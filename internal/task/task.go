// Package task 在后台跑任务，结果经 post 交回主线程。
// 同一个 key 再次启动会取消旧任务并丢弃其结果，UI 不用手工比对 generation。
package task

import (
	"context"
	"sync"
)

// Runner 管理一组后台任务。post 必须把函数排到主线程异步执行（如 mygo 的 win.Update）。
type Runner struct {
	post   func(func())
	ctx    context.Context
	stop   context.CancelFunc
	mu     sync.Mutex
	jobs   map[string]*job
	closed bool
	wg     sync.WaitGroup
}

type job struct{ cancel context.CancelFunc }

// NewRunner 创建任务管理器。
func NewRunner(post func(func())) *Runner {
	ctx, stop := context.WithCancel(context.Background())
	return &Runner{post: post, ctx: ctx, stop: stop, jobs: map[string]*job{}}
}

// Go 后台执行 work，完成后在主线程调用 done。
// 被同 key 新任务顶替、被 Cancel 或 Runner 已 Close 时，done 不会被调用。
func Go[T any](r *Runner, key string, work func(context.Context) (T, error), done func(T, error)) {
	ctx, j, ok := r.register(key)
	if !ok {
		return
	}
	go func() {
		defer r.wg.Done()
		defer j.cancel()
		v, err := work(ctx)
		r.post(func() {
			// 在主线程判定是否仍是当前任务，过期结果直接丢弃
			if r.current(key, j, true) && done != nil {
				done(v, err)
			}
		})
	}()
}

// Stream 后台执行持续产出的 work：每次 emit 的值在主线程交给 onItem，work 返回后在主线程调用 done。
// 被同 key 新任务顶替、被 Cancel 或 Runner 已 Close 之后，onItem 和 done 都不再调用。
func Stream[T any](r *Runner, key string, work func(ctx context.Context, emit func(T)) error, onItem func(T), done func(error)) {
	ctx, j, ok := r.register(key)
	if !ok {
		return
	}
	go func() {
		defer r.wg.Done()
		defer j.cancel()
		emit := func(v T) {
			if ctx.Err() != nil {
				return
			}
			r.post(func() {
				if r.current(key, j, false) && onItem != nil {
					onItem(v)
				}
			})
		}
		err := work(ctx, emit)
		r.post(func() {
			if r.current(key, j, true) && done != nil {
				done(err)
			}
		})
	}()
}

// register 登记 key 的新任务（顶替旧任务），返回它的 ctx；Runner 已关闭时 ok 为 false。
func (r *Runner) register(key string) (context.Context, *job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, false
	}
	if old := r.jobs[key]; old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(r.ctx)
	j := &job{cancel: cancel}
	r.jobs[key] = j
	r.wg.Add(1)
	return ctx, j, true
}

// current 报告 j 是否仍是 key 的当前任务；finish 为 true 时顺带把它登记为已完成。
func (r *Runner) current(key string, j *job, finish bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ok := !r.closed && r.jobs[key] == j
	if ok && finish {
		delete(r.jobs, key)
	}
	return ok
}

// Running 报告 key 对应的任务是否还在进行（结果尚未交付）。
func (r *Runner) Running(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[key] != nil
}

// Cancel 取消 key 对应的任务，其结果会被丢弃。
func (r *Runner) Cancel(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j := r.jobs[key]; j != nil {
		j.cancel()
		delete(r.jobs, key)
	}
}

// Close 取消全部任务并等待后台 goroutine 退出。之后 Go 不再生效。
func (r *Runner) Close() {
	r.mu.Lock()
	r.closed = true
	r.jobs = map[string]*job{}
	r.mu.Unlock()
	r.stop()
	r.wg.Wait()
}
