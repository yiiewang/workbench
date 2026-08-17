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
// payload 格式: orgID:userID:expiry
func GenerateToken(orgID, userID string, secret []byte, expiryDays int) string {
	expiry := time.Now().Unix() + int64(expiryDays)*secondsPerDay
	payload := fmt.Sprintf("%s:%s:%d", orgID, userID, expiry)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	raw := fmt.Sprintf("%s:%s", payload, hex.EncodeToString(sig))
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// ValidateToken 校验 token，返回 (valid, orgID, userID)
// 新格式: orgID:userID:expiry:sig（4 段）
// 旧格式: userID:expiry:sig（3 段，orgID 返回空串，调用方需 findOrg 兜底）
func ValidateToken(token string, secret []byte) (bool, string, string) {
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false, "", ""
	}

	// 新格式 4 段
	parts4 := strings.SplitN(string(data), ":", 4)
	if len(parts4) == 4 {
		orgID, userID, expiryStr, sigHex := parts4[0], parts4[1], parts4[2], parts4[3]
		if verifySig(orgID+":"+userID+":"+expiryStr, sigHex, secret) && checkExpiry(expiryStr) {
			return true, orgID, userID
		}
		return false, "", ""
	}

	// 旧格式 3 段（兼容期，旧 token 30 天内自然过期）
	parts3 := strings.SplitN(string(data), ":", 3)
	if len(parts3) == 3 {
		userID, expiryStr, sigHex := parts3[0], parts3[1], parts3[2]
		if verifySig(userID+":"+expiryStr, sigHex, secret) && checkExpiry(expiryStr) {
			return true, "", userID
		}
	}

	return false, "", ""
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

// AuthMiddleware 是 iris 鉴权中间件，校验失败直接返回 401，成功把 orgID + userID 写入 ctx.Values()
// findOrg 用于旧格式 token 兼容（orgID 为空时查 DB 补全），传 nil 则不补全
func AuthMiddleware(secret []byte, findOrg func(context.Context, string) (string, error)) iris.Handler {
	return func(ctx iris.Context) {
		token := extractTokenFromContext(ctx)
		if token == "" {
			writeFail(ctx, iris.StatusUnauthorized, CodeMissingToken)
			return
		}
		valid, orgID, uid := ValidateToken(token, secret)
		if !valid {
			writeFail(ctx, iris.StatusUnauthorized, CodeInvalidToken)
			return
		}
		// 旧格式 token 兼容：orgID 为空时查 DB 补全
		if orgID == "" && findOrg != nil {
			if oid, err := findOrg(ctx.Request().Context(), uid); err == nil && oid != "" {
				orgID = oid
			}
		}
		ctx.Values().Set("userID", uid)
		ctx.Values().Set("orgID", orgID)
		ctx.Next()
	}
}

// currentUserID 从 iris.Context 中取出 AuthMiddleware 写入的 userID
func currentUserID(ctx iris.Context) string {
	if v := ctx.Values().Get("userID"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// currentOrgID 从 iris.Context 中取出 AuthMiddleware 写入的 orgID
func currentOrgID(ctx iris.Context) string {
	if v := ctx.Values().Get("orgID"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
