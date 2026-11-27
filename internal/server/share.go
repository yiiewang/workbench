// Package server 分享管理 handler
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

// generateShareToken 生成 16 字节随机 hex token
func generateShareToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateShareID 生成 UUID-like ID
func generateShareID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// ============================================================
// 分享密码尝试速率限制（DB 持久化，跨重启/多实例生效）
// ============================================================

const sharePasswordMaxFailures = 5

// sharePasswordFailWindow 分享密码尝试失败计数窗口
const sharePasswordFailWindow = 5 * time.Minute

// sharePasswordFailureCount 返回当前失败次数；DB 查询失败时 fail-open 返回 0。
func (s *Server) sharePasswordFailureCount(ctx context.Context, key string) int {
	count, err := s.db.RateLimitCheck(ctx, key, sharePasswordFailWindow)
	if err != nil {
		slog.Error("share rate limit check failed, fail-open", "key", key, "err", err)
		return 0
	}
	return count
}

// recordSharePasswordFailure 记录一次失败
func (s *Server) recordSharePasswordFailure(ctx context.Context, key string) {
	if _, err := s.db.RateLimitRecord(ctx, key, sharePasswordFailWindow); err != nil {
		slog.Error("share rate limit record failed", "key", key, "err", err)
	}
}

// clearSharePasswordFailures 清除失败记录
func (s *Server) clearSharePasswordFailures(ctx context.Context, key string) {
	if err := s.db.RateLimitClear(ctx, key); err != nil {
		slog.Error("share rate limit clear failed", "key", key, "err", err)
	}
}

// ============================================================
// 有效时间窗（RFC3339 字符串，空 = 不限制）
// ============================================================

// shareWindow 解析后的时间窗；零值字段表示该侧不限制。
type shareWindow struct {
	effective time.Time // 生效时间（零值 = 立即生效）
	expires   time.Time // 过期时间（零值 = 永不过期）
}

// parseShareWindow 解析时间窗：创建时用于格式校验（msg 非空即非法），
// 访问时忽略 msg——历史脏数据按「该侧不限制」降级，不阻断已存在的分享。
func parseShareWindow(effectiveAt, expiresAt string) (shareWindow, string) {
	var w shareWindow
	if effectiveAt != "" {
		t, err := time.Parse(time.RFC3339, effectiveAt)
		if err != nil {
			return w, "Invalid effectiveAt format, use RFC3339"
		}
		w.effective = t
	}
	if expiresAt != "" {
		t, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return w, "Invalid expiresAt format, use RFC3339"
		}
		w.expires = t
	}
	return w, ""
}

// ============================================================
// POST /api/share — 创建分享（需登录）
// ============================================================

