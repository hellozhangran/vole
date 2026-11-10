package status

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Shared 让多个界面（状态 tab、菜单栏面板）共用一个采集进程：第一个订阅者开始 Watch 时启动底层采集，
// 最后一个订阅者退出时停掉；每一帧发给所有订阅者，新订阅者先拿到最近一帧（不用等下一秒）。
// 底层采集意外退出时，所有订阅者的 Watch 都返回那个错误（各自按原来的逻辑重启：再调 Watch 就会重新起一个进程）。
type Shared struct {
	src Source

	mu     sync.Mutex
	subs   map[int]*sub
	next   int
	cancel context.CancelFunc // 底层采集在跑时非空
	last   *Snapshot
	gen    int // 底层采集的代数：每次重新启动加一
}

type sub struct {
	emit func(Snapshot)
	done chan error // 底层采集意外退出时收到错误
}

// NewShared 包一层可共享的数据源。
func NewShared(src Source) *Shared { return &Shared{src: src, subs: map[int]*sub{}} }

var _ Source = (*Shared)(nil)

// Watch 订阅：阻塞到 ctx 取消（返回 ctx.Err()）或底层采集意外退出（返回那个错误）。
// interval 以第一个订阅者的为准（状态 tab 和面板都是 1 秒）。
func (s *Shared) Watch(ctx context.Context, interval time.Duration, emit func(Snapshot)) error {
	me := &sub{emit: emit, done: make(chan error, 1)}
	s.mu.Lock()
	id := s.next
	s.next++
	s.subs[id] = me
	if s.last != nil {
		last := *s.last
		s.mu.Unlock()
		emit(last)
		s.mu.Lock()
	}
	if s.cancel == nil {
		s.start(interval)
	}
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		s.leave(id)
		return ctx.Err()
	case err := <-me.done:
		return err
	}
}

// start 起底层采集（调用方持锁）。
func (s *Shared) start(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.gen++
	gen := s.gen
	go func() {
		err := s.src.Watch(ctx, interval, func(snap Snapshot) {
			s.mu.Lock()
			if s.gen != gen {
				s.mu.Unlock()
				return
			}
			s.last = &snap
			subs := make([]*sub, 0, len(s.subs))
			for _, x := range s.subs {
				subs = append(subs, x)
			}
			s.mu.Unlock()
			for _, x := range subs {
				x.emit(snap)
			}
		})
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.gen != gen {
			return // 已经被停掉、又重新起了一个
		}
		s.cancel = nil
		if errors.Is(err, context.Canceled) {
			return // 最后一个订阅者走了，主动停的
		}
		// 意外退出：通知所有订阅者，清空（它们按自己的逻辑重启）
		for id, x := range s.subs {
			x.done <- err
			delete(s.subs, id)
		}
	}()
}

// leave 退订；没人订了就停掉底层采集。
func (s *Shared) leave(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, id)
	if len(s.subs) == 0 && s.cancel != nil {
		s.cancel()
		s.cancel = nil
		s.gen++ // 旧的那一代收尾时不再动状态
	}
}

// Running 报告底层采集是否在跑（测试和实测用）。
func (s *Shared) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel != nil
}
