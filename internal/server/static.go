package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench"
)

// ============================================================
// 路由映射（静态文件路由重定向）
// ============================================================

func (s *Server) handleRouteRedirect(ctx iris.Context) bool {
	target, exists := s.cfg.Routes[ctx.Path()]
	if !exists {
		return false
	}
	if target == "__listdir__" {
		s.listDirectory(ctx, s.adminRoot)
		return true
	}
	if target == "__spa__" {
		s.serveUIAsset(ctx, "index.html")
		return true
	}
	if strings.HasPrefix(target, "/") {
		ctx.Redirect(target, iris.StatusFound)
		return true
	}
	return false
}

// ============================================================
// GET /__map__
// ============================================================

func (s *Server) handleMap(ctx iris.Context) {
	displayMap := make(map[string]string)
	for k, v := range s.cfg.Routes {
		if strings.HasPrefix(k, "/") && !strings.HasPrefix(v, "__") {
			displayMap[k] = v
		}
	}
	writeOK(ctx, displayMap)
}

// serveUIAsset 从 embed FS 服务内置 UI 资源（index.html/todo.html/css/js）
// 命中返回 true（已写入响应），未命中返回 false 由调用方走磁盘
func (s *Server) serveUIAsset(ctx iris.Context, reqPath string) bool {
	rel := strings.TrimPrefix(reqPath, "/")
	if rel == "" {
		rel = "index.html"
	}
	if !isUIAsset(rel) {
		return false
	}
	data, err := workbench.UIFS.ReadFile("frontend/dist/" + rel)
	if err != nil {
		return false
	}
	ctx.ContentType(mimeTypeByExt(rel))
	// HTML 入口（index.html/todo.html）必须每次重新校验，避免浏览器缓存旧版
	// 导致引用的 /assets/* hash 失效（典型的 stale cache → MIME 404 雪崩）
	// assets/* 由 Vite 哈希命名，内容不可变，可长期缓存
	if rel == "index.html" || rel == "todo.html" {
		ctx.Header("Cache-Control", "no-cache")
	} else {
		ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	_, _ = ctx.Write(data)
	return true
}

// isUIAsset 判断是否为内置 UI 资源（Vite 构建产物，embed 打包，不可变）
func isUIAsset(rel string) bool {
	switch rel {
	case "index.html", "todo.html":
		return true
	}
	return strings.HasPrefix(rel, "assets/")
}

// mimeTypeByExt 按扩展名返回 Content-Type
func mimeTypeByExt(path string) string {
	switch filepath.Ext(path) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2; charset=utf-8"
	case ".woff":
		return "font/woff; charset=utf-8"
	case ".ttf":
		return "font/ttf; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// isPublicEntry 判断路径是否为公开页面入口（无需鉴权即可访问）
func (s *Server) isPublicEntry(path string) bool {
	// 根路径
	if path == "/" || path == "" {
		return true
	}
	cleaned := strings.TrimPrefix(path, "/")
	// UI 资源（embed 内置：index.html/todo.html/assets/*）一律公开，无需鉴权
	if isUIAsset(cleaned) {
		return true
	}
	// 路由映射中配置的公开路径（如 /todo 重定向到 /todo.html）
	if _, exists := s.cfg.Routes[path]; exists {
		return true
	}
	return false
}

// handleStaticWithAuth 静态文件访问：页面入口公开，其余需 Bearer token
func (s *Server) handleStaticWithAuth(ctx iris.Context) {
	reqPath := ctx.Path()

	// 公开页面入口：放行
	if s.isPublicEntry(reqPath) {
		s.handleStatic(ctx)
		return
	}

	// 其余文件：需要鉴权
	token := extractTokenFromContext(ctx)
	if token == "" {
		writeFail(ctx, iris.StatusUnauthorized, CodeUnauthorized)
		return
	}
	valid, userID := ValidateToken(token, s.tokenSecret)
	if !valid {
		writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
		return
	}
	// 组织上下文：X-Org-Id 头（可选），缺省默认组织
	orgID := int64(0)
	if h := ctx.GetHeader("X-Org-Id"); h != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(h), 10, 64); err == nil {
			orgID = v
		}
	}
	resolvedOrgID, orgName, userName, orgRole, isPlatformAdmin, err := s.db.ResolveUserOrgIdentity(context.Background(), userID, orgID)
	if err != nil || resolvedOrgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}
	ctx.Values().Set("userID", userID)
	ctx.Values().Set("orgID", resolvedOrgID)
	ctx.Values().Set("userName", userName)
	ctx.Values().Set("orgName", orgName)
	ctx.Values().Set("orgRole", orgRole)
	ctx.Values().Set("isPlatformAdmin", isPlatformAdmin)
	ctx.Values().Set("role", externalRole(orgRole, isPlatformAdmin))
	s.handleStatic(ctx)
}

