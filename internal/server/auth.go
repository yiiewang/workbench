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

// GenerateToken 生成带过期时间的 HMAC token
// payload 格式: orgID:userID:expiry（orgID/userID 为整数 id）
func GenerateToken(orgID, userID int64, secret []byte, expiryDays int) string {
	expiry := time.Now().Unix() + int64(expiryDays)*secondsPerDay
	payload := fmt.Sprintf("%d:%d:%d", orgID, userID, expiry)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	raw := fmt.Sprintf("%s:%s", payload, hex.EncodeToString(sig))
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// ValidateToken 校验 token，返回 (valid, orgID, userID)
func ValidateToken(token string, secret []byte) (bool, int64, int64) {
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false, 0, 0
	}

	parts := strings.SplitN(string(data), ":", 4)
	if len(parts) != 4 {
		return false, 0, 0
	}
	orgIDStr, userIDStr, expiryStr, sigHex := parts[0], parts[1], parts[2], parts[3]
	if !verifySig(orgIDStr+":"+userIDStr+":"+expiryStr, sigHex, secret) || !checkExpiry(expiryStr) {
		return false, 0, 0
	}

	orgID, err1 := strconv.ParseInt(orgIDStr, 10, 64)
	userID, err2 := strconv.ParseInt(userIDStr, 10, 64)
	if err1 != nil || err2 != nil {
		return false, 0, 0
	}
	return true, orgID, userID
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

// AuthMiddleware 是 iris 鉴权中间件，校验失败直接返回 401，成功把整数 orgID/userID 与 name 写入 ctx.Values()
// lookupNames 用于按整数 id 反查 org/user 的 name（写 ctx 供目录路径与展示使用），传 nil 则不反查
func AuthMiddleware(secret []byte, lookupNames func(context.Context, int64, int64) (string, string, error)) iris.Handler {
	return func(ctx iris.Context) {
		token := extractTokenFromContext(ctx)
		if token == "" {
			writeFail(ctx, iris.StatusUnauthorized, CodeMissingToken)
			return
		}
		valid, orgID, userID := ValidateToken(token, secret)
		if !valid {
			writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
			return
		}
		orgName, userName := "", ""
		if lookupNames != nil {
			if on, un, err := lookupNames(ctx.Request().Context(), orgID, userID); err == nil {
				orgName, userName = on, un
			}
		}
		ctx.Values().Set("userID", userID)
		ctx.Values().Set("orgID", orgID)
		ctx.Values().Set("userName", userName)
		ctx.Values().Set("orgName", orgName)
		ctx.Next()
	}
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
