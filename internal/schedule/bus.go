// Package schedule 进程内定时调度与事件总线。
//
// 架构：
//
//	Scheduler（cron 引擎，每分钟 tick + 表达式匹配）
//	     │ fire
//	     ▼
//	EventBus（topic → handlers，goroutine 异步分发）
//	     │ publish
//	     ▼
//	订阅者（EmailNotifier 等，未来可扩展 webhook / 站内信）
//
// 设计取舍：
//   - EventBus 为进程内实现（无 MQ）：单实例部署下零依赖、零运维；
//     发布不阻塞（handler 各自 goroutine），单订阅者 panic 不影响其他订阅者；
//   - Scheduler 手写而非 robfig/cron：每分钟统一 tick + 表达式匹配，
//     避免外部依赖，也便于单测（表达式匹配器是纯函数）。
package schedule

import (
	"context"
	"log/slog"
	"sync"
)

// Event 总线事件
type Event struct {
	Topic   string
	Payload any
}

// Handler 事件订阅者处理函数
type Handler func(ctx context.Context, evt Event)

// EventBus 进程内发布/订阅：topic → handlers。
// Publish 为每个 handler 起独立 goroutine（发布方不等待、不被慢订阅者拖住），
// handler panic recover 记日志，不影响总线与其他订阅者。
type EventBus struct {
	mu   sync.RWMutex
	subs map[string][]Handler
}

// NewEventBus 创建事件总线
func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[string][]Handler)}
}

// Subscribe 订阅 topic（相同 handler 可重复注册，分发时逐个调用）
func (b *EventBus) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], h)
}

// Publish 异步分发事件到所有订阅者。
// ctx 为订阅者共享的根 context：服务停止时取消，订阅者应感知退出。
func (b *EventBus) Publish(ctx context.Context, evt Event) {
	b.mu.RLock()
	handlers := make([]Handler, len(b.subs[evt.Topic]))
	copy(handlers, b.subs[evt.Topic])
	b.mu.RUnlock()

	for _, h := range handlers {
		go func(h Handler) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("event bus handler panic", "topic", evt.Topic, "panic", r)
				}
			}()
			h(ctx, evt)
		}(h)
	}
}

// Topics 总线内省：返回各 topic 的订阅者数（list_events 工具 / 调试用）
func (b *EventBus) Topics() map[string]int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]int, len(b.subs))
	for t, hs := range b.subs {
		out[t] = len(hs)
	}
	return out
}
