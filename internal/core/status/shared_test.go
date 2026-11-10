package status

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSource 每 10ms 发一帧，记录启动了几次、现在有几个在跑。
type fakeSource struct {
	starts, live atomic.Int32
	fail         chan error
}

func (f *fakeSource) Watch(ctx context.Context, _ time.Duration, emit func(Snapshot)) error {
	f.starts.Add(1)
	f.live.Add(1)
	defer f.live.Add(-1)
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-f.fail:
			return err
		case <-t.C:
			n++
			emit(Snapshot{Health: n})
		}
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(what)
}

func TestSharedOneProcessForManySubscribers(t *testing.T) {
	src := &fakeSource{fail: make(chan error, 1)}
	s := NewShared(src)
	var a, b atomic.Int32
	ctxA, stopA := context.WithCancel(context.Background())
	ctxB, stopB := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = s.Watch(ctxA, time.Second, func(Snapshot) { a.Add(1) }) })
	waitFor(t, "A 应收到帧", func() bool { return a.Load() > 0 })
	wg.Go(func() { _ = s.Watch(ctxB, time.Second, func(Snapshot) { b.Add(1) }) })
	waitFor(t, "B 应收到帧", func() bool { return b.Load() > 1 })
	if src.starts.Load() != 1 || src.live.Load() != 1 {
		t.Fatalf("两个订阅者只能有一个采集进程：启动 %d 次，在跑 %d 个", src.starts.Load(), src.live.Load())
	}
	stopA()
	time.Sleep(30 * time.Millisecond)
	if !s.Running() || src.live.Load() != 1 {
		t.Fatal("还有订阅者时不能停")
	}
	stopB()
	wg.Wait()
	waitFor(t, "最后一个订阅者走了应停掉采集", func() bool { return src.live.Load() == 0 && !s.Running() })

	// 再订阅：重新起一个，先拿到最近一帧
	var first atomic.Int32
	ctxC, stopC := context.WithCancel(context.Background())
	wg.Go(func() {
		_ = s.Watch(ctxC, time.Second, func(snap Snapshot) {
			first.CompareAndSwap(0, int32(snap.Health)+1)
		})
	})
	waitFor(t, "新订阅者应先拿到最近一帧", func() bool { return first.Load() > 1 })
	waitFor(t, "应重新启动采集", func() bool { return src.starts.Load() == 2 })
	stopC()
	wg.Wait()
}

func TestSharedPropagatesExit(t *testing.T) {
	src := &fakeSource{fail: make(chan error, 1)}
	s := NewShared(src)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- s.Watch(context.Background(), time.Second, func(Snapshot) {}) }()
	}
	waitFor(t, "应启动", func() bool { return src.live.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	boom := &ExitError{ExitCode: 1, Err: errors.New("boom")}
	src.fail <- boom
	for i := 0; i < 2; i++ {
		if err := <-errs; !errors.Is(err, boom) {
			t.Fatalf("意外退出要通知所有订阅者：%v", err)
		}
	}
	if s.Running() {
		t.Fatal("退出后应标记为没在跑，下一次 Watch 会重新启动")
	}
}
