package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// aiStreamWriter SSE 输出流：写增量 + 立即 flush（iris 的 ResponseWriter 满足此接口）
type aiStreamWriter interface {
	io.Writer
	Flush()
}

// sseStreamWriter SSE 输出流：写入即 flush，写失败后静默吞掉后续写入。
// state 仅 AI 对话镜像需要（断连时置 disconnected，让 Agent 循环继续跑完并落库）；
// state 为 nil 时只保留断连静默语义（pipeline 订阅等纯推送场景）。
type sseStreamWriter struct {
	w     io.Writer
	state *aiRunState
	alive bool
}

func newSSEStreamWriter(w io.Writer, state *aiRunState) *sseStreamWriter {
	return &sseStreamWriter{w: w, state: state, alive: true}
}

func (w *sseStreamWriter) Write(p []byte) (int, error) {
	if !w.alive {
		return len(p), nil
	}
	if _, err := w.w.Write(p); err != nil {
		w.alive = false
		if w.state != nil {
			w.state.disconnected = true
		}
	}
	// 写失败不向上传播：SSE 推送方无法有意义地处理，统一以降级（吞掉写入）收场
	return len(p), nil
}

func (w *sseStreamWriter) Flush() {
	if !w.alive {
		return
	}
	if f, ok := w.w.(http.Flusher); ok {
		f.Flush()
	}
}

// sseWriteEvent 向 SSE 流写一个自定义事件（event + data 两行 + 空行）
func sseWriteEvent(w aiStreamWriter, event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	w.Flush()
}
