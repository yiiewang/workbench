package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
)

// compressionSkipPrefixes 排除压缩的流式接口：
//   - /api/media：http.ServeContent 的 Range 语义与压缩不兼容
//     （客户端按未压缩字节区间请求，压缩后区间错位，视频拖动直接失效）
//   - /api/ai/chat：SSE 逐行透传（每行 flush 的小增量在压缩层下无收益，
//     徒增大量小 gzip 块与解压开销）
func compressionSkipPrefixes() []string {
	return []string{"/api/media", "/api/ai/chat"}
}

func (s *Server) compressionMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		p := ctx.Path()
		for _, prefix := range compressionSkipPrefixes() {
			if strings.HasPrefix(p, prefix) {
				ctx.Next()
				return
			}
		}
		// 客户端未声明 Accept-Encoding 时 iris 内部自动回退原 writer（静默失败）
		_ = ctx.CompressWriter(true)
		ctx.Next()
	}
}

func (s *Server) loggingMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		// /api/media、/api/ai/chat 必须跳过 Record：ResponseRecorder 会把整个响应体攒在
		// 内存里、请求结束才 flush，那样就不是流式了（大视频/SSE 增量直接失效）。
		if ctx.Path() != "/api/media" && ctx.Path() != "/api/ai/chat" {
			ctx.Record()
		}
		ctx.Next()

		statusCode := ctx.GetStatusCode()

		ip, _, _ := net.SplitHostPort(ctx.RemoteAddr())
		if ip == "" {
			ip = ctx.RemoteAddr()
		}
		ua := ctx.GetHeader("User-Agent")
		if len(ua) > 50 {
			ua = ua[:50]
		}
		visitorID := fmt.Sprintf("%s|%s", ip, ua)

		timestamp := time.Now().Format("2006-01-02 15:04:05")
		writeAccessLog(s.logFile, timestamp, visitorID, ctx.Path(), fmt.Sprintf("%d", statusCode))
	}
}

func writeAccessLog(logPath, timestamp, visitorID, path, status string) {
	line := fmt.Sprintf("%s | %s | %s | %s\n", timestamp, visitorID, path, status)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func (s *Server) securityHeadersMiddleware() iris.Handler {
	return func(ctx iris.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "SAMEORIGIN")
		ctx.Header("Referrer-Policy", "no-referrer")

		// CORS for sandboxed iframe（null origin）— 仅 API 路径
		// 预览窗口 iframe 使用 sandbox（无 allow-same-origin），origin 为 null
		// 不设置 Allow-Credentials，因为使用 Bearer token 而非 cookie
		path := ctx.Path()
		if strings.HasPrefix(path, "/api/") {
			ctx.Header("Access-Control-Allow-Origin", "null")
			ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			ctx.Header("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			if ctx.Method() == "OPTIONS" {
				ctx.StatusCode(204)
				return
			}
		}

		ctx.Next()
	}
}

// ============================================================
// 登录限流：每 IP 每分钟最多 loginMaxFailures 次失败（DB 持久化，跨重启/多实例生效）
// ============================================================

const (
	loginMaxFailures = 5
	loginWindow      = time.Minute
)

// loginFailureCount 返回 IP 在窗口内的失败次数；DB 查询失败时 fail-open 返回 0。
func (s *Server) loginFailureCount(ctx context.Context, ip string) int {
	count, err := s.db.RateLimitCheck(ctx, "login:"+ip, loginWindow)
	if err != nil {
		slog.Error("rate limit check failed, fail-open", "key", "login:"+ip, "err", err)
		return 0
	}
	return count
}

// recordLoginFailure 记录一次登录失败。
func (s *Server) recordLoginFailure(ctx context.Context, ip string) {
	if _, err := s.db.RateLimitRecord(ctx, "login:"+ip, loginWindow); err != nil {
		slog.Error("rate limit record failed", "key", "login:"+ip, "err", err)
	}
}

// clearLoginFailures 登录成功后清除 IP 的失败记录。
func (s *Server) clearLoginFailures(ctx context.Context, ip string) {
	if err := s.db.RateLimitClear(ctx, "login:"+ip); err != nil {
		slog.Error("rate limit clear failed", "key", "login:"+ip, "err", err)
	}
}

// clientIP 从 ctx.RemoteAddr 提取客户端 IP
func clientIP(ctx iris.Context) string {
	ip, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		return ctx.RemoteAddr()
	}
	return ip
}
