package server

import (
	"context"
	"errors"
	"log/slog"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

// aiJobSaveError 定时任务保存失败的分类结果：
// status/code 供 HTTP 层写响应，msg 供 AI 工具路径转成 map{"error": msg}。
type aiJobSaveError struct {
	status int
	code   int
	msg    string
}

func (e *aiJobSaveError) Error() string { return e.msg }

func aiJobBadRequest(msg string) *aiJobSaveError {
	return &aiJobSaveError{status: iris.StatusBadRequest, code: CodeInvalidParam, msg: msg}
}

func aiJobNotFound(msg string) *aiJobSaveError {
	return &aiJobSaveError{status: iris.StatusNotFound, code: CodeNotFound, msg: msg}
}

func aiJobInternal(msg string) *aiJobSaveError {
	return &aiJobSaveError{status: iris.StatusInternalServerError, code: CodeInternalError, msg: msg}
}

// saveAIJob 创建/更新定时任务的唯一写入口（HTTP handler 与 AI 工具共用），jobID == 0 表示创建：
//   - 校验请求体（name/cron 必填、target 结构合法）
//   - 更新时校验任务归属（全局任务/他人任务不可改）
//   - target 归一化为 pipeline Definition
//   - preflight 非 nil 时做调度预检（fail fast，避免写入跑不起来的任务）。HTTP 写入路径传
//     validateAIJobTarget；AI 工具路径传 nil——工具执行器由包级 aiTools 表静态引用，
//     若在此静态引用 validateAIJobTarget 会形成 aiTools → execCreateAIJob → … → aiTools 的初始化环
//   - 落库并同步 cron trigger；创建时 trigger 写失败回滚 job 与 trigger
func (s *Server) saveAIJob(ctx context.Context, orgID, userID int64, body *aiJobBody, jobID int64,
	preflight func(db.AIJobWithOwner) error) (int64, error) {
	if msg := validateAIJobBody(body); msg != "" {
		return 0, aiJobBadRequest(msg)
	}

	if jobID > 0 {
		cur, err := s.db.GetAIJob(ctx, orgID, userID, jobID)
		if err != nil {
			slog.Error("get ai job failed", "org", orgID, "user", userID, "job", jobID, "err", err)
			return 0, aiJobInternal("get ai job failed")
		}
		if cur == nil {
			return 0, aiJobNotFound("job not found")
		}
	}

	spec := aiJobSpecFromBody(body)
	preview := aiJobPreview(spec, orgID, userID)
	if err := s.normalizeTargetToPipeline(ctx, &preview); err != nil {
		return 0, aiJobBadRequest("invalid target: " + err.Error())
	}
	spec.TargetType = preview.TargetType
	spec.TargetJSON = string(preview.Target)
	if preflight != nil {
		if err := preflight(preview); err != nil {
			return 0, aiJobBadRequest(err.Error())
		}
	}

	if jobID == 0 {
		id, err := s.db.CreateAIJobWithSpec(ctx, orgID, userID, spec)
		if err != nil {
			slog.Error("create ai job failed", "org", orgID, "user", userID, "err", err)
			return 0, aiJobInternal("create ai job failed")
		}
		if err := s.upsertAIJobTrigger(ctx, orgID, userID, id, body.CronExpr, *body.Enabled); err != nil {
			_ = s.deleteAIJobTriggers(ctx, orgID, userID, id)
			_ = s.db.DeleteAIJob(ctx, orgID, userID, id)
			slog.Error("create ai job trigger failed", "org", orgID, "user", userID, "job", id, "err", err)
			return 0, aiJobInternal("create ai job trigger failed")
		}
		return id, nil
	}

	if err := s.db.UpdateAIJobWithSpec(ctx, orgID, userID, jobID, spec); err != nil {
		slog.Error("update ai job failed", "org", orgID, "user", userID, "job", jobID, "err", err)
		return 0, aiJobInternal("update ai job failed")
	}
	if err := s.upsertAIJobTrigger(ctx, orgID, userID, jobID, body.CronExpr, *body.Enabled); err != nil {
		slog.Error("update ai job trigger failed", "org", orgID, "user", userID, "job", jobID, "err", err)
		return 0, aiJobInternal("update ai job trigger failed")
	}
	return jobID, nil
}

// writeAIJobSaveError 把 saveAIJob 的错误写成 HTTP 响应（内部错误已在 saveAIJob 记日志）
func writeAIJobSaveError(ctx iris.Context, err error) {
	var se *aiJobSaveError
	if !errors.As(err, &se) {
		serverError(ctx, "save ai job failed", err)
		return
	}
	writeFailMsg(ctx, se.status, se.code, se.msg)
}

// aiJobSaveErrMsg 把 saveAIJob 的错误转成 AI 工具结果里的 error 文本
func aiJobSaveErrMsg(err error) string {
	var se *aiJobSaveError
	if errors.As(err, &se) {
		return se.msg
	}
	return err.Error()
}
