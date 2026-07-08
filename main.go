package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	// 解析命令行参数
	serveDir := filepath.Join(baseDir(), "html")
	if len(os.Args) > 1 {
		serveDir = os.Args[1]
	}
	serveDir, _ = filepath.Abs(serveDir)

	// 端口（兼容 PORT 环境变量）
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 日志目录
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = filepath.Join(os.Getenv("HOME"), ".local", "state", "workbench")
	}

	// 确保目录存在
	ensureDir(serveDir)
	ensureDir(logDir)

	// 加载路由配置
	routeConfig := loadRouteConfig(serveDir)

	// 初始化数据库，db 文件放在 serveDir 下
	db, err := OpenDB(serveDir)
	if err != nil {
		log.Fatalf("open database failed, err=%v", err)
	}
	defer db.Close()

	// 加载 token 秘钥
	tokenSecret, err := loadOrCreateTokenSecret(serveDir)
	if err != nil {
		log.Fatalf("load token secret failed, err=%v", err)
	}

	logFile := filepath.Join(logDir, "preview.log")

	srv := NewServer(db, serveDir, routeConfig, tokenSecret, logFile)

	// 优雅退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: srv.Handler(),
	}

	go func() {
		<-sigCh
		log.Println("server shutting down...")
		server.Close()
	}()

	log.Printf("Workbench started on http://localhost:%s", port)
	log.Printf("Serving directory: %s", serveDir)
	log.Printf("Visit stats: http://localhost:%s/__stats__", port)
	log.Printf("Log file: %s", logFile)
	log.Println("Press Ctrl+C to stop")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed, err=%v", err)
	}
	log.Println("server stopped")
}

// baseDir 返回可执行文件所在目录
func baseDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// loadRouteConfig 从 config.json 加载路由配置
func loadRouteConfig(serveDir string) map[string]string {
	configPath := filepath.Join(serveDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("config.json not found, using empty route config, path=%s", configPath)
		return map[string]string{}
	}

	var cfg struct {
		RouteConfig map[string]string `json:"route_config"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("parse config.json failed, err=%v", err)
		return map[string]string{}
	}

	return cfg.RouteConfig
}
