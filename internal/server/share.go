// Package server 分享管理 handler
package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
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
func generateShareID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ============================================================
// 密码尝试速率限制（内存计数器，每 IP+token 维度）
// ============================================================

const sharePasswordMaxFailures = 5

type rateLimitEntry struct {
	count     int
	expiresAt time.Time
}

var shareRateLimits = make(map[string]*rateLimitEntry)

// sharePasswordFailureCount 返回当前失败次数
func sharePasswordFailureCount(key string) int {
	if entry, ok := shareRateLimits[key]; ok && time.Now().Before(entry.expiresAt) {
		return entry.count
	}
	return 0
}

// recordSharePasswordFailure 记录一次失败
func recordSharePasswordFailure(key string) {
	entry, ok := shareRateLimits[key]
	if !ok || !time.Now().Before(entry.expiresAt) {
		entry = &rateLimitEntry{expiresAt: time.Now().Add(5 * time.Minute)}
		shareRateLimits[key] = entry
	}
	entry.count++
}

// clearSharePasswordFailures 清除失败记录
func clearSharePasswordFailures(key string) {
	delete(shareRateLimits, key)
}

// ============================================================
// POST /api/share — 创建分享（需登录）
// ============================================================

func (s *Server) handleCreateShare(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID, err := s.db.FindUserOrg(userID)
	if err != nil || orgID == "" {
		writeJSON(ctx, iris.StatusForbidden, map[string]string{"error": "User org not found"})
		return
	}

	var req struct {
		ResourcePath   string `json:"resourcePath"`
		ResourceType   string `json:"resourceType"`
		MaxAccessCount int    `json:"maxAccessCount"`
		Password       string `json:"password"`
		EffectiveAt    string `json:"effectiveAt"`
		ExpiresAt      string `json:"expiresAt"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	resourcePath := strings.TrimSpace(req.ResourcePath)
	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}
	if strings.Contains(resourcePath, "..") {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid path"})
		return
	}

	fsPath, ok := s.resolveStaticPath(resourcePath)
	if !ok {
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Resource not found"})
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil {
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Resource not found"})
		return
	}

	// 强制以实际资源类型为准（忽略前端传入的 resourceType）
	// 防止前后端不一致：例如前端误传 'file' 但实际是目录
	resourceType := "file"
	if info.IsDir() {
		resourceType = "dir"
	}

	if req.EffectiveAt != "" {
		if _, err := time.Parse(time.RFC3339, req.EffectiveAt); err != nil {
			writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid effectiveAt format, use RFC3339"})
			return
		}
	}
	if req.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, req.ExpiresAt); err != nil {
			writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Invalid expiresAt format, use RFC3339"})
			return
		}
	}

	passwordHash := ""
	if req.Password != "" {
		passwordHash, err = HashPassword(req.Password)
		if err != nil {
			log.Printf("hash share password failed, err=%v", err)
			writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
			return
		}
	}

	token, err := generateShareToken()
	if err != nil {
		log.Printf("generate share token failed, err=%v", err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	share := &db.Share{
		ID:             generateShareID(),
		Token:          token,
		OwnerUserID:    userID,
		OwnerOrgID:     orgID,
		ResourcePath:   resourcePath,
		ResourceType:   resourceType,
		MaxAccessCount: req.MaxAccessCount,
		PasswordHash:   passwordHash,
		EffectiveAt:    req.EffectiveAt,
		ExpiresAt:      req.ExpiresAt,
	}

	if err := s.db.CreateShare(share); err != nil {
		log.Printf("create share failed, err=%v", err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	shareURL := fmt.Sprintf("%s/s/%s", s.baseURL(ctx), token)
	writeJSON(ctx, iris.StatusOK, map[string]any{
		"id":             share.ID,
		"token":          share.Token,
		"url":            shareURL,
		"resourcePath":   share.ResourcePath,
		"resourceType":   share.ResourceType,
		"maxAccessCount": share.MaxAccessCount,
		"hasPassword":    passwordHash != "",
		"effectiveAt":    share.EffectiveAt,
		"expiresAt":      share.ExpiresAt,
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
	userID := currentUserID(ctx)
	orgID, err := s.db.FindUserOrg(userID)
	if err != nil || orgID == "" {
		writeJSON(ctx, iris.StatusForbidden, map[string]string{"error": "User org not found"})
		return
	}

	shares, err := s.db.ListSharesByOwner(orgID, userID)
	if err != nil {
		log.Printf("list shares failed, user=%s, err=%v", userID, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if shares == nil {
		shares = []db.Share{}
	}
	writeJSON(ctx, iris.StatusOK, map[string]any{"shares": shares})
}

// ============================================================
// DELETE /api/share/{id} — 撤销分享（需登录）
// ============================================================

func (s *Server) handleDeleteShare(ctx iris.Context) {
	userID := currentUserID(ctx)
	orgID, err := s.db.FindUserOrg(userID)
	if err != nil || orgID == "" {
		writeJSON(ctx, iris.StatusForbidden, map[string]string{"error": "User org not found"})
		return
	}

	shareID := ctx.Params().Get("id")
	if shareID == "" {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Share id required"})
		return
	}

	if err := s.db.DeleteShare(orgID, userID, shareID); err != nil {
		log.Printf("delete share failed, id=%s, err=%v", shareID, err)
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Share not found or no permission"})
		return
	}
	writeJSON(ctx, iris.StatusOK, map[string]string{"status": "ok"})
}

// ============================================================
// GET /s/{token} — 返回 share.html 预览页面
// GET /s/{token}/{p:path} — 返回 share.html（文件夹子路径也走前端路由）
// ============================================================

func (s *Server) handleShareAccess(ctx iris.Context) {
	token := ctx.Params().Get("token")
	if token == "" {
		ctx.NotFound()
		return
	}

	// 检查 share 是否存在（不做权限校验，仅用于页面标题）
	share, err := s.db.GetShareByToken(token)
	if err != nil {
		log.Printf("get share by token failed, err=%v", err)
		ctx.NotFound()
		return
	}
	if share == nil {
		ctx.NotFound()
		return
	}

	// 返回 index.html（前端检测 /s/{token} 路径自动进入分享视图）
	indexHTML := filepath.Join(s.serDirAbs, "index.html")
	if _, err := os.Stat(indexHTML); err != nil {
		ctx.NotFound()
		return
	}
	ctx.ServeFile(indexHTML)
}

// ============================================================
// GET /api/share/{token} — 公开 API 返回分享数据
// Header: X-Share-Password (可选), query: path (文件夹子路径，可选)
// 或 POST body: {"password":"xxx"} 用于密码提交
// ============================================================

func (s *Server) handleShareData(ctx iris.Context) {
	token := ctx.Params().Get("token")
	if token == "" {
		writeJSON(ctx, iris.StatusBadRequest, map[string]string{"error": "Token required"})
		return
	}

	share, err := s.db.GetShareByToken(token)
	if err != nil {
		log.Printf("get share by token failed, err=%v", err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if share == nil {
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Share not found"})
		return
	}

	// 校验时间窗口
	now := time.Now()
	if share.EffectiveAt != "" {
		effective, err := time.Parse(time.RFC3339, share.EffectiveAt)
		if err == nil && now.Before(effective) {
			writeJSON(ctx, iris.StatusForbidden, map[string]string{
				"error":   "not_effective_yet",
				"message": "This share is not yet effective",
			})
			return
		}
	}
	if share.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, share.ExpiresAt)
		if err == nil && now.After(expires) {
			writeJSON(ctx, iris.StatusGone, map[string]string{
				"error":   "expired",
				"message": "This share has expired",
			})
			return
		}
	}

	// 校验访问次数
	if share.MaxAccessCount > 0 && share.AccessCount >= share.MaxAccessCount {
		writeJSON(ctx, iris.StatusGone, map[string]string{
			"error":   "limit_reached",
			"message": "Access limit reached",
		})
		return
	}

	// 校验密码（密码通过 Header 或 POST body 传递，不通过 URL query param）
	if share.PasswordHash != "" {
		// 优先从 POST body 读取
		password := ""
		if ctx.Method() == "POST" {
			var body struct {
				Password string `json:"password"`
			}
			if err := readJSON(ctx, &body); err == nil {
				password = body.Password
			}
		}
		// 其次从 Header 读取
		if password == "" {
			password = ctx.GetHeader("X-Share-Password")
		}

		if password == "" {
			// 返回需要密码的提示（不泄露 resourcePath）
			writeJSON(ctx, iris.StatusUnauthorized, map[string]string{
				"error":       "password_required",
				"message":     "This share requires a password",
				"hasPassword": "true",
			})
			return
		}

		// 密码尝试速率限制（每 token 每 5 分钟最多 5 次失败）
		ip := clientIP(ctx)
		rateKey := "share:" + token + ":" + ip
		if sharePasswordFailureCount(rateKey) >= sharePasswordMaxFailures {
			writeJSON(ctx, iris.StatusTooManyRequests, map[string]string{
				"error":   "too_many_attempts",
				"message": "Too many password attempts, try later",
			})
			return
		}

		if !VerifyPassword(share.PasswordHash, password) {
			recordSharePasswordFailure(rateKey)
			writeJSON(ctx, iris.StatusUnauthorized, map[string]string{
				"error":   "invalid_password",
				"message": "Invalid password",
			})
			return
		}
		// 密码正确，清除失败记录
		clearSharePasswordFailures(rateKey)
	}

	// 增加访问计数（密码校验通过后才增加，防止暴力尝试消耗次数）
	_, limitReached, err := s.db.IncrementShareAccessCount(token)
	if err != nil {
		log.Printf("increment share access count failed, token=%s, err=%v", token, err)
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if limitReached {
		writeJSON(ctx, iris.StatusGone, map[string]string{
			"error":   "limit_reached",
			"message": "Access limit reached",
		})
		return
	}

	// 解析资源路径
	subPath := ctx.URLParam("path")
	resourcePath := share.ResourcePath
	fsPath, ok := s.resolveStaticPath(resourcePath)
	if !ok {
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Resource not found"})
		return
	}

	// 安全校验：如果带子路径，确保仍在分享根目录内
	if subPath != "" {
		cleaned := filepath.Clean("/" + subPath)
		// 兼容前端传入完整路径的情况：如果 subPath 以 share 根路径开头，剥离前缀转为相对路径
		// 例如 share 根为 /docs，前端传 /docs/sub/file.md → 剥离为 /sub/file.md
		sharePathPrefix := strings.TrimSuffix(share.ResourcePath, "/")
		if strings.HasPrefix(cleaned, sharePathPrefix+"/") {
			cleaned = strings.TrimPrefix(cleaned, sharePathPrefix)
		}
		fsPath = filepath.Join(fsPath, cleaned)
		// 路径遍历防护：确保仍在 shareRoot 内
		shareRoot, _ := s.resolveStaticPath(share.ResourcePath)
		absBase, _ := filepath.Abs(shareRoot)
		absFull, _ := filepath.Abs(fsPath)
		if !strings.HasPrefix(absFull, absBase+string(filepath.Separator)) && absFull != absBase {
			writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Resource not found"})
			return
		}
		resourcePath = share.ResourcePath + "/" + strings.TrimPrefix(cleaned, "/")
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		writeJSON(ctx, iris.StatusNotFound, map[string]string{"error": "Resource not found"})
		return
	}

	// 构建基础响应
	resp := map[string]any{
		"resourcePath":   resourcePath,
		"resourceType":   share.ResourceType,
		"accessCount":    share.AccessCount + 1,
		"maxAccessCount": share.MaxAccessCount,
		"expiresAt":      share.ExpiresAt,
		"effectiveAt":    share.EffectiveAt,
		"hasPassword":    share.PasswordHash != "",
	}

	if info.IsDir() {
		// 文件夹分享：返回与 /__tree__ 一致的 JSON 结构 {path, dirs, files}
		entries, err := os.ReadDir(fsPath)
		if err != nil {
			writeJSON(ctx, iris.StatusForbidden, map[string]string{"error": "Cannot read directory"})
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

		// 返回与 /__tree__ 一致的格式
		resp["path"] = resourcePath
		resp["dirs"] = dirs
		resp["files"] = files
		resp["isDir"] = true
		// 额外保留分享信息（不影响前端渲染）
		resp["currentPath"] = resourcePath
		relPath := ""
		if shareRoot, _ := s.resolveStaticPath(share.ResourcePath); shareRoot != "" {
			if rel, err := filepath.Rel(shareRoot, fsPath); err == nil && rel != "." {
				relPath = filepath.ToSlash(rel)
			}
		}
		resp["relPath"] = relPath
		writeJSON(ctx, iris.StatusOK, resp)
		return
	}

	// 文件分享：返回文件内容（base64 编码）
	ext := strings.ToLower(filepath.Ext(fsPath))
	content, err := os.ReadFile(fsPath)
	if err != nil {
		writeJSON(ctx, iris.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	resp["isDir"] = false
	resp["fileName"] = filepath.Base(fsPath)
	resp["ext"] = ext
	resp["size"] = info.Size()
	resp["contentType"] = contentType(ext)
	resp["content"] = base64.StdEncoding.EncodeToString(content)
	resp["isBinary"] = isBinaryExt(ext)
	writeJSON(ctx, iris.StatusOK, resp)
}
