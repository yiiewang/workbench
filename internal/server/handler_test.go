package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kataras/iris/v12/httptest"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// setupTestServer 构建一个带临时 DB + 临时静态目录的 Server，返回 httptest.Expect。
func setupTestServer(t *testing.T) (*httptest.Expect, *db.DB, []byte, string) {
	t.Helper()
	// DB 用独立手动临时目录，避免异步访问日志 goroutine 占用 WAL 文件
	// 导致 t.TempDir 自动清理失败。
	dbDir, err := os.MkdirTemp("", "wbdb")
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
		// 等待异步访问日志 goroutine 短暂退出，再尽力清理（忽略残留 WAL 导致的错误）
		time.Sleep(10 * time.Millisecond)
		_ = os.RemoveAll(dbDir)
	})

	staticDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.StaticDir = staticDir
	secret := []byte("test-secret-key")

	srv, err := New(d, cfg, secret, filepath.Join(t.TempDir(), "access.log"))
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	// 限流已迁移至 DB（每测试独立 temp DB，无需跨测试重置）
	return httptest.New(t, srv.App()), d, secret, staticDir
}

// createUser 创建一个带 bcrypt 哈希的用户，返回可直接用的 token。
func createUser(t *testing.T, d *db.DB, secret []byte, orgID, userID, password string) string {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureOrg(context.Background(), orgID); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertUser(context.Background(), orgID, userID, hash); err != nil {
		t.Fatal(err)
	}
	return GenerateToken(userID, secret, 30)
}

func authHeader(token string) string { return "Bearer " + token }

// ============================================================
// 登录 / 设置密码
// ============================================================

func TestHandler_Login(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "pass123")

	// 正确密码 → 200 + token
	obj := e.POST("/api/login").WithJSON(map[string]string{
		"orgId": "org1", "userId": "alice", "password": "pass123",
	}).Expect().Status(http.StatusOK).JSON().Object()
	obj.Value("code").Equal(0)
	obj.Value("data").Object().Value("token").String().NotEqual("")
	obj.Value("data").Object().Value("user").Object().Value("userId").Equal("alice")

	// 错误密码 → 401
	e.POST("/api/login").WithJSON(map[string]string{
		"orgId": "org1", "userId": "alice", "password": "wrong",
	}).Expect().Status(http.StatusUnauthorized).
		JSON().Object().Value("code").Equal(CodeInvalidPassword)

	// 缺字段 → 400
	e.POST("/api/login").WithJSON(map[string]string{
		"orgId": "org1", "userId": "alice",
	}).Expect().Status(http.StatusBadRequest)

	// token 可用
	e.GET("/api/me").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)
}

func TestHandler_LoginUserWithoutPassword(t *testing.T) {
	e, d, _, _ := setupTestServer(t)
	_ = d.EnsureOrg(context.Background(), "org1")
	_ = d.UpsertUser(context.Background(), "org1", "bob", "")

	e.POST("/api/login").WithJSON(map[string]string{
		"orgId": "org1", "userId": "bob", "password": "anything",
	}).Expect().Status(http.StatusForbidden).
		JSON().Object().Value("code").Equal(CodePasswordNotSet)
}

func TestHandler_SetPassword_FirstInit(t *testing.T) {
	e, _, _, _ := setupTestServer(t)

	// 系统无任何用户时允许首次设置
	obj := e.POST("/api/set-password").WithJSON(map[string]string{
		"orgId": "org1", "userId": "admin", "newPassword": "secret123",
	}).Expect().Status(http.StatusOK).JSON().Object()
	obj.Value("code").Equal(0)
	obj.Value("data").Object().Value("token").NotNull()

	// 系统已有用户后禁止开放注册
	e.POST("/api/set-password").WithJSON(map[string]string{
		"orgId": "org1", "userId": "stranger", "newPassword": "secret123",
	}).Expect().Status(http.StatusForbidden).
		JSON().Object().Value("code").Equal(CodeUserNotFound)
}

// ============================================================
// /api/me 鉴权
// ============================================================

func TestHandler_Me(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	e.GET("/api/me").Expect().Status(http.StatusUnauthorized) // 无 token

	obj := e.GET("/api/me").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object()
	obj.Value("code").Equal(0)
	obj.Value("data").Object().Value("userId").Equal("alice")

	// 过期 token → 401
	expired := GenerateToken("alice", secret, -1)
	e.GET("/api/me").WithHeader("Authorization", authHeader(expired)).
		Expect().Status(http.StatusUnauthorized)
}

func TestHandler_Me_InvalidToken(t *testing.T) {
	e, _, _, _ := setupTestServer(t)
	e.GET("/api/me").WithHeader("Authorization", "Bearer garbage").
		Expect().Status(http.StatusUnauthorized)
}

// ============================================================
// /api/tasks
// ============================================================