func (s *Server) handleStatic(ctx iris.Context) {
	if s.handleRouteRedirect(ctx) {
		return
	}

	reqPath := ctx.Path()

	// 检查通过中文名访问
	decodedPath, _ := url.QueryUnescape(reqPath)
	for filePath, displayName := range s.cfg.Routes {
		if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(displayName, "__") {
			continue
		}
		if decodedPath == "/"+displayName || strings.HasSuffix(decodedPath, "/"+displayName) {
			reqPath = filePath
			break
		}
	}

	// UI 资源走 embed（内置模板，不可变），未命中再走磁盘用户根
	if s.serveUIAsset(ctx, reqPath) {
		return
	}

	fsPath, ok := s.resolveUserPath(ctx, reqPath)
	if !ok {
		ctx.NotFound()
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil {
		ctx.NotFound()
		return
	}

	if info.IsDir() {
		indexPath := filepath.Join(fsPath, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			_ = ctx.ServeFile(indexPath)
			return
		}
		s.listDirectory(ctx, fsPath)
		return
	}

	content, err := os.ReadFile(fsPath)
	if err != nil {
		serverError(ctx, "read static file failed", err)
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	// 二进制扩展名走 ServeFile 触发下载
	if isBinaryExt(ext) {
		_ = ctx.ServeFile(fsPath)
		return
	}
	ctx.ContentType(contentType(ext) + "; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Content-Length", fmt.Sprintf("%d", len(content)))
	ctx.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	ctx.StatusCode(iris.StatusOK)
	_, _ = ctx.Write(content)
}

// ============================================================
// GET /api/file?path=... — 静态文件访问（统一 /api 前缀）
// ============================================================

func (s *Server) handleServeFile(ctx iris.Context) {
	relPath := ctx.URLParam("path")
	if relPath == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}

	if !strings.HasPrefix(relPath, "/") {
		relPath = "/" + relPath
	}
	fsPath, ok := s.resolveUserPath(ctx, relPath)
	if !ok {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	if info.IsDir() {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}

	ext := strings.ToLower(filepath.Ext(fsPath))
	if isBinaryExt(ext) {
		_ = ctx.ServeFile(fsPath)
		return
	}

	content, err := os.ReadFile(fsPath)
	if err != nil {
		serverError(ctx, "read file failed", err)
		return
	}
	ctx.ContentType(contentType(ext) + "; charset=utf-8")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Content-Length", fmt.Sprintf("%d", len(content)))
	ctx.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	ctx.StatusCode(iris.StatusOK)
	_, _ = ctx.Write(content)
}

// isBinaryExt 判断是否应触发下载而非内联展示
func isBinaryExt(ext string) bool {
	switch ext {
	case ".zip", ".tar", ".gz", ".tgz", ".rar", ".7z", ".bz2", ".xz",
		".exe", ".dll", ".so", ".dylib", ".bin",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".mp4", ".mp3", ".wav", ".flv", ".avi", ".mov", ".mkv",
		".ttf", ".otf", ".woff", ".woff2":
		return true
	}
	return false
}

func (s *Server) listDirectory(ctx iris.Context, dirPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		ctx.StatusCode(iris.StatusForbidden)
		return
	}

	var dirs, files []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	displayPath := html.EscapeString(ctx.Path())

	var items []string
	if displayPath != "/" {
		parent := filepath.Dir(strings.TrimRight(displayPath, "/"))
		if parent == "." {
			parent = "/"
		}
		items = append(items, fmt.Sprintf(`<li><a href="%s">../</a></li>`, parent))
	}

	displayNames := make(map[string]string)
	for fp, mn := range s.cfg.Routes {
		fname := strings.TrimPrefix(fp, "/")
		if strings.HasPrefix(mn, "__") {
			continue
		}
		displayNames[fname] = mn
	}

	for _, d := range dirs {
		name := d.Name()
		dn := name
		if mapped, ok := displayNames[name]; ok {
			dn = mapped
		}
		items = append(items, fmt.Sprintf(`<li><a href="%s/">%s/</a></li>`,
			html.EscapeString(name), html.EscapeString(dn)))
	}

	for _, f := range files {
		name := f.Name()
		dn := name
		if mapped, ok := displayNames[name]; ok {
			dn = mapped
		}
		href := url.PathEscape(name)
		var fileHref string
		switch {
		case !strings.HasSuffix(displayPath, "/"):
			fileHref = displayPath + "/" + href
		case displayPath != "/":
			fileHref = displayPath + href
		default:
			fileHref = "/" + href
		}
		items = append(items, fmt.Sprintf(`<li><a href="%s">%s</a></li>`,
			html.EscapeString(fileHref), html.EscapeString(dn)))
	}

	body := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Index of %s</title>
</head>
<body>
<h1>Index of %s</h1>
<ul>
%s
</ul>
</body>
</html>`, displayPath, displayPath, strings.Join(items, ""))

	ctx.ContentType("text/html; charset=utf-8")
	_, _ = ctx.WriteString(body)
}

func contentType(ext string) string {
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".xml":
		return "application/xml"
	case ".py":
		return "text/x-python"
	case ".go":
		return "text/x-go"
	default:
		return "application/octet-stream"
	}
}
