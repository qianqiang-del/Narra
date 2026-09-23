package retention

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore 记录每次清理的 cutoff，并按需返回错误或条数。
type fakeStore struct {
	mu      sync.Mutex
	cutoffs []time.Time
	deleted int64
	err     error
}

func (s *fakeStore) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoffs = append(s.cutoffs, before)
	return s.deleted, s.err
}

func (s *fakeStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cutoffs)
}

func (s *fakeStore) lastCutoff() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cutoffs[len(s.cutoffs)-1]
}

// 一轮清理要挨个扫到每张表，cutoff 是"刚刚"—— 保留期写在数据里，清理侧只认 expires_at。
func TestSweepCleansEveryTable(t *testing.T) {
	first := &fakeStore{deleted: 3}
	second := &fakeStore{}
	cleaner := New(
		Table{Name: "conversation_events", Store: first},
		Table{Name: "agent_trace_spans", Store: second},
	)

	before := time.Now()
	cleaner.sweep()
	after := time.Now()

	if first.calls() != 1 || second.calls() != 1 {
		t.Fatalf("清理次数 = %d / %d，期望各 1 次", first.calls(), second.calls())
	}
	if cutoff := first.lastCutoff(); cutoff.Before(before) || cutoff.After(after) {
		t.Errorf("cutoff = %v，期望落在 [%v, %v] 之间", cutoff, before, after)
	}
}

// 一张表删失败只记日志，不能拦住后面的表 —— 清理是尽力而为的后台任务。
func TestSweepContinuesAfterFailure(t *testing.T) {
	broken := &fakeStore{err: errors.New("数据库抖动")}
	healthy := &fakeStore{deleted: 1}
	cleaner := New(
		Table{Name: "broken", Store: broken},
		Table{Name: "healthy", Store: healthy},
	)

	cleaner.sweep()

	if healthy.calls() != 1 {
		t.Fatalf("健康表的清理次数 = %d，期望 1（前面的表失败不该拦住它）", healthy.calls())
	}
}

// 启动时先清一轮（服务停了几天的场景），Stop 能停稳。
func TestStartSweepsImmediatelyAndStops(t *testing.T) {
	store := &fakeStore{}
	cleaner := New(Table{Name: "conversation_events", Store: store})
	cleaner.interval = time.Hour // 只验证启动那一次

	cleaner.Start()

	deadline := time.Now().Add(time.Second)
	for store.calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.calls() == 0 {
		t.Fatal("启动之后没有立即清一轮")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cleaner.Stop(ctx); err != nil {
		t.Fatalf("Stop 返回错误: %v", err)
	}

	// 重复 Stop 不该 panic 或卡住（once 保证 cancel 只执行一次）。
	if err := cleaner.Stop(ctx); err != nil {
		t.Fatalf("重复 Stop 返回错误: %v", err)
	}
}