func TestHandler_TasksJSON(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	e.GET("/api/tasks").Expect().Status(http.StatusUnauthorized) // 无 token

	e.GET("/api/tasks").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)

	body := map[string]any{
		"orgs": map[string]any{
			"org1": map[string]any{
				"alice": map[string]any{
					"tasks": []map[string]any{
						{"id": "t1", "title": "first", "status": "todo", "sortOrder": 1},
					},
					"version": map[string]string{"md5": "v1"},
				},
			},
		},
	}
	e.PUT("/api/tasks").WithHeader("Authorization", authHeader(tok)).
		WithJSON(body).Expect().Status(http.StatusOK).
		JSON().Object().Value("code").Equal(0)

	e.GET("/api/tasks").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("orgs").NotNull()
}

// ============================================================
// /api/tree
// ============================================================

func TestHandler_Tree(t *testing.T) {
	e, d, secret, staticDir := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	_ = os.WriteFile(filepath.Join(staticDir, "a.txt"), []byte("hello"), 0644)
	_ = os.MkdirAll(filepath.Join(staticDir, "sub"), 0755)

	obj := e.GET("/api/tree").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object()
	obj.Value("code").Equal(0)
	obj.Value("data").Object().Value("files").Array().NotEmpty()
	obj.Value("data").Object().Value("dirs").Array().NotEmpty()
}

// ============================================================
// 分享 CRUD
// ============================================================

func TestHandler_ShareCreateAccessDelete(t *testing.T) {
	e, d, secret, staticDir := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")
	_ = os.WriteFile(filepath.Join(staticDir, "doc.md"), []byte("# hi"), 0644)

	// 无 token → 401
	e.POST("/api/share").WithJSON(map[string]string{"resourcePath": "/doc.md"}).
		Expect().Status(http.StatusUnauthorized)

	// 创建分享
	shareToken := e.POST("/api/share").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]string{"resourcePath": "/doc.md"}).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("token").String().Raw()

	// 公开访问
	e.GET("/api/share/" + shareToken).Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("fileName").Equal("doc.md")

	// 取 id 撤销
	id := e.GET("/api/share").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("shares").Array().First().Object().Value("id").String().Raw()

	e.DELETE("/api/share/"+id).WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)

	// 撤销后访问 → 404
	e.GET("/api/share/" + shareToken).Expect().Status(http.StatusNotFound)
}

func TestHandler_ShareWithPassword(t *testing.T) {
	e, d, secret, staticDir := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")
	_ = os.WriteFile(filepath.Join(staticDir, "secret.md"), []byte("top"), 0644)

	shareToken := e.POST("/api/share").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]string{"resourcePath": "/secret.md", "password": "pw"}).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("token").String().Raw()

	// 无密码 → 401 PasswordRequired
	e.GET("/api/share/" + shareToken).Expect().Status(http.StatusUnauthorized).
		JSON().Object().Value("code").Equal(CodePasswordRequired)

	// 错误密码 → 401
	e.POST("/api/share/" + shareToken).WithJSON(map[string]string{"password": "wrong"}).
		Expect().Status(http.StatusUnauthorized).JSON().Object().Value("code").Equal(CodeInvalidSharePwd)

	// 正确密码 → 200
	e.POST("/api/share/" + shareToken).WithJSON(map[string]string{"password": "pw"}).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)
}

func TestHandler_ShareExpired(t *testing.T) {
	e, d, secret, staticDir := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")
	_ = os.WriteFile(filepath.Join(staticDir, "f.md"), []byte("x"), 0644)

	shareToken := e.POST("/api/share").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]string{"resourcePath": "/f.md", "expiresAt": "2000-01-01T00:00:00Z"}).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("token").String().Raw()

	e.GET("/api/share/" + shareToken).Expect().Status(http.StatusGone).
		JSON().Object().Value("code").Equal(CodeShareExpired)
}

func TestHandler_ShareNotYetEffective(t *testing.T) {
	e, d, secret, staticDir := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")
	_ = os.WriteFile(filepath.Join(staticDir, "f.md"), []byte("x"), 0644)

	shareToken := e.POST("/api/share").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]string{"resourcePath": "/f.md", "effectiveAt": "2999-01-01T00:00:00Z"}).
		Expect().Status(http.StatusOK).JSON().Object().Value("data").Object().Value("token").String().Raw()

	e.GET("/api/share/" + shareToken).Expect().Status(http.StatusForbidden).
		JSON().Object().Value("code").Equal(CodeShareNotEffective)
}

// ============================================================
// /api/org-members, /api/map, /api/stats
// ============================================================

func TestHandler_OrgMembers(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	e.GET("/api/org-members").WithQuery("orgId", "org1").
		WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("members").Array().NotEmpty()
}

func TestHandler_MapAndStats(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")
	_ = d

	e.GET("/api/map").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)
	e.GET("/api/stats").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)
}
