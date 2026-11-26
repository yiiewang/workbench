package server

// 编排执行装配：把 internal/pipeline 引擎接到平台已有能力上。
//   tool/<name> → 复用 AI 工具注册表（合成 iris.Context，工具层零改动）
//   api/<path>  → 站内 /api/* 自请求（自签 token 带当前用户身份）
//   llm         → 非流式一次生成（aiChatOnce）
// 每步状态写入 pipeline_runs，查询接口形态照 ai_runs。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
)

// captureWriter 内部调用用的最小 ResponseWriter（不走网络）
type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{header: make(http.Header), status: http.StatusOK}
}

func (w *captureWriter) Header() http.Header         { return w.header }
func (w *captureWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func (w *captureWriter) WriteHeader(code int)        { w.status = code }
func (w *captureWriter) Flush()                      {}

// withToolContext 从 app 的 ContextPool 造一个不与真实请求绑定的 ctx，预置身份
// Values；Request().Context() 直接用后台 run ctx，天然衔接 aiToolRunCtx。
func (s *Server) withToolContext(ctx context.Context, orgID, userID int64, fn func(iris.Context) any) any {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://pipeline.internal/", nil)
	if err != nil {
		return map[string]any{"error": "build tool context failed: " + err.Error()}
	}
	ictx := s.app.ContextPool.Acquire(newCaptureWriter(), req)
	defer s.app.ContextPool.Release(ictx)
	ictx.Values().Set("userID", userID)
	ictx.Values().Set("orgID", orgID)
	// 真实请求的 userName/orgName 由 auth 中间件写入，工具层依赖它们拼
	// 用户目录（userRoot）；定时任务没有中间件，这里按 id 反查补齐，
	// 否则 userRoot 返回空串（表现为 reindex_files 扫空目录"成功"结束）
	if s.db != nil {
		if orgName, userName, err := s.db.GetUserNames(ctx, orgID, userID); err == nil {
			ictx.Values().Set("userName", userName)
			ictx.Values().Set("orgName", orgName)
		}
	}
	return fn(ictx)
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// execPipelineTool 执行一次 AI 工具：写类工具必须在 allowWrite 中显式授权
func (s *Server) execPipelineTool(ctx context.Context, name string, args map[string]any, def pipeline.Definition, rctx *pipeline.RunContext) (any, error) {
	tool := s.aiToolByName(name)
	if tool == nil {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	if tool.NeedApproval && !containsString(def.AllowWrite, name) {
		return nil, fmt.Errorf("write tool %q must be listed in allowWrite", name)
	}
	argsJSON, _ := json.Marshal(args)
	call := aiToolCall{
		ID:       "pipe-" + uuid.NewString(),
		Type:     "function",
		Function: aiToolCallFunction{Name: name, Arguments: string(argsJSON)},
	}
	result := s.withToolContext(ctx, rctx.OrgID, rctx.UserID, func(ictx iris.Context) any {
		return s.execAITool(ictx, call)
	})
	if m, ok := result.(map[string]any); ok {
		if e, ok := m["error"].(string); ok && e != "" {
			return nil, fmt.Errorf("%s", e)
		}
	}
	return result, nil
}

func (s *Server) execPipelineLLM(ctx context.Context, args map[string]any) (any, error) {
	prompt, _ := args["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("llm step requires prompt")
	}
	return s.aiChatOnce(ctx, prompt)
}

// execPipelineAPI 调用站内 /api/*：自签 token 携带当前用户身份，
// 非 GET 方法需在 allowWrite 里按 "api/<path>" 显式授权。
func (s *Server) execPipelineAPI(ctx context.Context, name string, args map[string]any, def pipeline.Definition, rctx *pipeline.RunContext) (any, error) {
	path, method, err := resolveAPIRequest(name, args, def)
	if err != nil {
		return nil, err
	}
	target := path
	if q, ok := args["query"].(map[string]any); ok && len(q) > 0 {
		values := url.Values{}
		for k, v := range q {
			values.Set(k, fmt.Sprint(v))
		}
		target += "?" + values.Encode()
	}
	var body io.Reader
	if args["body"] != nil {
		b, err := json.Marshal(args["body"])
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://pipeline.internal"+target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+GenerateToken(rctx.UserID, s.tokenSecret, 1))
	req.Header.Set("X-Org-Id", strconv.FormatInt(rctx.OrgID, 10))
	req.Header.Set("Accept-Encoding", "identity")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := newCaptureWriter()
	s.app.ServeHTTP(w, req)

	var env Response
	if err := json.Unmarshal(w.body.Bytes(), &env); err != nil {
		return nil, fmt.Errorf("api %s returned non-JSON (status %d)", path, w.status)
	}
	if w.status < 200 || w.status >= 300 || env.Code != CodeOK {
		return nil, fmt.Errorf("api %s: status %d code %d msg %s", path, w.status, env.Code, env.Msg)
	}
	return env.Data, nil
}

// runPipeline 编排执行核心：与 HTTP 上下文解耦，AI 工具 / HTTP 直调 /
// 定时任务触发共用。逐步落库，返回步骤结果与最终错误。
func (s *Server) runPipeline(ctx context.Context, orgID, userID int64, runID string, def pipeline.Definition) ([]pipeline.StepResult, error) {
	defJSON, _ := json.Marshal(def)
	if err := s.db.CreatePipelineRun(ctx, orgID, userID, runID, def.Name, string(defJSON)); err != nil {
		return nil, fmt.Errorf("create pipeline run: %w", err)
	}
	save := func(status, errMsg string, results []pipeline.StepResult) {
		stepsJSON, _ := json.Marshal(results)
		if err := s.db.SavePipelineRun(context.Background(), orgID, userID, db.PipelineRun{
			ID: runID, Status: status, Steps: stepsJSON, Error: errMsg,
		}); err != nil {
			slog.Warn("pipeline run flush failed", "run", runID, "err", err)
		}
	}

	rctx := pipeline.NewRunContext(orgID, userID)
	exec := func(stepCtx context.Context, kind, name string, stepArgs map[string]any, rctx *pipeline.RunContext) (any, error) {
		switch kind {
		case "tool":
			return s.execPipelineTool(stepCtx, name, stepArgs, def, rctx)
		case "llm":
			return s.execPipelineLLM(stepCtx, stepArgs)
		case "api":
			return s.execPipelineAPI(stepCtx, name, stepArgs, def, rctx)
		default:
			return nil, fmt.Errorf("unknown step kind %q", kind)
		}
	}
	runner := pipeline.NewRunner(exec, func(results []pipeline.StepResult) {
		save("running", "", results)
	})
	results, runErr := runner.Run(ctx, def, rctx)
	if runErr != nil {
		save("failed", runErr.Error(), results)
		return results, runErr
	}
	save("done", "", results)
	return results, nil
}

// execRunPipeline AI 工具入口：整条流程一次审批，按序执行并逐步落库。
// 结果只回传步骤摘要，完整输出由 GET /api/pipeline/runs/{id} 查询。
func execRunPipeline(s *Server, ctx iris.Context, args map[string]any) any {
	raw, err := json.Marshal(args)
	if err != nil {
		return map[string]any{"error": "invalid pipeline arguments"}
	}
	var def pipeline.Definition
	if err := json.Unmarshal(raw, &def); err != nil {
		return map[string]any{"error": "invalid pipeline definition: " + err.Error()}
	}
	if err := def.Validate(); err != nil {
		return map[string]any{"error": "invalid pipeline definition: " + err.Error()}
	}

	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	runID := uuid.NewString()
	results, runErr := s.runPipeline(aiToolRunCtx(ctx), orgID, userID, runID, def)
	status, errMsg := "done", ""
	if runErr != nil {
		status, errMsg = "failed", runErr.Error()
	}

	brief := make([]map[string]any, 0, len(results))
	for _, r := range results {
		brief = append(brief, map[string]any{
			"id": r.ID, "uses": r.Uses, "status": r.Status,
			"error": r.Error, "durationMs": r.DurationMs,
		})
	}
	return map[string]any{
		"runId":  runID,
		"name":   def.Name,
		"status": status,
		"error":  errMsg,
		"steps":  brief,
		"note":   "完整步骤输出可用 GET /api/pipeline/runs/{id} 查询；断连后状态仍会落库",
	}
}

// ============================================================
// 编排运行状态查询（客户端断开后仍可查到每步结果）
// ============================================================

func (s *Server) handlePipelineRunsList(ctx iris.Context) {
	limit, _ := strconv.Atoi(ctx.URLParam("limit"))
	runs, err := s.db.ListPipelineRuns(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), limit)
	if err != nil {
		serverError(ctx, "list pipeline runs failed", err)
		return
	}
	writeOK(ctx, map[string]any{"runs": runs})
}

func (s *Server) handlePipelineRunGet(ctx iris.Context) {
	run, ok, err := s.db.GetPipelineRun(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), ctx.Params().Get("id"))
	if err != nil {
		serverError(ctx, "get pipeline run failed", err)
		return
	}
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	writeOK(ctx, run)
}
