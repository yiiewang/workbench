// Package server 文件上传（POST /api/upload）
package server

import (
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/kataras/iris/v12"
)

// 上传资源限制（首版写死，后续如需可挪到 config.yaml）
const (
	uploadMaxBodySize = 512 << 20 // 单请求 body 上限 512MB
	uploadMaxFiles    = 100       // 单请求最多文件数
	uploadMemBuffer   = 32 << 20  // multipart 内存缓冲 32MB，超出部分自动落盘临时文件
)

type uploadItem struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type uploadSkipItem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type uploadData struct {
	Uploaded []uploadItem     `json:"uploaded"`
	Skipped  []uploadSkipItem `json:"skipped"`
	// HiddenNames 上传成功但命中 hidden 过滤规则、目录树中不可见的文件名（提醒用户避免困惑）
	HiddenNames []string `json:"hiddenNames,omitempty"`
}

// handleUpload POST /api/upload — multipart（path + files）上传文件到当前用户根目录下的指定子目录。
// 权限控制：
//   - auth 中间件保证已登录且组织上下文有效（401/403 由中间件返回）
//   - resolveUserPathSafe 把目标目录沙箱限定在 serDirAbs/{orgName}/{userName}/ 内，
//     防跨用户/跨组织目录穿越；AllowSymlink=false 时对真实路径做符号链接逃逸校验
//   - 文件名经 sanitizeUploadName 清理（防文件名携带路径穿越）
//   - body 大小与单请求文件数上限防资源滥用
//
// 与现有写操作（创建分享/任务）一致：登录用户对自己的私有目录有全部操作权，不按角色额外收紧。
func (s *Server) handleUpload(ctx iris.Context) {
	// 大小限制：超限由 iris 中断请求（413）
	ctx.SetMaxRequestBodySize(uploadMaxBodySize)

	// 解析 multipart（超出内存缓冲的部分自动落盘，不撑内存）
	if err := ctx.Request().ParseMultipartForm(uploadMemBuffer); err != nil {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidJSON, "Invalid multipart form")
		return
	}
	form := ctx.Request().MultipartForm
	if form == nil || len(form.File["files"]) == 0 {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "No file provided")
		return
	}
	if len(form.File["files"]) > uploadMaxFiles {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "Too many files per request")
		return
	}

	// 目标目录：沙箱限定在当前用户根目录内
	relPath := ctx.FormValue("path")
	if relPath == "" {
		relPath = "/"
	}
	dirFsPath, _, ok := s.resolveUserPathSafe(ctx, relPath)
	if !ok {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	info, err := os.Stat(dirFsPath)
	if err != nil || !info.IsDir() {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}

	data := uploadData{Uploaded: []uploadItem{}, Skipped: []uploadSkipItem{}}
	for _, fh := range form.File["files"] {
		name := sanitizeUploadName(fh.Filename)
		if name == "" {
			data.Skipped = append(data.Skipped, uploadSkipItem{Name: fh.Filename, Reason: "invalid file name"})
			continue
		}
		dst := filepath.Join(dirFsPath, name)
		// 同名不覆盖：存在即跳过（防数据丢失）
		if _, err := os.Lstat(dst); err == nil {
			data.Skipped = append(data.Skipped, uploadSkipItem{Name: name, Reason: "already exists"})
			continue
		}
		size, err := saveUploadFile(fh, dst)
		if err != nil {
			slog.Error("upload save failed", "user", currentUserID(ctx), "dir", relPath, "file", name, "err", err)
			data.Skipped = append(data.Skipped, uploadSkipItem{Name: name, Reason: "write failed"})
			continue
		}
		data.Uploaded = append(data.Uploaded, uploadItem{Name: name, Size: size})
		// 命中 hidden 规则（如 .js、点开头文件）的文件树里不可见，单独报告
		if s.isHidden(name) {
			data.HiddenNames = append(data.HiddenNames, name)
		}
	}

	slog.Info("upload",
		"user", currentUserID(ctx), "org", currentOrgID(ctx), "dir", relPath,
		"uploaded", len(data.Uploaded), "skipped", len(data.Skipped))
	writeOK(ctx, data)
}

// saveUploadFile 把 multipart 文件落盘到 dst（调用方已保证 dst 在用户沙箱内），返回写入字节数。
// O_EXCL 防并发上传同名文件时的竞态覆盖（与外层 Lstat 检查双保险）。
func saveUploadFile(fh *multipart.FileHeader, dst string) (int64, error) {
	src, err := fh.Open()
	if err != nil {
		return 0, err
	}
	defer src.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	return io.Copy(out, src)
}

// sanitizeUploadName 清理上传文件名：仅取最后一段（剥离任何路径成分），拒绝空名与点名。
// 防御文件名携带绝对路径/相对路径穿越（如 "../../evil.sh"、"C:\f\a.txt"）。
func sanitizeUploadName(filename string) string {
	filename = strings.ReplaceAll(filename, "\\", "/")
	name := filepath.Base(strings.TrimSpace(filename))
	if name == "" || name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}
