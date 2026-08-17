package server

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestGenerateAndValidateToken(t *testing.T) {
	secret := []byte("test-secret")
	tok := GenerateToken("org1", "alice", secret, 30)
	if tok == "" {
		t.Fatal("empty token")
	}
	ok, orgID, uid := ValidateToken(tok, secret)
	if !ok || orgID != "org1" || uid != "alice" {
		t.Fatalf("validate = %v,%q,%q want true,org1,alice", ok, orgID, uid)
	}
}

func TestValidateToken_Expired(t *testing.T) {
	secret := []byte("s")
	// expiryDays=-1 → 过期一天
	tok := GenerateToken("org1", "bob", secret, -1)
	if ok, _, _ := ValidateToken(tok, secret); ok {
		t.Fatal("expired token should be invalid")
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	tok := GenerateToken("org1", "alice", []byte("secret-a"), 30)
	if ok, _, _ := ValidateToken(tok, []byte("secret-b")); ok {
		t.Fatal("token verified with wrong secret should be invalid")
	}
}

func TestValidateToken_Tampered(t *testing.T) {
	secret := []byte("s")
	tok := GenerateToken("org1", "alice", secret, 30)
	// 篡改尾部签名
	tampered := tok[:len(tok)-4] + "AAAA"
	if ok, _, _ := ValidateToken(tampered, secret); ok {
		t.Fatal("tampered token should be invalid")
	}
}

func TestValidateToken_Malformed(t *testing.T) {
	secret := []byte("s")
	for _, bad := range []string{"", "not-base64!!!", "####", "aGVsbG8="} {
		// aGVsbG8= = "hello"，分割后不足 3 段
		if ok, _, _ := ValidateToken(bad, secret); ok {
			t.Fatalf("malformed token %q should be invalid", bad)
		}
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "hunter2") {
		t.Fatal("correct password should verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password should not verify")
	}
	// bcrypt 加盐：同一密码两次哈希应不同
	h2, _ := HashPassword("hunter2")
	if h2 == hash {
		t.Fatal("bcrypt should produce different hashes due to salt")
	}
}

func TestVerifyPassword_LegacySHA256(t *testing.T) {
	// 兼容旧 SHA-256 无盐哈希（登录后改密即升级 bcrypt）
	h := sha256.Sum256([]byte("hunter2"))
	legacy := hex.EncodeToString(h[:])
	if !VerifyPassword(legacy, "hunter2") {
		t.Fatal("legacy sha256 hash should verify")
	}
	if VerifyPassword(legacy, "nope") {
		t.Fatal("legacy wrong password should not verify")
	}
}
