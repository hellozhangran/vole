package task

import (
	"context"
	"testing"
	"time"
)

// mainLoop 模拟主线程：post 进来的函数在 drain 时按序执行。
type mainLoop struct{ ch chan func() }

func newLoop() *mainLoop           { return &mainLoop{ch: make(chan func(), 16)} }
func (l *mainLoop) post(fn func()) { l.ch <- fn }
func (l *mainLoop) runOne(t *testing.T) {
	t.Helper()
	select {
	case fn := <-l.ch:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("等待主线程回调超时")
	}
}

func TestSameKeyDropsOld(t *testing.T) {
	loop := newLoop()
	r := NewRunner(loop.post)
	defer r.Close()

	release := make(chan struct{})
	var got []string
	var oldCanceled bool
	Go(r, "scan", func(ctx context.Context) (string, error) {
		<-ctx.Done() // 被新任务顶替时应收到取消
		oldCanceled = true
		<-release
		return "old", nil
	}, func(v string, _ error) { got = append(got, v) })
	Go(r, "scan", func(context.Context) (string, error) { return "new", nil },
		func(v string, _ error) { got = append(got, v) })

	loop.runOne(t) // new 先完成
	close(release)
	loop.runOne(t) // old 的回调到达但应被丢弃
	if !oldCanceled || len(got) != 1 || got[0] != "new" {
		t.Fatalf("got %v, oldCanceled=%v", got, oldCanceled)
	}
	if r.Running("scan") {
		t.Fatal("完成后不应仍在运行")
	}
}

func TestCancelAndClose(t *testing.T) {
	loop := newLoop()
	r := NewRunner(loop.post)
	called := false
	Go(r, "a", func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() },
		func(int, error) { called = true })
	r.Cancel("a")
	loop.runOne(t)
	if called {
		t.Fatal("Cancel 后不应回调")
	}

	Go(r, "b", func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, nil },
		func(int, error) { called = true })
	r.Close() // 应取消并等到 goroutine 退出
	loop.runOne(t)
	if called {
		t.Fatal("Close 后不应回调")
	}
	Go(r, "c", func(context.Context) (int, error) { return 1, nil }, func(int, error) { called = true })
	select {
	case <-loop.ch:
		t.Fatal("Close 后 Go 不应生效")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStream(t *testing.T) {
	loop := newLoop()
	r := NewRunner(loop.post)
	defer r.Close()

	var items []int
	var doneErr error
	finished := false
	Go2 := func() {
		Stream(r, "watch", func(ctx context.Context, emit func(int)) error {
			for i := 1; i <= 3; i++ {
				emit(i)
			}
			return context.DeadlineExceeded // 模拟进程意外退出
		}, func(v int) { items = append(items, v) }, func(err error) { doneErr, finished = err, true })
	}
	Go2()
	for i := 0; i < 4; i++ {
		loop.runOne(t)
	}
	if len(items) != 3 || !finished || doneErr != context.DeadlineExceeded {
		t.Fatalf("items=%v finished=%v err=%v", items, finished, doneErr)
	}
	if r.Running("watch") {
		t.Fatal("结束后不应仍在运行")
	}
}

func TestStreamCancelDropsLaterItems(t *testing.T) {
	loop := newLoop()
	r := NewRunner(loop.post)
	defer r.Close()

	emitted := make(chan struct{})
	var items []int
	called := false
	Stream(r, "watch", func(ctx context.Context, emit func(int)) error {
		emit(1)
		close(emitted)
		<-ctx.Done()
		emit(2) // 取消后的 emit 不应再投递
		return ctx.Err()
	}, func(v int) { items = append(items, v) }, func(error) { called = true })

	<-emitted
	loop.runOne(t) // 第 1 帧照常交付
	r.Cancel("watch")
	loop.runOne(t) // done 的回调到达但被丢弃
	select {
	case fn := <-loop.ch:
		fn()
	case <-time.After(100 * time.Millisecond):
	}
	if len(items) != 1 || called {
		t.Fatalf("取消后不应再交付：items=%v done=%v", items, called)
	}
}
