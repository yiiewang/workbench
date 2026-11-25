package server

// 编排 HTTP 入口：直接运行 Definition、模板 CRUD、手动触发模板，
// 以及注册给通用调度器的执行入口。模板只存 Definition；定时配置
// 属于调度领域（schedule_triggers），不在这里。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/pipeline"
)

// pipelineRunTimeout 后台异步执行一份编排的总超时
const pipelineRunTimeout = 15 * time.Minute

type pipelineTemplateBody struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
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

func validatePipelineTemplateBody(b pipelineTemplateBody) error {
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if _, err := parsePipelineDefinition(b.Definition); err != nil {
		return err
	}
	return nil
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
		"note":    "用 GET /api/pipeline/runs/{id} 查询逐步结果",
	})
}

// ============================================================
// 编排模板 CRUD（纯 Definition 固化复用；定时规则走 /api/schedule/triggers）
// ============================================================

func (s *Server) handlePipelineTemplatesList(ctx iris.Context) {
	limit, _ := strconv.Atoi(ctx.URLParam("limit"))
	templates, err := s.db.ListPipelineTemplates(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), limit)
	if err != nil {
		serverError(ctx, "list pipeline templates failed", err)
		return
	}
	writeOK(ctx, map[string]any{"templates": templates})
}

// handlePipelineTemplateGet 模板详情（含 definition，编辑表单用）
func (s *Server) handlePipelineTemplateGet(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	t, ok, err := s.db.GetPipelineTemplate(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id)
	if err != nil || !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	writeOK(ctx, t)
}

func (s *Server) handlePipelineTemplateCreate(ctx iris.Context) {
	var body pipelineTemplateBody
	if err := readJSON(ctx, &body); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if err := validatePipelineTemplateBody(body); err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, err.Error())
		return
	}
	id, err := s.db.CreatePipelineTemplate(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx),
		body.Name, body.Definition)
	if err != nil {
		serverError(ctx, "create pipeline template failed", err)
		return
	}
	writeOK(ctx, map[string]any{"id": id})
}

func (s *Server) handlePipelineTemplateUpdate(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	var body pipelineTemplateBody
	if err := readJSON(ctx, &body); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if err := validatePipelineTemplateBody(body); err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, err.Error())
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	_, ok, err := s.db.GetPipelineTemplate(ctx.Request().Context(), orgID, userID, id)
	if err != nil || !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	if err := s.db.UpdatePipelineTemplate(ctx.Request().Context(), orgID, userID, id,
		body.Name, body.Definition); err != nil {
		serverError(ctx, "update pipeline template failed", err)
		return
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}

func (s *Server) handlePipelineTemplateDelete(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	if err := s.db.DeletePipelineTemplate(ctx.Request().Context(), orgID, userID, id); err != nil {
		serverError(ctx, "delete pipeline template failed", err)
		return
	}
	// 模板删除后清理指向它的触发规则，避免通用调度器每分钟报「目标不存在」
	if err := s.db.DeleteCronTriggersForTarget(ctx.Request().Context(), orgID, userID, "pipeline_template", id); err != nil {
		slog.Warn("delete orphan schedule triggers failed", "template", id, "err", err)
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}

// handlePipelineTemplateRun POST /api/pipeline/templates/{id}/run — 手动触发
func (s *Server) handlePipelineTemplateRun(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	orgID, userID := currentOrgID(ctx), currentUserID(ctx)
	t, ok, err := s.db.GetPipelineTemplate(ctx.Request().Context(), orgID, userID, id)
	if err != nil || !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	def, err := parsePipelineDefinition(t.Definition)
	if err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, err.Error())
		return
	}
	// 手动触发只负责启动一次执行；状态与过程由 pipeline_runs 记录，
	// 触发规则自身的 last_run_at/last_status 只归定时调度使用。
	runID := uuid.NewString()
	go func() {
		if _, err := s.runTemplateAsync(runID, orgID, userID, def); err != nil {
			slog.Warn("pipeline template manual run failed", "template", id, "run", runID, "err", err)
		}
	}()
	writeOK(ctx, map[string]any{
		"runId": runID, "templateId": id, "started": true,
		"note": "用 GET /api/pipeline/runs/{id} 查询逐步结果",
	})
}

// runTemplateAsync 后台执行一次模板（带独立超时，不绑定请求连接）
func (s *Server) runTemplateAsync(runID string, orgID, userID int64, def pipeline.Definition) ([]pipeline.StepResult, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), pipelineRunTimeout)
	defer cancel()
	return s.runPipeline(runCtx, orgID, userID, runID, def)
}

// triggerPipelineTemplate 是 pipeline 模块注册给通用调度器的执行入口。
// 只认通用触发规则行（含归属与 target_id），按 id 读模板 Definition 并执行；
// schedule 包不需要知道 pipeline 是什么，也不需要反向依赖 server。
func (s *Server) triggerPipelineTemplate(runCtx context.Context, t db.CronTriggerWithOwner) error {
	tpl, ok, err := s.db.GetPipelineTemplate(runCtx, t.OrgID, t.UserID, t.TargetID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pipeline template %d not found", t.TargetID)
	}
	def, err := parsePipelineDefinition(tpl.Definition)
	if err != nil {
		return err
	}
	_, err = s.runPipeline(runCtx, t.OrgID, t.UserID, uuid.NewString(), def)
	return err
}
