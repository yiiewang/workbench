package schedule

// 事件总线与到期扫描 job 单测。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yiiewang/workbench/internal/db"
)

func TestEventBus_PubSub(t *testing.T) {
	bus := NewEventBus()
	var mu sync.Mutex
	got := []int{}

	bus.Subscribe("t", func(ctx context.Context, evt Event) { mu.Lock(); got = append(got, 1); mu.Unlock() })
	bus.Subscribe("t", func(ctx context.Context, evt Event) { mu.Lock(); got = append(got, 2); mu.Unlock() })
	// 其他 topic 不受影响
	bus.Subscribe("other", func(ctx context.Context, evt Event) { mu.Lock(); got = append(got, 99); mu.Unlock() })

	bus.Publish(context.Background(), Event{Topic: "t"})

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("handlers called %d times, want 2: %v", len(got), got)
	}
	for _, v := range got {
		if v == 99 {
			t.Fatal("other-topic handler must not be called")
		}
	}
}

func TestEventBus_HandlerPanicNotFatal(t *testing.T) {
	bus := NewEventBus()
	var ok bool
	var mu sync.Mutex

	bus.Subscribe("t", func(ctx context.Context, evt Event) { panic("boom") })
	bus.Subscribe("t", func(ctx context.Context, evt Event) { mu.Lock(); ok = true; mu.Unlock() })

	bus.Publish(context.Background(), Event{Topic: "t"})

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		done := ok
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !ok {
		t.Fatal("second handler should still run after first panics")
	}
}

// mockQuerier 扫描 job 的可测依赖
type mockQuerier struct {
	tasks    []db.DueTaskRow
	notified map[string]bool // 已发过的 dedupKey+date
}

func (m *mockQuerier) ScanDueTasks(ctx context.Context, today string) ([]db.DueTaskRow, error) {
	return m.tasks, nil
}

func (m *mockQuerier) MarkNotified(ctx context.Context, topic, dedupKey, notifyDate string) (bool, error) {
	k := dedupKey + "@" + notifyDate
	if m.notified[k] {
		return false, nil
	}
	m.notified[k] = true
	return true, nil
}

func TestRunDueScan_DedupAndPublish(t *testing.T) {
	bus := NewEventBus()
	var mu sync.Mutex
	var events []TaskDueEvent
	bus.Subscribe(TopicTaskDue, func(ctx context.Context, evt Event) {
		if e, ok := evt.Payload.(TaskDueEvent); ok {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		}
	})

	q := &mockQuerier{notified: map[string]bool{}, tasks: []db.DueTaskRow{
		{Task: db.TaskItem{ID: "t1", Title: "today", Due: "2026-11-11"}, UserID: 1, OrgID: 1},
		{Task: db.TaskItem{ID: "t2", Title: "overdue", Due: "2026-11-08"}, UserID: 1, OrgID: 1},
	}}

	// 快照辅助：绝不在持锁状态下做后续调用（防自死锁）
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(events)
	}
	snapshot := func() []TaskDueEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]TaskDueEvent(nil), events...)
	}
	waitFor := func(n int) {
		deadline := time.Now().Add(time.Second)
		for count() < n && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}

	fireAt := at(2026, 11, 11, 9, 0)
	runDueScan(context.Background(), fireAt, q, time.UTC, bus)
	waitFor(2)

	evs := snapshot()
	if len(evs) != 2 {
		t.Fatalf("published %d events, want 2", len(evs))
	}
	// 逾期天数（Publish 为每 handler 独立 goroutine，事件到达顺序不定——按 id 匹配断言）
	byID := map[string]int{}
	for _, e := range evs {
		byID[string(e.Task.ID)] = e.OverdueDays
	}
	if byID["t1"] != 0 || byID["t2"] != 3 {
		t.Fatalf("overdue days = %v, want t1:0 t2:3", byID)
	}

	// 同日第二次扫描：全部去重，不再发事件
	runDueScan(context.Background(), fireAt.Add(time.Minute), q, time.UTC, bus)
	waitFor(3) // 若误发会到 3；正确应停在 2
	if got := count(); got != 2 {
		t.Fatalf("same-day rescan published again: %d", got)
	}

	// 次日扫描：再次投递（每天一次）
	runDueScan(context.Background(), at(2026, 11, 12, 9, 0), q, time.UTC, bus)
	waitFor(4)
	if got := count(); got != 4 {
		t.Fatalf("next-day rescan should publish 2 more, got %d", got)
	}
}