func (s *Server) handleCreateShare(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	if orgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}

	var req struct {
		ResourcePath   string `json:"resourcePath"`
		ResourceType   string `json:"resourceType"`
		MaxAccessCount int    `json:"maxAccessCount"`
		Password       string `json:"password"`
		Remark         string `json:"remark"`
		EffectiveAt    string `json:"effectiveAt"`
		ExpiresAt      string `json:"expiresAt"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}

	resourcePath := strings.TrimSpace(req.ResourcePath)
	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}
	if strings.Contains(resourcePath, "..") {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}

	fsPath, ok := s.resolveUserPath(ctx, resourcePath)
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	// 强制以实际资源类型为准（忽略前端传入的 resourceType）
	resourceType := "file"
	if info.IsDir() {
		resourceType = "dir"
	}

	if _, msg := parseShareWindow(req.EffectiveAt, req.ExpiresAt); msg != "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, msg)
		return
	}

	passwordHash := ""
	if req.Password != "" {
		passwordHash, err = HashPassword(req.Password)
		if err != nil {
			serverError(ctx, "hash share password failed", err)
			return
		}
	}

	token, err := generateShareToken()
	if err != nil {
		serverError(ctx, "generate share token failed", err)
		return
	}

	shareID, err := generateShareID()
	if err != nil {
		serverError(ctx, "generate share id failed", err)
		return
	}

	share := &db.Share{
		ID:             shareID,
		Token:          token,
		OwnerUserID:    userID,
		OwnerOrgID:     orgID,
		ResourcePath:   resourcePath,
		ResourceType:   resourceType,
		MaxAccessCount: req.MaxAccessCount,
		PasswordHash:   passwordHash,
		Remark:         strings.TrimSpace(req.Remark),
		EffectiveAt:    req.EffectiveAt,
		ExpiresAt:      req.ExpiresAt,
	}

	if err := s.db.CreateShare(rctx, share); err != nil {
		serverError(ctx, "create share failed", err)
		return
	}

	shareURL := fmt.Sprintf("%s/s/%s", s.baseURL(ctx), token)
	writeOK(ctx, shareCreateData{
		ID:             share.ID,
		Token:          share.Token,
		URL:            shareURL,
		ResourcePath:   share.ResourcePath,
		ResourceType:   share.ResourceType,
		MaxAccessCount: share.MaxAccessCount,
		HasPassword:    passwordHash != "",
		Remark:         share.Remark,
		EffectiveAt:    share.EffectiveAt,
		ExpiresAt:      share.ExpiresAt,
	})
}

// baseURL 从请求中提取 origin
func (s *Server) baseURL(ctx iris.Context) string {
	scheme := "http"
	if ctx.Request().TLS != nil {
		scheme = "https"
	}
	host := ctx.Host()
	return fmt.Sprintf("%s://%s", scheme, host)
}

// ============================================================
// GET /api/share — 我的分享列表（需登录）
// ============================================================

func (s *Server) handleListShares(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	if orgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}

	shares, err := s.db.ListSharesByOwner(rctx, orgID, userID)
	if err != nil {
		serverError(ctx, "list shares failed", err, "user", userID)
		return
	}
	if shares == nil {
		shares = []db.Share{}
	}
	writeOK(ctx, sharesData{Shares: shares})
}

// ============================================================
// DELETE /api/share/{id} — 撤销分享（需登录）
// ============================================================

func (s *Server) handleDeleteShare(ctx iris.Context) {
	rctx := ctx.Request().Context()
	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	if orgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}

	shareID := ctx.Params().Get("id")
	if shareID == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Share id required")
		return
	}

	if err := s.db.DeleteShare(rctx, orgID, userID, shareID); err != nil {
		slog.Error("delete share failed", "id", shareID, "err", err)
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	writeOK(ctx, nil)
}

// ============================================================
// GET /s/{token} — 返回 share.html 预览页面
// ============================================================

func (s *Server) handleShareAccess(ctx iris.Context) {
	token := ctx.Params().Get("token")
	if token == "" {
		ctx.NotFound()
		return
	}

	share, err := s.db.GetShareByToken(ctx.Request().Context(), token)
	if err != nil {
		slog.Error("get share by token failed", "err", err)
		ctx.NotFound()
		return
	}
	if share == nil {
		ctx.NotFound()
		return
	}

	// SPA 迁移后 index.html 是 embed 资源（frontend/dist），不再从 static_dir 磁盘读取
	s.serveUIAsset(ctx, "index.html")
}

// ============================================================
// GET /api/share/{token} — 公开 API 返回分享数据
// ============================================================

func (s *Server) handleShareData(ctx iris.Context) {
	rctx := ctx.Request().Context()
	token := ctx.Params().Get("token")
	if token == "" {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Token required")
		return
	}

	share, err := s.db.GetShareByToken(rctx, token)
	if err != nil {
		serverError(ctx, "get share by token failed", err)
		return
	}
	if share == nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	// 校验时间窗口（解析失败的历史数据按该侧不限制处理）
	now := time.Now()
	win, _ := parseShareWindow(share.EffectiveAt, share.ExpiresAt)
	if !win.effective.IsZero() && now.Before(win.effective) {
		writeFailMsg(ctx, iris.StatusForbidden, CodeShareNotEffective, "This share is not yet effective")
		return
	}
	if !win.expires.IsZero() && now.After(win.expires) {
		writeFailMsg(ctx, iris.StatusGone, CodeShareExpired, "This share has expired")
		return
	}

	if share.MaxAccessCount > 0 && share.AccessCount >= share.MaxAccessCount {
		writeFailMsg(ctx, iris.StatusGone, CodeShareLimitReached, "Access limit reached")
		return
	}

	if share.PasswordHash != "" {
		password := ""
		if ctx.Method() == "POST" {
			var body struct {
				Password string `json:"password"`
			}
			if err := readJSON(ctx, &body); err == nil {
				password = body.Password
			}
		}
		if password == "" {
			password = ctx.GetHeader("X-Share-Password")
		}

		if password == "" {
			writeFailMsg(ctx, iris.StatusUnauthorized, CodePasswordRequired, "This share requires a password")
			return
		}

		ip := clientIP(ctx)
		rateKey := "share:" + token + ":" + ip
		if s.sharePasswordFailureCount(rctx, rateKey) >= sharePasswordMaxFailures {
			writeFailMsg(ctx, iris.StatusTooManyRequests, CodeTooManyRequests, "Too many password attempts, try later")
			return
		}

		if !VerifyPassword(share.PasswordHash, password) {
			s.recordSharePasswordFailure(rctx, rateKey)
			writeFail(ctx, iris.StatusUnauthorized, CodeInvalidSharePwd)
			return
		}
		s.clearSharePasswordFailures(rctx, rateKey)
	}

	_, limitReached, err := s.db.IncrementShareAccessCount(rctx, token)
	if err != nil {
		serverError(ctx, "increment share access count failed", err, "token", token)
		return
	}
	if limitReached {
		writeFailMsg(ctx, iris.StatusGone, CodeShareLimitReached, "Access limit reached")
		return
	}

	subPath := ctx.URLParam("path")
	resourcePath := share.ResourcePath
	// shareRoot 是分享资源本身的绝对路径，也是子路径浏览的沙箱边界
	shareRoot, ok := s.resolveUserPathByOwner(rctx, share.OwnerOrgID, share.OwnerUserID, resourcePath)
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	fsPath := shareRoot

	if subPath != "" {
		// 子路径可能相对分享根（/a/b），也可能带完整用户路径（/docs/a/b），先剥掉分享根前缀
		rel := strings.TrimPrefix(filepath.Clean("/"+subPath), "/")
		if prefix := strings.Trim(strings.TrimSuffix(share.ResourcePath, "/"), "/"); prefix != "" {
			if rel == prefix {
				rel = ""
			} else if strings.HasPrefix(rel, prefix+"/") {
				rel = strings.TrimPrefix(rel, prefix)
			}
		}
		joined, cleanRel, ok := safeJoin(shareRoot, rel)
		if !ok {
			writeFail(ctx, iris.StatusNotFound, CodeNotFound)
			return
		}
		fsPath = joined
		resourcePath = strings.TrimSuffix(share.ResourcePath, "/") + cleanRel
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	resp := shareAccessData{
		ResourcePath:   resourcePath,
		ResourceType:   share.ResourceType,
		AccessCount:    share.AccessCount + 1,
		MaxAccessCount: share.MaxAccessCount,
		ExpiresAt:      share.ExpiresAt,
		EffectiveAt:    share.EffectiveAt,
		HasPassword:    share.PasswordHash != "",
		Remark:         share.Remark,
	}

	if info.IsDir() {
		dirs, files, err := s.listTreeItems(fsPath)
		if err != nil {
			writeFail(ctx, iris.StatusForbidden, CodeForbidden)
			return
		}

		resp.Path = resourcePath
		resp.Dirs = dirs
		resp.Files = files
		resp.IsDir = true
		resp.CurrentPath = resourcePath
		relPath := ""
		if rel, err := filepath.Rel(shareRoot, fsPath); err == nil && rel != "." {
			relPath = filepath.ToSlash(rel)
		}
		resp.RelPath = relPath
		writeOK(ctx, resp)
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	content, err := os.ReadFile(fsPath)
	if err != nil {
		serverError(ctx, "read share file failed", err)
		return
	}

	resp.IsDir = false
	resp.FileName = filepath.Base(fsPath)
	resp.Ext = ext
	resp.Size = info.Size()
	resp.ContentType = contentType(ext)
	resp.Content = base64.StdEncoding.EncodeToString(content)
	resp.IsBinary = isBinaryExt(ext)
	writeOK(ctx, resp)
}
