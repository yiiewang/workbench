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
	"sort"
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

	if req.EffectiveAt != "" {
		if _, err := time.Parse(time.RFC3339, req.EffectiveAt); err != nil {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Invalid effectiveAt format, use RFC3339")
			return
		}
	}
	if req.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, req.ExpiresAt); err != nil {
			writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Invalid expiresAt format, use RFC3339")
			return
		}
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

	// 校验时间窗口
	now := time.Now()
	if share.EffectiveAt != "" {
		effective, err := time.Parse(time.RFC3339, share.EffectiveAt)
		if err == nil && now.Before(effective) {
			writeFailMsg(ctx, iris.StatusForbidden, CodeShareNotEffective, "This share is not yet effective")
			return
		}
	}
	if share.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, share.ExpiresAt)
		if err == nil && now.After(expires) {
			writeFailMsg(ctx, iris.StatusGone, CodeShareExpired, "This share has expired")
			return
		}
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
	fsPath, ok := s.resolveUserPathByOwner(rctx, share.OwnerOrgID, share.OwnerUserID, resourcePath)
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	if subPath != "" {
		cleaned := filepath.Clean("/" + subPath)
		sharePathPrefix := strings.TrimSuffix(share.ResourcePath, "/")
		if strings.HasPrefix(cleaned, sharePathPrefix+"/") {
			cleaned = strings.TrimPrefix(cleaned, sharePathPrefix)
		}
		fsPath = filepath.Join(fsPath, cleaned)
		shareRoot, _ := s.resolveUserPathByOwner(rctx, share.OwnerOrgID, share.OwnerUserID, share.ResourcePath)
		absBase, _ := filepath.Abs(shareRoot)
		absFull, _ := filepath.Abs(fsPath)
		if !strings.HasPrefix(absFull, absBase+string(filepath.Separator)) && absFull != absBase {
			writeFail(ctx, iris.StatusNotFound, CodeNotFound)
			return
		}
		resourcePath = share.ResourcePath + "/" + strings.TrimPrefix(cleaned, "/")
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
		entries, err := os.ReadDir(fsPath)
		if err != nil {
			writeFail(ctx, iris.StatusForbidden, CodeForbidden)
			return
		}

		var dirs, files []treeItem
		for _, e := range entries {
			if s.isHidden(e.Name()) {
				continue
			}
			isDir := e.IsDir()
			if !isDir && e.Type()&os.ModeSymlink != 0 {
				if info, err := os.Stat(filepath.Join(fsPath, e.Name())); err == nil {
					isDir = info.IsDir()
				}
			}
			if isDir {
				dirs = append(dirs, treeItem{Name: e.Name(), IsDir: true})
			} else {
				fi, _ := e.Info()
				size := int64(0)
				if fi != nil {
					size = fi.Size()
				}
				files = append(files, treeItem{Name: e.Name(), IsDir: false, Size: size})
			}
		}
		sort.Slice(dirs, func(i, j int) bool { return naturalLess(dirs[i].Name, dirs[j].Name) })
		sort.Slice(files, func(i, j int) bool { return naturalLess(files[i].Name, files[j].Name) })

		resp.Path = resourcePath
		resp.Dirs = dirs
		resp.Files = files
		resp.IsDir = true
		resp.CurrentPath = resourcePath
		relPath := ""
		if shareRoot, _ := s.resolveUserPathByOwner(rctx, share.OwnerOrgID, share.OwnerUserID, share.ResourcePath); shareRoot != "" {
			if rel, err := filepath.Rel(shareRoot, fsPath); err == nil && rel != "." {
				relPath = filepath.ToSlash(rel)
			}
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
