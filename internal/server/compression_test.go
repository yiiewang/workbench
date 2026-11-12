package server

// 响应压缩中间件行为测试：
//  1. 带 Accept-Encoding: gzip 的 API 请求 → 响应 Content-Encoding: gzip；
//  2. 不带 Accept-Encoding → 不压缩（iris 内部回退原 writer）；
//  3. 流式接口（/api/ai/chat）跳过压缩，保持 SSE 透传。

import (
	"net/http"
	"testing"
)

func TestCompressionMiddleware(t *testing.T) {
	e, _, _, _ := setupTestServer(t)

	// 静态文件（embed 风格）：写入后经 handleStatic 服务
	tok := "" // /api/tasks 未登录 401 也走压缩中间件（信封 JSON），足够验证

	// 1. 声明 gzip → 响应压缩
	e.GET("/api/tasks").
		WithHeader("Authorization", "Bearer "+tok).
		WithHeader("Accept-Encoding", "gzip, deflate, br").
		Expect().
		Status(http.StatusUnauthorized).
		Header("Content-Encoding").
		IsEqual("gzip")

	// 2. 不声明 Accept-Encoding → 不压缩
	resp := e.GET("/api/tasks").
		WithHeader("Authorization", "Bearer "+tok).
		Expect().
		Status(http.StatusUnauthorized).
		Raw()
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("unexpected Content-Encoding %q without Accept-Encoding", ce)
	}

	// 3. SSE 接口（AI chat）跳过压缩：auth 先行，空 token 401 信封也不应带 Content-Encoding
	resp = e.POST("/api/ai/chat").
		WithHeader("Accept-Encoding", "gzip").
		WithJSON(map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}).
		Expect().
		Status(http.StatusUnauthorized).
		Raw()
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("sse endpoint must skip compression, got Content-Encoding %q", ce)
	}
}
