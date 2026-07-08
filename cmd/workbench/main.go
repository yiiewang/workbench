// workbench 个人开发平台入口
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config YAML file")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config failed, err=%v", err)
	}

	// 环境变量覆盖
	config.ApplyEnv(cfg)

	// 解析路径
	staticDir := config.ResolvePath(cfg.Server.StaticDir)
	dbPath := config.ResolvePath(cfg.DB.Path)
	logDir := config.ResolvePath(cfg.Logging.Dir)

	// 确保目录存在
	os.MkdirAll(staticDir, 0755)
	os.MkdirAll(logDir, 0755)
	os.MkdirAll(filepath.Dir(dbPath), 0755)

	// 初始化数据库
	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("open database failed, err=%v", err)
	}
	defer database.Close()

	// 加载 token 秘钥
	tokenSecret, err := server.LoadOrCreateTokenSecret(staticDir)
	if err != nil {
		log.Fatalf("load token secret failed, err=%v", err)
	}

	logFile := filepath.Join(logDir, "preview.log")

	// 初始化 HTTP Server
	srv, err := server.New(database, cfg, tokenSecret, logFile)
	if err != nil {
		log.Fatalf("create server failed, err=%v", err)
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)

	// 优雅退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: srv.Handler(),
	}

	go func() {
		<-sigCh
		log.Println("server shutting down...")
		httpServer.Close()
	}()

	log.Printf("Workbench started on http://localhost:%d", cfg.Server.Port)
	log.Printf("Serving directory: %s", staticDir)
	log.Printf("Database: %s", dbPath)
	log.Printf("Visit stats: http://localhost:%d/__stats__", cfg.Server.Port)
	log.Printf("Log file: %s", logFile)
	log.Println("Press Ctrl+C to stop")

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed, err=%v", err)
	}
	log.Println("server stopped")
}
