package server

// 分享目录浏览：子路径解析与沙箱边界（safeJoin 复用后的回归基线）。

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestHandler_ShareDirSubPath(t *testing.T) {
	e, d, secret, staticDir := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")
	root := userTestRoot(staticDir, "org1", "alice")
	if err := os.MkdirAll(filepath.Join(root, "docs", "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.md"), []byte("# a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "sub", "b.md"), []byte("# b"), 0644); err != nil {
		t.Fatal(err)
	}
	// 分享根之外的文件：任何子路径都不应读到
	if err := os.WriteFile(filepath.Join(root, "outside.md"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	shareToken := e.POST("/api/share").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]string{"resourcePath": "/docs"}).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("token").String().Raw()

	// 完整用户路径形式的子路径
	e.GET("/api/share/"+shareToken).WithQuery("path", "/docs/sub").Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("relPath").Equal("sub")
	// 相对分享根的子路径
	e.GET("/api/share/"+shareToken).WithQuery("path", "/sub").Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("relPath").Equal("sub")
	// 子路径下的文件
	e.GET("/api/share/"+shareToken).WithQuery("path", "/docs/a.md").Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("fileName").Equal("a.md")

	// 越界：跳出分享根的文件不可访问
	for _, p := range []string{"../outside.md", "/../../outside.md", "/docs/../outside.md"} {
		e.GET("/api/share/"+shareToken).WithQuery("path", p).Expect().Status(http.StatusNotFound)
	}
}
