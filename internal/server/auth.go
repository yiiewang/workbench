// Package server HTTP 鉴权逻辑
package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// LoadOrCreateTokenSecret 加载或生成 token 签名秘钥
func LoadOrCreateTokenSecret(serveDir string) ([]byte, error) {
	secretPath := filepath.Join(serveDir, ".token_secret")
	if data, err := os.ReadFile(secretPath); err == nil {
		return data, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate token secret: %w", err)
	}
	if err := os.WriteFile(secretPath, secret, 0600); err != nil {
		return nil, fmt.Errorf("write token secret: %w", err)
	}
	return secret, nil
}

// HashPassword 用 bcrypt 对密码加盐哈希，抵御彩虹表攻击
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword 校验密码哈希，优先 bcrypt，兼容旧 SHA-256 哈希（存量用户登录后改密即升级）
func VerifyPassword(hash, password string) bool {
	// 新密码：bcrypt
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err == nil {
		return true
	}
	// 兼容旧 SHA-256 无盐哈希
	h := sha256.Sum256([]byte(password))
	old := hex.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(hash), []byte(old)) == 1
}

// secondsPerDay 一天的秒数，用于 token 过期时间换算
const secondsPerDay = 24 * 60 * 60

// 角色名常量（与 db.roles 表 name 字段对应）
const (
	roleNameAdmin    = "admin"
	roleNameUser     = "user"
	roleNameOrgAdmin = "org_admin"
)

// GenerateToken 生成带过期时间的 HMAC token
// payload 格式: userID:expiry（组织上下文通过 X-Org-Id 请求头传递，不在 token 固化）
func GenerateToken(userID int64, secret []byte, expiryDays int) string {
	expiry := time.Now().Unix() + int64(expiryDays)*secondsPerDay
	payload := fmt.Sprintf("%d:%d", userID, expiry)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	raw := fmt.Sprintf("%s:%s", payload, hex.EncodeToString(sig))
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// ValidateToken 校验 token，返回 (valid, userID)
func ValidateToken(token string, secret []byte) (bool, int64) {
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false, 0
	}

	parts := strings.SplitN(string(data), ":", 3)
	if len(parts) != 3 {
		return false, 0
	}
	userIDStr, expiryStr, sigHex := parts[0], parts[1], parts[2]
	if !verifySig(userIDStr+":"+expiryStr, sigHex, secret) || !checkExpiry(expiryStr) {
		return false, 0
	}

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return false, 0
	}
	return true, userID
}

// verifySig 校验 payload 与签名是否匹配
func verifySig(payload, sigHex string, secret []byte) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sigHex), []byte(expectedSig))
}

// checkExpiry 校验过期时间字符串是否未过期
func checkExpiry(expiryStr string) bool {
	expiry := int64(0)
	if _, err := fmt.Sscanf(expiryStr, "%d", &expiry); err != nil {
		return false
	}
	return time.Now().Unix() <= expiry
}

// extractTokenFromContext 从 iris.Context 请求头提取 Bearer token
func extractTokenFromContext(ctx iris.Context) string {
	auth := ctx.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// AuthMiddleware 是 iris 鉴权中间件，校验失败返回 401，成功把用户与组织上下文写入 ctx.Values()。
// 组织上下文解析：优先 X-Org-Id 请求头（组织切换），缺省时由 resolveIdentity 取用户默认组织。
// resolveIdentity 返回 (resolvedOrgID, orgName, userName, orgRole, isPlatformAdmin, err)。
func AuthMiddleware(secret []byte, resolveIdentity func(context.Context, int64, int64) (int64, string, string, string, bool, error)) iris.Handler {
	return func(ctx iris.Context) {
		token := extractTokenFromContext(ctx)
		if token == "" {
			writeFail(ctx, iris.StatusUnauthorized, CodeMissingToken)
			return
		}
		valid, userID := ValidateToken(token, secret)
		if !valid {
			writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
			return
		}

		// 组织上下文：优先 X-Org-Id 头，缺省 0 由 resolveIdentity 取默认组织
		orgID := int64(0)
		if h := ctx.GetHeader("X-Org-Id"); h != "" {
			if v, err := strconv.ParseInt(strings.TrimSpace(h), 10, 64); err == nil {
				orgID = v
			}
		}

		resolvedOrgID, orgName, userName, orgRole, isPlatformAdmin, err := resolveIdentity(ctx.Request().Context(), userID, orgID)
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
		ctx.Next()
	}
}

// RequireUserManager 要求当前用户具备用户管理权限（平台超管，或组织 owner/admin），否则 403。
// 须在 AuthMiddleware 之后使用；通过后把 isSuperAdmin 标志写入 ctx（平台超管=true，组织管理员=false）。
func RequireUserManager(ctx iris.Context) {
	if !isPlatformAdmin(ctx) && !isOrgManager(ctx) {
		writeFail(ctx, iris.StatusForbidden, CodeAdminRequired)
		return
	}
	ctx.Values().Set("isSuperAdmin", isPlatformAdmin(ctx))
	ctx.Next()
}

// isOrgManager 判断当前用户是否为组织管理员（owner/admin 角色）
func isOrgManager(ctx iris.Context) bool {
	r := currentOrgRole(ctx)
	return r == db.RoleOwner || r == db.RoleAdmin
}

// isSuperAdmin 从 ctx 读取 RequireUserManager 写入的标志（true=超级 admin，false=org_admin）
func isSuperAdmin(ctx iris.Context) bool {
	if v := ctx.Values().Get("isSuperAdmin"); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// currentUserID 从 iris.Context 中取出 AuthMiddleware 写入的整数 userID
func currentUserID(ctx iris.Context) int64 {
	if v := ctx.Values().Get("userID"); v != nil {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}

// currentOrgID 从 iris.Context 中取出 AuthMiddleware 写入的整数 orgID
func currentOrgID(ctx iris.Context) int64 {
	if v := ctx.Values().Get("orgID"); v != nil {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}

// currentUserName 从 iris.Context 中取出 AuthMiddleware 写入的用户 name
func currentUserName(ctx iris.Context) string {
	if v := ctx.Values().Get("userName"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// currentOrgName 从 iris.Context 中取出 AuthMiddleware 写入的组织 name
func currentOrgName(ctx iris.Context) string {
	if v := ctx.Values().Get("orgName"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// currentRole 从 iris.Context 中取出 AuthMiddleware 写入的角色名（对外语义：admin/org_admin/user）
func currentRole(ctx iris.Context) string {
	if v := ctx.Values().Get("role"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// currentOrgRole 从 iris.Context 中取出 AuthMiddleware 写入的组织内角色（owner/admin/member）
func currentOrgRole(ctx iris.Context) string {
	if v := ctx.Values().Get("orgRole"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// isPlatformAdmin 从 iris.Context 中取出 AuthMiddleware 写入的平台超管标志
func isPlatformAdmin(ctx iris.Context) bool {
	if v := ctx.Values().Get("isPlatformAdmin"); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// externalRole 将内部组织角色 + 平台超管标志映射为对外角色（供前端展示与权限判断）。
// 平台超管→admin，组织 owner/admin→org_admin，其余→user。
func externalRole(orgRole string, platformAdmin bool) string {
	if platformAdmin {
		return roleNameAdmin
	}
	if orgRole == db.RoleOwner || orgRole == db.RoleAdmin {
		return roleNameOrgAdmin
	}
	return roleNameUser
}
