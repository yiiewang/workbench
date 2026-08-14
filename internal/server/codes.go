// Package server 业务状态码与错误字典
package server

// 业务状态码：0 表示成功，非 0 表示错误。
// 编码规则：HTTP 状态码 * 100 + 序号，便于从码值反推 HTTP 类别。
const (
	CodeOK = 0

	// 4xx 客户端错误
	CodeInvalidJSON        = 40001 // 请求体 JSON 非法
	CodeInvalidParam       = 40002 // 参数缺失或非法（动态消息）
	CodeInvalidPath        = 40003 // 路径非法或越界
	CodeInvalidTasksJSON   = 40004 // /api/tasks 结构非法
	CodeUnauthorized       = 40101 // 需要登录
	CodeMissingToken       = 40102 // 缺少 token
	CodeInvalidToken       = 40103 // token 无效或过期
	CodeInvalidPassword    = 40104 // 登录密码错误
	CodeInvalidOldPassword = 40105 // 修改密码时旧密码错误
	CodePasswordNotSet     = 40301 // 用户尚未设置密码
	CodeUserNotFound       = 40302 // 用户不存在
	CodeForbidden          = 40303 // 无权操作（如组织不匹配）
	CodePasswordRequired   = 40304 // 分享需要密码
	CodeInvalidSharePwd    = 40305 // 分享密码错误
	CodeShareNotEffective  = 40306 // 分享尚未生效
	CodeNotFound           = 40401 // 资源不存在
	CodeShareExpired       = 41001 // 分享已过期
	CodeShareLimitReached  = 41002 // 分享访问次数已达上限
	CodeTooManyRequests    = 42901 // 请求过于频繁

	// 5xx 服务端错误
	CodeInternalError = 50000 // 内部错误
)

// errMsgMap 错误字典：业务码 → 默认消息
var errMsgMap = map[int]string{
	CodeOK:                 "ok",
	CodeInvalidJSON:        "Invalid JSON",
	CodeInvalidParam:       "Invalid parameter",
	CodeInvalidPath:        "Invalid path",
	CodeInvalidTasksJSON:   "Invalid tasks JSON",
	CodeUnauthorized:       "Authentication required",
	CodeMissingToken:       "Missing token",
	CodeInvalidToken:       "Invalid or expired token",
	CodeInvalidPassword:    "Invalid password",
	CodeInvalidOldPassword: "Invalid old password",
	CodePasswordNotSet:     "Password not set",
	CodeUserNotFound:       "User does not exist",
	CodeForbidden:          "Forbidden",
	CodePasswordRequired:   "Password required",
	CodeInvalidSharePwd:    "Invalid password",
	CodeShareNotEffective:  "Share not effective yet",
	CodeNotFound:           "Not found",
	CodeShareExpired:       "Share expired",
	CodeShareLimitReached:  "Access limit reached",
	CodeTooManyRequests:    "Too many failed attempts, try later",
	CodeInternalError:      "Internal server error",
}

// errMsg 返回业务码对应的默认消息，未注册时返回兜底消息
func errMsg(code int) string {
	if m, ok := errMsgMap[code]; ok {
		return m
	}
	return "Unknown error"
}
