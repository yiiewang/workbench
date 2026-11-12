package server

// AI 写操作审批协调器（Human-in-the-loop）。
//
// 背景：Agent 模式下写类工具（write_file / update_task）不能静默执行，
// 必须先经用户确认。但确认动作发生在另一个 HTTP 请求（POST /api/ai/approval），
// 与 SSE 对话流（POST /api/ai/chat）是两条独立连接，需要一个跨请求的
// 协调层把两边接起来：
//
//	SSE 流（chat）                     审批回调（approval）
//	──────────────                     ──────────────────
//	create(id) → 发 approval_request
//	                                   收到用户决定
//	select 等待 ch  ←—————————————————— resolve(id) 发送决定
//	批准 → 执行工具
//	拒绝/超时 → 结果告知模型
//
// 安全设计：
//   - 每个审批绑定发起用户，回调时校验 userID（防止替他人批/探测他人审批）；
//   - 一次性：resolve 后立即删除，重复提交同一 id 返回失败；
//   - 等待方超时/断开时由 remove 作废，channel 接收方始终只有 create 的调用方。

import (
	"sync"
	"time"
)

// aiApprovalTimeout 审批等待时长：超时视为用户未确认（拒绝处理）。
// 人工操作场景给足时间（用户可能不在屏幕前）。
const aiApprovalTimeout = 5 * time.Minute

// aiApproval 单个待审批项
type aiApproval struct {
	ch     chan bool // 缓冲 1：resolve 方非阻塞发送，等待方 select 读取
	userID int64     // 发起对话的用户（回调校验归属）
}

// aiApprovalHub 待审批表：Agent 循环 create/等待，审批接口 resolve。
// 零值可用（mu 零值即解锁态，pend 惰性初始化）。
type aiApprovalHub struct {
	mu   sync.Mutex
	pend map[string]*aiApproval
}

// create 注册一个待审批项，返回用于等待决定的 channel。
// id 直接复用上游模型的 tool_call ID（一次对话内天然唯一）。
func (h *aiApprovalHub) create(id string, userID int64) <-chan bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pend == nil {
		h.pend = make(map[string]*aiApproval)
	}
	ch := make(chan bool, 1)
	h.pend[id] = &aiApproval{ch: ch, userID: userID}
	return ch
}

// resolve 提交审批决定：校验存在性与用户归属，成功返回 true。
// 一次性：归属校验通过才删除条目（归属不符时不能作废他人审批），
// 重复提交同 id 返回 false。
func (h *aiApprovalHub) resolve(id string, userID int64, approved bool) bool {
	h.mu.Lock()
	ap, ok := h.pend[id]
	if ok && ap.userID != userID {
		h.mu.Unlock()
		return false // 非本人发起：条目保留，不消费
	}
	if ok {
		delete(h.pend, id)
	}
	h.mu.Unlock()
	if !ok {
		return false
	}
	ap.ch <- approved // 缓冲 1，等待方一定在读或已超时离开，不阻塞
	return true
}

// remove 作废一个待审批项（等待方超时/客户端断开时调用）。
// channel 里可能已有决定（回调先到、等待方刚好超时离开）——直接丢弃。
func (h *aiApprovalHub) remove(id string) {
	h.mu.Lock()
	delete(h.pend, id)
	h.mu.Unlock()
}
