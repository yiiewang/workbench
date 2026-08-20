// Package server 统一响应信封与载荷结构
package server

import (
	"log/slog"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

// Response 标准响应信封
type Response struct {
	Code int    `json:"code"`           // 0=成功，非 0=业务错误码
	Msg  string `json:"msg"`            // ok / 错误描述
	Data any    `json:"data,omitempty"` // 成功时的载荷，错误时为 nil
}

// writeOK 写入成功响应（HTTP 200，code=0，data 为载荷）
func writeOK(ctx iris.Context, data any) {
	writeJSON(ctx, iris.StatusOK, Response{Code: CodeOK, Msg: errMsg(CodeOK), Data: data})
}

// writeFail 写入错误响应，msg 取错误字典
func writeFail(ctx iris.Context, httpStatus, code int) {
	writeJSON(ctx, httpStatus, Response{Code: code, Msg: errMsg(code)})
}

// writeFailMsg 写入错误响应，msg 覆盖字典默认值（用于动态参数提示）
func writeFailMsg(ctx iris.Context, httpStatus, code int, msg string) {
	writeJSON(ctx, httpStatus, Response{Code: code, Msg: msg})
}

// serverError 记录操作失败日志并写入 500 业务错误响应。
// args 为附加结构化字段（成对 key/value），err 自动作为 "err" 字段。
// 用于收敛 handler 中重复的 "slog.Error + writeFail(500) + return" 样板。
func serverError(ctx iris.Context, msg string, err error, args ...any) {
	args = append(args, "err", err)
	slog.Error(msg, args...)
	writeFail(ctx, iris.StatusInternalServerError, CodeInternalError)
}

// ============================================================
// 响应载荷结构
// ============================================================

// userBrief 用户简要信息（登录/设置密码响应内嵌）
type userBrief struct {
	UserID   int64  `json:"userId"`
	OrgID    int64  `json:"orgId"`
	UserName string `json:"userName"`
	OrgName  string `json:"orgName"`
	Role     string `json:"role"`
}

// loginData 登录/设置密码成功载荷
type loginData struct {
	Token string    `json:"token"`
	User  userBrief `json:"user"`
}

// meData /api/me 载荷
type meData struct {
	UserID   int64  `json:"userId"`
	OrgID    int64  `json:"orgId"`
	UserName string `json:"userName"`
	OrgName  string `json:"orgName"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"`
}

// usersData GET /api/admin/users 载荷
type usersData struct {
	Users []db.UserInfo `json:"users"`
}

// rolesData GET /api/admin/roles 载荷
type rolesData struct {
	Roles []db.Role `json:"roles"`
}

// membersData /api/org-members 载荷
type membersData struct {
	Members []db.Member `json:"members"`
}

// treeData /api/tree 载荷（与分享目录视图共用）
type treeData struct {
	Path  string     `json:"path"`
	Dirs  []treeItem `json:"dirs"`
	Files []treeItem `json:"files"`
}

// tasksOKData PUT /api/tasks 成功载荷
type tasksOKData struct {
	Status string `json:"status"`
	File   string `json:"file"`
}

// sharesData 分享列表载荷
type sharesData struct {
	Shares []db.Share `json:"shares"`
}

// shareCreateData 创建分享成功载荷
type shareCreateData struct {
	ID             string `json:"id"`
	Token          string `json:"token"`
	URL            string `json:"url"`
	ResourcePath   string `json:"resourcePath"`
	ResourceType   string `json:"resourceType"`
	MaxAccessCount int    `json:"maxAccessCount"`
	HasPassword    bool   `json:"hasPassword"`
	Remark         string `json:"remark"`
	EffectiveAt    string `json:"effectiveAt"`
	ExpiresAt      string `json:"expiresAt"`
}

// shareAccessData 分享访问载荷（目录/文件两态，omitempty 区分）
type shareAccessData struct {
	// 共有字段
	ResourcePath   string `json:"resourcePath"`
	ResourceType   string `json:"resourceType"`
	AccessCount    int    `json:"accessCount"`
	MaxAccessCount int    `json:"maxAccessCount"`
	ExpiresAt      string `json:"expiresAt"`
	EffectiveAt    string `json:"effectiveAt"`
	HasPassword    bool   `json:"hasPassword"`
	Remark         string `json:"remark"`
	IsDir          bool   `json:"isDir"`
	// 目录字段
	Path        string     `json:"path,omitempty"`
	CurrentPath string     `json:"currentPath,omitempty"`
	RelPath     string     `json:"relPath,omitempty"`
	Dirs        []treeItem `json:"dirs,omitempty"`
	Files       []treeItem `json:"files,omitempty"`
	// 文件字段
	FileName    string `json:"fileName,omitempty"`
	Ext         string `json:"ext,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Content     string `json:"content,omitempty"`
	IsBinary    bool   `json:"isBinary,omitempty"`
}
