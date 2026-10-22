// Package server 文件管理操作：新建目录 / 删除 / 重命名 / 移动（均沙箱限定在当前用户根目录内）
package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kataras/iris/v12"
)

// validEntryName 校验新建/改名使用的单段名称：非空、非点名、不含路径分隔符。
// 与 sanitizeUploadName 不同：这里不做清理改写，非法即整体拒绝（调用方传的是用户输入）。
func validEntryName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\")
}

// resolveFsEntrySafe 解析 fsops（删除/重命名/移动）的操作目标条目路径。
// 与 resolveUserPathSafe 的差异：条目本身是符号链接时操作**链接条目自身**，不跟随（不触及真实目标）。
// 安全校验：
//   - AllowSymlink=true：safeJoin 根内校验即可（配置显式放开符号链接）
//   - AllowSymlink=false：非链接条目沿用 EvalSymlinks 全路径沙箱校验（防中间目录链接逃逸）；
//     链接条目则校验父目录真实路径在根内，操作路径 = 父真实路径 + 条目名——
//     既保证父链真实在根内（操作系统解析路径时中间链接仍会跟随），又保留条目不被跟随。
//     链接目标本身可在根外：操作条目不访问内容，内容访问（读/预览）仍走 EvalSymlinks 沙箱。
func (s *Server) resolveFsEntrySafe(ctx iris.Context, relPath string) (string, string, bool) {
	root := s.userRoot(ctx)
	if root == "" {
		return "", "", false
	}
	entryFs, cleanRel, ok := safeJoin(root, relPath)
	if !ok {
		return "", "", false
	}
	if s.cfg.Server.AllowSymlink {
		return entryFs, cleanRel, true
	}
	info, err := os.Lstat(entryFs)
	if err != nil {
		return "", "", false
	}
	if info.Mode()&os.ModeSymlink == 0 {
		// 非链接条目：沿用原有 EvalSymlinks 沙箱校验
		return s.resolveUserPathSafe(ctx, relPath)
	}
	// 链接条目：操作链接本身，父目录真实路径必须仍在根内
	realParent, err := filepath.EvalSymlinks(filepath.Dir(entryFs))
	if err != nil {
		return "", "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", false
	}
	if realParent != realRoot && !strings.HasPrefix(realParent, realRoot+string(filepath.Separator)) {
		return "", "", false
	}
	return filepath.Join(realParent, filepath.Base(entryFs)), cleanRel, true
}

// ============================================================
// POST /api/mkdir — 新建目录（单层）
// ============================================================

