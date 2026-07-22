// Package server HTTP 鉴权逻辑
package server

import (
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

// GenerateToken 生成带过期时间的 HMAC token
func GenerateToken(userID string, secret []byte, expiryDays int) string {
	expiry := time.Now().Unix() + int64(expiryDays)*86400
	payload := fmt.Sprintf("%s:%d", userID, expiry)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	raw := fmt.Sprintf("%s:%s", payload, hex.EncodeToString(sig))
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// ValidateToken 校验 token
func ValidateToken(token string, secret []byte) (bool, string) {
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false, ""
	}
	parts := strings.SplitN(string(data), ":", 3)
	if len(parts) != 3 {
		return false, ""
	}
	userID, expiryStr, sigHex := parts[0], parts[1], parts[2]

	expiry := int64(0)
	fmt.Sscanf(expiryStr, "%d", &expiry)
	if time.Now().Unix() > expiry {
		return false, ""
	}

	payload := fmt.Sprintf("%s:%s", userID, expiryStr)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sigHex), []byte(expectedSig)) {
		return false, ""
	}
	return true, userID
}

// extractTokenFromContext 从 iris.Context 请求头提取 Bearer token
func extractTokenFromContext(ctx iris.Context) string {
	auth := ctx.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// AuthMiddleware 是 iris 鉴权中间件，校验失败直接返回 401，成功把 userID 写入 ctx.Values()
func AuthMiddleware(secret []byte) iris.Handler {
	return func(ctx iris.Context) {
		token := extractTokenFromContext(ctx)
		if token == "" {
			writeJSON(ctx, iris.StatusUnauthorized, map[string]string{"error": "Missing token"})
			return
		}
		valid, uid := ValidateToken(token, secret)
		if !valid {
			writeJSON(ctx, iris.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
			return
		}
		ctx.Values().Set("userID", uid)
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
