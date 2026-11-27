package server

// 编排 HTTP 入口：直接运行一份 Definition（定时任务的内联编排、前端立即执行共用）。
// 编排流程不落独立模板表，Definition 直接存在 jobs.target_json 里。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/pipeline"
)

// pipelineRunTimeout 后台异步执行一份编排的总超时
const pipelineRunTimeout = 15 * time.Minute

// pipelineStreamPoll 轮询 pipeline_runs 的间隔（进度按 DAG 层落库，秒级足够）
const pipelineStreamPoll = time.Second

// pipelineStreamHeartbeat SSE 心跳间隔：慢步骤（llm 单步最长 5min）期间
// 无事件可推，定期发一条注释行防止代理/浏览器按空闲连接掐断
const pipelineStreamHeartbeat = 15 * time.Second

// pipelineStreamWriter 最小 SSE 写出器：http.Flusher 断言刷新。
// ai.go 的 aiRunWriter 自带镜像落库逻辑，这里只需要裸写。
type pipelineStreamWriter struct{ w io.Writer }

func (s pipelineStreamWriter) Write(p []byte) (int, error) { return s.w.Write(p) }

func (s pipelineStreamWriter) Flush() {
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func parsePipelineDefinition(raw string) (pipeline.Definition, error) {
	var def pipeline.Definition
	if err := json.Unmarshal([]byte(raw), &def); err != nil {
		return def, fmt.Errorf("definition JSON 非法：%w", err)
	}
	if err := def.Validate(); err != nil {
		return def, err
	}
	return def, nil
}

// handlePipelineRun POST /api/pipeline/run — 后台异步执行一份 Definition
func (s *Server) handlePipelineRun(ctx iris.Context) {
	var body struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := readJSON(ctx, &body); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	def, err := parsePipelineDefinition(string(body.Definition))
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, err.Error())
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	runID := uuid.NewString()
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), pipelineRunTimeout)
		defer cancel()
		if _, err := s.runPipeline(runCtx, orgID, userID, runID, def); err != nil {
			slog.Warn("pipeline run failed", "run", runID, "err", err)
		}
	}()
	writeOK(ctx, map[string]any{
		"runId":   runID,
		"started": true,
		"note":    "用 GET /api/pipeline/runs/{id}/stream 实时订阅，或 GET /api/pipeline/runs/{id} 查询结果",
	})
}

// handlePipelineRunStream GET /api/pipeline/runs/{id}/stream — SSE 实时推送运行进度。
// 推送源是 pipeline_runs 表轮询：Runner.onStep 按层全量快照落库，本端点比对
// status/steps/err_msg 变化即推一份 progress（data 为整份运行快照），终态
// （done/failed）再推 end 后关闭。不做进程内 pub/sub——DB 轮询对定时触发等
// 所有入口产生的 run 一视同仁，实现最简，且执行端无需任何改动。
func (s *Server) handlePipelineRunStream(ctx iris.Context) {
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	runID := ctx.Params().Get("id")
	rctx := ctx.Request().Context()

	run, ok, err := s.db.GetPipelineRun(rctx, orgID, userID, runID)
	if err != nil {
		serverError(ctx, "get pipeline run failed", err)
		return
	}
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	ctx.ContentType("text/event-stream; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("X-Accel-Buffering", "no")
	ctx.StatusCode(iris.StatusOK)
	w := pipelineStreamWriter{w: ctx.ResponseWriter()}
	// 连接建立先推一份当前快照（叠加历史 run 时立即有状态可显示）
	sseWriteEvent(w, "progress", run)
	if run.Status != "running" {
		sseWriteEvent(w, "end", map[string]string{"status": run.Status})
		return
	}

	lastStatus, lastSteps, lastErr := run.Status, string(run.Steps), run.Error
	ticker := time.NewTicker(pipelineStreamPoll)
	defer ticker.Stop()
	hb := time.NewTicker(pipelineStreamHeartbeat)
	defer hb.Stop()
	// 连接寿命上限 = 执行总超时 + 1min 余量：进程崩溃会让 run 永远停在
	// running（落库方已不在），无此上限连接将永久挂起
	limit := time.After(pipelineRunTimeout + time.Minute)
	for {
		select {
		case <-rctx.Done(): // 客户端断开
			return
		case <-limit:
			sseWriteEvent(w, "error", map[string]string{"msg": "run 超时仍在 running，订阅结束"})
			return
		case <-hb.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			w.Flush()
		case <-ticker.C:
			r, ok, err := s.db.GetPipelineRun(rctx, orgID, userID, runID)
			if err != nil {
				slog.Error("pipeline run stream: query failed", "run", runID, "err", err)
				return
			}
			if !ok {
				sseWriteEvent(w, "error", map[string]string{"msg": "run not found"})
				return
			}
			if r.Status == lastStatus && string(r.Steps) == lastSteps && r.Error == lastErr {
				continue
			}
			lastStatus, lastSteps, lastErr = r.Status, string(r.Steps), r.Error
			sseWriteEvent(w, "progress", r)
			if r.Status != "running" {
				sseWriteEvent(w, "end", map[string]string{"status": r.Status})
				return
			}
		}
	}
}