func (s *Server) handleMkdir(ctx iris.Context) {
	var req struct {
		Path string `json:"path"` // 完整目标路径：父目录/新目录名
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	path := strings.TrimSpace(req.Path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimSuffix(path, "/")

	// 拆父目录与名字（目标尚不存在，不能直接 resolveUserPathSafe：EvalSymlinks 要求路径存在）
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	parent, name := path[:idx], path[idx+1:]
	if parent == "" {
		parent = "/"
	}
	if !validEntryName(name) {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Invalid folder name")
		return
	}

	// 父目录必须已存在且在沙箱内
	parentFs, parentRel, ok := s.resolveUserPathSafe(ctx, parent)
	if !ok {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	if info, err := os.Stat(parentFs); err != nil || !info.IsDir() {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	dst := filepath.Join(parentFs, name)
	if _, err := os.Lstat(dst); err == nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Already exists")
		return
	}
	if err := os.Mkdir(dst, 0755); err != nil {
		serverError(ctx, "mkdir failed", err, "path", path)
		return
	}

	slog.Info("fsop mkdir", "user", currentUserID(ctx), "org", currentOrgID(ctx), "path", parentRel+"/"+name)
	writeOK(ctx, map[string]interface{}{
		"path":   parentRel + "/" + name,
		"hidden": s.isHidden(name),
	})
}

// ============================================================
// DELETE /api/file?path= — 删除文件或目录（目录递归，不可恢复）
// ============================================================

func (s *Server) handleDeleteFile(ctx iris.Context) {
	relPath := strings.TrimSpace(ctx.URLParam("path"))
	// 拒绝删除用户根目录
	if relPath == "" || relPath == "/" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	// 符号链接只删链接本身，不删真实目标
	fsPath, cleanRel, ok := s.resolveFsEntrySafe(ctx, relPath)
	if !ok || cleanRel == "/" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	info, err := os.Lstat(fsPath)
	if err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	if err := os.RemoveAll(fsPath); err != nil {
		serverError(ctx, "delete failed", err, "path", cleanRel)
		return
	}

	slog.Info("fsop delete", "user", currentUserID(ctx), "org", currentOrgID(ctx), "path", cleanRel, "isDir", info.IsDir())
	writeOK(ctx, map[string]interface{}{"path": cleanRel, "isDir": info.IsDir()})
}

// ============================================================
// POST /api/rename — 同目录重命名
// ============================================================

func (s *Server) handleRename(ctx iris.Context) {
	var req struct {
		Path    string `json:"path"`
		NewName string `json:"newName"`
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	if !validEntryName(req.NewName) {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Invalid name")
		return
	}
	// 符号链接只重命名链接本身，不动真实目标
	srcFs, srcRel, ok := s.resolveFsEntrySafe(ctx, req.Path)
	if !ok || srcRel == "/" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	if _, err := os.Lstat(srcFs); err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	newName := strings.TrimSpace(req.NewName)
	dst := filepath.Join(filepath.Dir(srcFs), newName)
	if _, err := os.Lstat(dst); err == nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Already exists")
		return
	}
	if err := os.Rename(srcFs, dst); err != nil {
		serverError(ctx, "rename failed", err, "path", srcRel, "newName", newName)
		return
	}

	slog.Info("fsop rename", "user", currentUserID(ctx), "org", currentOrgID(ctx), "path", srcRel, "newName", newName)
	writeOK(ctx, map[string]interface{}{
		"path":   filepath.Dir(srcRel) + "/" + newName,
		"name":   newName,
		"hidden": s.isHidden(newName),
	})
}

// ============================================================
// POST /api/move — 移动到目标目录（保持原文件名）
// ============================================================

func (s *Server) handleMove(ctx iris.Context) {
	var req struct {
		Path    string `json:"path"`    // 源文件/目录
		DestDir string `json:"destDir"` // 目标目录（已存在）
	}
	if err := readJSON(ctx, &req); err != nil {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidJSON)
		return
	}
	// 符号链接只移动链接本身，不搬真实目标；目标目录走真实解析（移入"链接目录"=移入其真实目标，与浏览语义一致）
	srcFs, srcRel, ok := s.resolveFsEntrySafe(ctx, req.Path)
	if !ok || srcRel == "/" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	if _, err := os.Lstat(srcFs); err != nil {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	dstFs, dstRel, ok := s.resolveUserPathSafe(ctx, req.DestDir)
	if !ok {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	if info, err := os.Stat(dstFs); err != nil || !info.IsDir() {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	// 环检测：目标目录不得是源自身或源的子孙目录（否则目录会移进自己内部）
	srcPrefix := strings.TrimSuffix(srcRel, "/")
	if dstRel == srcPrefix || strings.HasPrefix(dstRel, srcPrefix+"/") {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Cannot move into itself")
		return
	}

	name := filepath.Base(srcRel)
	dst := filepath.Join(dstFs, name)
	if _, err := os.Lstat(dst); err == nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Already exists in destination")
		return
	}
	if err := os.Rename(srcFs, dst); err != nil {
		serverError(ctx, "move failed", err, "path", srcRel, "dest", dstRel)
		return
	}

	newPath := strings.TrimSuffix(dstRel, "/") + "/" + name
	slog.Info("fsop move", "user", currentUserID(ctx), "org", currentOrgID(ctx), "path", srcRel, "dest", dstRel)
	writeOK(ctx, map[string]interface{}{"path": newPath})
}
