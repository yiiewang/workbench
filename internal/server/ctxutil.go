package server

import (
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
)

// pathID 解析 :id 路径参数为整数主键。
// 解析失败或非正数时写入 400 响应（CodeInvalidParam）并返回 false。
func pathID(ctx iris.Context) (int64, bool) {
	id, err := strconv.ParseInt(ctx.Params().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return 0, false
	}
	return id, true
}

// queryLimit 读取 ?limit= 查询参数，缺省或非法时返回 0（默认值由 db 层决定）。
func queryLimit(ctx iris.Context) int {
	limit, _ := strconv.Atoi(ctx.URLParam("limit"))
	return limit
}

// isUniqueViolation 判断错误是否为 SQLite 唯一约束冲突，
// 用于把「用户名/手机号重复」映射为对应的业务错误码。
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
