package main

// 临时 UI 复现探针：真实网关 + 当前代码 + 最新嵌入前端，监听 127.0.0.1:18099。
// 用后即删（诊断 write_file 审批确认 UI 是否出现）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/server"
)

func main() {
	dir, _ := os.MkdirTemp("", "probeui")
	d, err := db.Open(filepath.Join(dir, "p.db"))
	if err != nil {
		panic(err)
	}
	staticDir := filepath.Join(dir, "static")
	_ = os.MkdirAll(filepath.Join(staticDir, "1", "1"), 0o755) // 预建用户目录

	// 建测试账号 agent/pass123（org=1 user=1）
	hash, err := server.HashPassword("pass123")
	if err != nil {
		panic(err)
	}
	orgID, err := d.EnsureOrg(context.Background(), "t")
	if err != nil {
		panic(err)
	}
	if _, err := d.UpsertUser(context.Background(), orgID, "agent", hash, db.RoleIDUser); err != nil {
		panic(err)
	}
	_ = os.MkdirAll(filepath.Join(staticDir, fmt.Sprint(orgID), "1"), 0o755)

	cfg := config.DefaultConfig()
	cfg.Server.StaticDir = staticDir
	cfg.AI.Chat = config.ChatConfig{
		Gateways: []config.ChatGateway{{
			ID: "default", Name: "Default",
			BaseURL: os.Getenv("AI_BASE_URL"),
			APIKey:  os.Getenv("AI_API_KEY"),
			Model:   os.Getenv("AI_MODEL"),
		}},
		Tools: true,
	}
	secret := []byte("probe-secret")
	srv, err := server.New(d, cfg, secret, filepath.Join(dir, "access.log"))
	if err != nil {
		panic(err)
	}
	fmt.Println("probe server: http://127.0.0.1:18099 (agent/pass123)")
	if err := srv.App().Listen("127.0.0.1:18099"); err != nil {
		panic(err)
	}
}
