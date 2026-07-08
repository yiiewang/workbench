// Package server HTTP 鉴权逻辑
package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// HashPassword 对密码进行 SHA-256 哈希
func HashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
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

// ExtractToken 从请求头提取 Bearer token
func ExtractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// RequireAuth 校验请求中的 token，失败时直接写 401 响应
func RequireAuth(w http.ResponseWriter, r *http.Request, secret []byte) (userID string, ok bool) {
	token := ExtractToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Missing token"})
		return "", false
	}
	valid, uid := ValidateToken(token, secret)
	if !valid {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
		return "", false
	}
	return uid, true
}
