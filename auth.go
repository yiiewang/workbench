package main

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

const tokenExpiryDays = 30

// loadOrCreateTokenSecret 从文件中加载或新建 token 签名秘钥
func loadOrCreateTokenSecret(serveDir string) ([]byte, error) {
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

// hashPassword 对密码进行 SHA-256 哈希
func hashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}

// generateToken 生成带过期时间的 HMAC token
// 格式：base64(userId:expiry:hmac_hex)
func generateToken(userID string, secret []byte) string {
	expiry := time.Now().Unix() + tokenExpiryDays*86400
	payload := fmt.Sprintf("%s:%d", userID, expiry)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	raw := fmt.Sprintf("%s:%s", payload, hex.EncodeToString(sig))
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// validateToken 校验 token，返回 (valid, userID)
func validateToken(token string, secret []byte) (bool, string) {
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

// extractToken 从请求头中提取 Bearer token
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// requireAuth 从请求中提取并校验 token，返回 userID。校验失败时直接写响应。
func requireAuth(w http.ResponseWriter, r *http.Request, secret []byte) (userID string, ok bool) {
	token := extractToken(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Missing token"})
		return "", false
	}
	valid, uid := validateToken(token, secret)
	if !valid {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or expired token"})
		return "", false
	}
	return uid, true
}
