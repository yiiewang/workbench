package server

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kataras/iris/v12"
)

// listTreeItems 列出目录下的可见条目：隐藏规则过滤、符号链接目录按真实类型归类、
// 文件名自然排序。tree API 与分享目录视图共用。
func (s *Server) listTreeItems(fsPath string) (dirs, files []treeItem, err error) {
	entries, err := os.ReadDir(fsPath)
	if err != nil {
		return nil, nil, err
	}
	dirs, files = make([]treeItem, 0), make([]treeItem, 0)
	for _, e := range entries {
		if s.isHidden(e.Name()) {
			continue
		}
		isDir := e.IsDir()
		isLink := e.Type()&os.ModeSymlink != 0
		// 软链接目录：e.IsDir() 返回 false，需要 stat 跟随链接后判断
		if isLink && !isDir {
			if info, err := os.Stat(filepath.Join(fsPath, e.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, treeItem{Name: e.Name(), IsDir: true, IsLink: isLink})
		} else {
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			files = append(files, treeItem{Name: e.Name(), IsDir: false, Size: size, IsLink: isLink})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return naturalLess(dirs[i].Name, dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return naturalLess(files[i].Name, files[j].Name) })
	return dirs, files, nil
}

// isHidden 根据配置中的 hidden 规则判断文件名是否应隐藏
func (s *Server) isHidden(name string) bool {
	for _, pattern := range s.cfg.Server.Hidden {
		if pattern == ".*" && strings.HasPrefix(name, ".") {
			return true
		}
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// userRoot 返回当前登录用户的私有文件根目录: serDirAbs/{orgName}/{userName}/
// 目录层使用 name 拼路径（双轨架构：存储层用整数 id，目录层用 name）。
// 若目录不存在则自动创建。orgName 或 userName 为空时返回空串表示无效。
func (s *Server) userRoot(ctx iris.Context) string {
	orgName := currentOrgName(ctx)
	userName := currentUserName(ctx)
	if orgName == "" || userName == "" {
		return ""
	}
	root := filepath.Join(s.serDirAbs, orgName, userName)
	_ = os.MkdirAll(root, 0755)
	return root
}

// userRootByOwner 通过 share 的 owner 整数 id 反查 name 定位用户根目录（分享访问场景）
func (s *Server) userRootByOwner(ctx context.Context, orgID, userID int64) string {
	if orgID == 0 || userID == 0 {
		return ""
	}
	orgName, userName, err := s.db.GetUserNames(ctx, orgID, userID)
	if err != nil {
		return ""
	}
	return filepath.Join(s.serDirAbs, orgName, userName)
}

// resolveUserPath 以当前用户根为基准解析相对路径，防止跨用户/跨组织目录穿越
// 返回解析后的绝对路径和是否成功
func (s *Server) resolveUserPath(ctx iris.Context, relPath string) (string, bool) {
	root := s.userRoot(ctx)
	if root == "" {
		return "", false
	}
	fsPath, _, ok := safeJoin(root, relPath)
	if !ok {
		return "", false
	}
	if s.cfg.Server.AllowSymlink {
		return fsPath, true
	}
	realPath, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", false
	}
	realBase, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if realPath != realBase && !strings.HasPrefix(realPath, realBase+string(filepath.Separator)) {
		return "", false
	}
	return realPath, true
}

// resolveUserPathByOwner 以指定 owner 的用户根为基准解析路径（分享访问场景）
func (s *Server) resolveUserPathByOwner(ctx context.Context, orgID, userID int64, relPath string) (string, bool) {
	root := s.userRootByOwner(ctx, orgID, userID)
	if root == "" {
		return "", false
	}
	fsPath, _, ok := safeJoin(root, relPath)
	if !ok {
		return "", false
	}
	if s.cfg.Server.AllowSymlink {
		return fsPath, true
	}
	realPath, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", false
	}
	realBase, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if realPath != realBase && !strings.HasPrefix(realPath, realBase+string(filepath.Separator)) {
		return "", false
	}
	return realPath, true
}

// resolveUserPathSafe 以当前用户根为基准解析路径，返回 (fsPath, cleanRel, ok)
// cleanRel 是相对于用户根的规范化路径（以 / 开头），供前端展示用
func (s *Server) resolveUserPathSafe(ctx iris.Context, relPath string) (string, string, bool) {
	root := s.userRoot(ctx)
	if root == "" {
		return "", "", false
	}
	fsPath, cleanRel, ok := safeJoin(root, relPath)
	if !ok {
		return "", "", false
	}
	if s.cfg.Server.AllowSymlink {
		return fsPath, cleanRel, true
	}
	realPath, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return "", "", false
	}
	realBase, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	if realPath != realBase && !strings.HasPrefix(realPath, realBase+string(filepath.Separator)) {
		return "", "", false
	}
	return realPath, cleanRel, true
}

// safeJoin 将 baseDir 与 relPath 安全拼接，确保结果落在 baseDir 目录内，防止路径遍历。
// 返回拼接后的绝对路径、相对于 baseDir 的规范化路径（以 / 开头，根目录为 /）。
func safeJoin(baseDir, relPath string) (fsPath, cleanRel string, ok bool) {
	cleaned := filepath.Clean(relPath)
	fsPath = filepath.Join(baseDir, cleaned)
	rel, err := filepath.Rel(baseDir, fsPath)
	if err != nil {
		return "", "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	if rel == "." {
		cleanRel = "/"
	} else {
		cleanRel = "/" + filepath.ToSlash(rel)
	}
	return fsPath, cleanRel, true
}
