package server

// 通用定时触发规则 API（/api/schedule/triggers）。
// schedule 包只认「target_type + target_id + cron」；目标是否存在、归谁所有
// 由 server 装配层在这里校验，因此 schedule 不需要认识任何业务对象。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/schedule"
)

// supportedTriggerTargets 装配层已注册 handler 的目标类型白名单。
// 新增领域对象时，只需在 server.go 注册 handler 并在这里加一行。
var supportedTriggerTargets = map[string]bool{
	"pipeline_template": true,
}

type scheduleTriggerBody struct {
	TargetType string `json:"targetType"`
	TargetID   int64  `json:"targetId"`
	CronExpr   string `json:"cronExpr"`
	Enabled    *bool  `json:"enabled"`
}

// validateScheduleTriggerCron 校验 cron 并给 enabled 填默认值。
// targetType/targetID 仅创建时校验，更新时目标绑定不可变。
func validateScheduleTriggerCron(b *scheduleTriggerBody) string {
	b.CronExpr = strings.TrimSpace(b.CronExpr)
	if b.CronExpr == "" {
		return "cronExpr is required"
	}
	if _, err := schedule.ParseCron(b.CronExpr); err != nil {
		return "invalid cron expression: " + err.Error()
	}
	if b.Enabled == nil {
		t := true
		b.Enabled = &t
	}
	return ""
}

// validateTriggerTarget 校验目标属于当前用户；返回 ok=false 表示目标不存在或类型未注册。
// err 非 nil 属于内部错误，由 handler 走 500。
func (s *Server) validateTriggerTarget(ctx iris.Context, targetType string, targetID int64) (bool, error) {
	if !supportedTriggerTargets[targetType] {
		return false, nil
	}
	if targetID <= 0 {
		return false, nil
	}
	switch targetType {
	case "pipeline_template":
		_, ok, err := s.db.GetPipelineTemplate(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), targetID)
		return ok, err
	}
	return false, nil
}

func (s *Server) handleScheduleTriggersList(ctx iris.Context) {
	targetID, _ := strconv.ParseInt(ctx.URLParam("targetId"), 10, 64)
	triggers, err := s.db.ListCronTriggers(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx),
		ctx.URLParam("targetType"), targetID, 0)
	if err != nil {
		serverError(ctx, "list schedule triggers failed", err)
		return
	}
	writeOK(ctx, map[string]any{"triggers": triggers})
}

func (s *Server) handleScheduleTriggerGet(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	t, ok, err := s.db.GetCronTrigger(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id)
	if err != nil {
		serverError(ctx, "get schedule trigger failed", err)
		return
	}
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	writeOK(ctx, t)
}

func (s *Server) handleScheduleTriggerCreate(ctx iris.Context) {
	var body scheduleTriggerBody
	if err := readJSON(ctx, &body); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateScheduleTriggerCron(&body); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	body.TargetType = strings.TrimSpace(body.TargetType)
	if !supportedTriggerTargets[body.TargetType] {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, fmt.Sprintf("unsupported targetType %q", body.TargetType))
		return
	}
	ok, err := s.validateTriggerTarget(ctx, body.TargetType, body.TargetID)
	if err != nil {
		serverError(ctx, "validate trigger target failed", err)
		return
	}
	if !ok {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "target not found or not owned by current user")
		return
	}
	id, err := s.db.CreateCronTrigger(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx),
		body.TargetType, body.TargetID, body.CronExpr, *body.Enabled)
	if err != nil {
		serverError(ctx, "create schedule trigger failed", err)
		return
	}
	writeOK(ctx, map[string]any{"id": id})
}

// handleScheduleTriggerUpdate 只改 cron 与启用状态；目标绑定创建后不可变。
func (s *Server) handleScheduleTriggerUpdate(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	var body scheduleTriggerBody
	if err := readJSON(ctx, &body); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if msg := validateScheduleTriggerCron(&body); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}
	if _, ok, err := s.db.GetCronTrigger(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id); err != nil {
		serverError(ctx, "get schedule trigger failed", err)
		return
	} else if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	if err := s.db.UpdateCronTrigger(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id,
		body.CronExpr, *body.Enabled); err != nil {
		serverError(ctx, "update schedule trigger failed", err)
		return
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}

func (s *Server) handleScheduleTriggerDelete(ctx iris.Context) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}
	if err := s.db.DeleteCronTrigger(ctx.Request().Context(), currentOrgID(ctx), currentUserID(ctx), id); err != nil {
		serverError(ctx, "delete schedule trigger failed", err)
		return
	}
	writeOK(ctx, map[string]any{"status": "ok"})
}
