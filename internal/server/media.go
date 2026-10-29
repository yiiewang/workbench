package server

// 媒体流式访问（视频/音频）。
//
// 背景：<video>/<audio> 的 src 请求由浏览器直接发起，带不上 Authorization 与 X-Org-Id
// 请求头，而 /api/file 走 AuthMiddleware 只认 Bearer，播放器无法直接指向它。这里引入
// 一条独立的媒体通道：
//   1. GET /api/media/ticket（Bearer 鉴权）→ 换取绑定 path/userID/orgID 的短时效 ticket
//   2. GET /api/media?path=&ticket=（无 header 鉴权）→ 校验 ticket 后用 http.ServeContent
//      响应，原生支持 Range（206 / Content-Range / Accept-Ranges），播放器可拖动、不占内存
//
// 两个配套约束：
//   - ticket 不进访问日志：loggingMiddleware 只记录 ctx.Path()，不含 query。
//   - /api/media 必须跳过 ctx.Record()：iris 的 ResponseRecorder 会把整个响应体攒在
//     内存里、请求结束才 flush，那样就不是流式了（见 loggingMiddleware 的特判）。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
)

// mediaTicketTTL 媒体 ticket 有效期。不能设成几十秒：播放器拖动进度条时会重新发起
// Range 请求，ticket 过期会直接断流。ticket 绑定单个 path 且不落日志，泄漏面可控。
const mediaTicketTTL = 2 * time.Hour

// mediaMIME 可在浏览器直接播放的媒体扩展名 → MIME。
// 不能复用 contentType()：它对媒体扩展名一律返回 application/octet-stream，
// 而 <video>/<audio> 是严格按 MIME 判定能否播放的（不像 <img> 会嗅探字节）。
var mediaMIME = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime",
	".webm": "video/webm", ".ogv": "video/ogg", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac",
	".wav": "audio/wav", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".flac": "audio/flac",
}

// isMediaExt 判断扩展名是否为可直接播放的媒体文件
func isMediaExt(ext string) bool {
	_, ok := mediaMIME[strings.ToLower(ext)]
	return ok
}

// normalizeRelPath 统一相对路径为以 / 开头的绝对路径形式
func normalizeRelPath(p string) string {
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// issueMediaTicket 签发媒体访问 ticket：payload = exp|userID|orgID|path，附 HMAC-SHA256 签名。
// 用 tokenSecret 做密钥，与登录 JWT 同源，不引入新密钥。
func (s *Server) issueMediaTicket(path string, userID, orgID int64) string {
	payload := fmt.Sprintf("%d|%d|%d|%s", time.Now().Add(mediaTicketTTL).Unix(), userID, orgID, path)
	mac := hmac.New(sha256.New, s.tokenSecret)
	_, _ = mac.Write([]byte(payload))
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(payload)) + "." + enc(mac.Sum(nil))
}

// verifyMediaTicket 校验 ticket 的签名、有效期与绑定路径，返回签发时的 userID/orgID。
// 三者缺一不可：userID+orgID 用于定位用户根目录（<video> 请求带不上 X-Org-Id），
// path 绑定防止一个 ticket 被用于访问同用户目录下的其他文件。
func (s *Server) verifyMediaTicket(ticket, path string) (userID, orgID int64, ok bool) {
	parts := strings.Split(ticket, ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, 0, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, 0, false
	}
	mac := hmac.New(sha256.New, s.tokenSecret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return 0, 0, false
	}

	fields := strings.SplitN(string(payload), "|", 4)
	if len(fields) != 4 {
		return 0, 0, false
	}
	exp, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, 0, false
	}
	if userID, err = strconv.ParseInt(fields[1], 10, 64); err != nil || userID == 0 {
		return 0, 0, false
	}
	if orgID, err = strconv.ParseInt(fields[2], 10, 64); err != nil || orgID == 0 {
		return 0, 0, false
	}
	if fields[3] != path {
		return 0, 0, false
	}
	return userID, orgID, true
}

// ============================================================
// GET /api/media/ticket?path=...（需鉴权）— 换取媒体播放地址
// ============================================================

func (s *Server) handleMediaTicket(ctx iris.Context) {
	relPath := normalizeRelPath(ctx.URLParam("path"))
	if relPath == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}

	userID := currentUserID(ctx)
	orgID := currentOrgID(ctx)
	if userID == 0 || orgID == 0 {
		writeFail(ctx, iris.StatusForbidden, CodeForbidden)
		return
	}

	fsPath, ok := s.resolveUserPathByOwner(ctx.Request().Context(), orgID, userID, relPath)
	if !ok {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	if !isMediaExt(filepath.Ext(fsPath)) {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "not a playable media file")
		return
	}

	ticket := s.issueMediaTicket(relPath, userID, orgID)
	// size 一并下发：流式分支不会把整个文件读成 Blob，前端拿不到大小，顶栏要显示
	writeOK(ctx, map[string]any{
		"ticket": ticket,
		"size":   info.Size(),
		"url": fmt.Sprintf("/api/media?path=%s&ticket=%s",
			url.QueryEscape(relPath), url.QueryEscape(ticket)),
	})
}

// ============================================================
// GET /api/media?path=...&ticket=...（无 header 鉴权，靠 ticket）
// ============================================================

func (s *Server) handleServeMedia(ctx iris.Context) {
	relPath := normalizeRelPath(ctx.URLParam("path"))
	ticket := ctx.URLParam("ticket")
	if relPath == "" || ticket == "" {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidParam)
		return
	}

	userID, orgID, ok := s.verifyMediaTicket(ticket, relPath)
	if !ok {
		writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
		return
	}

	fsPath, ok := s.resolveUserPathByOwner(ctx.Request().Context(), orgID, userID, relPath)
	if !ok {
		writeFail(ctx, iris.StatusBadRequest, CodeInvalidPath)
		return
	}
	info, err := os.Stat(fsPath)
	if err != nil || info.IsDir() {
		writeFail(ctx, iris.StatusNotFound, CodeNotFound)
		return
	}
	ctype, isMedia := mediaMIME[strings.ToLower(filepath.Ext(fsPath))]
	if !isMedia {
		writeFailMsg(ctx, iris.StatusBadRequest, CodeInvalidParam, "not a playable media file")
		return
	}

	f, err := os.Open(fsPath)
	if err != nil {
		serverError(ctx, "open media file failed", err)
		return
	}
	defer f.Close()

	// Content-Type 先写好：http.ServeContent 只在未设置时才按扩展名嗅探。
	// Accept-Ranges 也显式声明，*os.File 可 Seek，Range 一定可满足。
	ctx.ContentType(ctype)
	ctx.Header("Accept-Ranges", "bytes")
	ctx.Header("Cache-Control", "private, max-age=60")
	ctx.Header("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))

	// 标准库实现：自动处理 If-Range / Range → 206 + Content-Range，边读边写不缓冲。
	http.ServeContent(ctx.ResponseWriter(), ctx.Request(), filepath.Base(fsPath), info.ModTime(), f)
}
