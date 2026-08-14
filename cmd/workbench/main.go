// workbench 个人开发平台入口
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
	"github.com/yiiewang/workbench/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// parseLogLevel 将配置字符串转为 slog.Level，默认 info。
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// run 承载全部启动逻辑；返回 error 时 main 中的 slog.Error+os.Exit 才执行，
// 此时 run 内的 defer（如 database.Close）已正常释放，避免 exitAfterDefer。
func run() error {
	// 先用默认级别初始化结构化日志，配置加载后按 cfg.Logging.Level 调整
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	configPath := flag.String("config", "config.yaml", "path to config YAML file")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 按配置级别重建 logger
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(cfg.Logging.Level)})))

	// 环境变量覆盖
	config.ApplyEnv(cfg)

	// 解析路径
	staticDir := config.ResolvePath(cfg.Server.StaticDir)
	dbPath := config.ResolvePath(cfg.DB.Path)
	logDir := config.ResolvePath(cfg.Logging.Dir)

	// 确保目录存在
	for _, dir := range []string{staticDir, logDir, filepath.Dir(dbPath)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// 初始化数据库
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	// 加载 token 秘钥（存入 db，自动迁移旧 .token_secret 文件）
	tokenSecret, err := database.LoadOrCreateSecret(context.Background(), "token", filepath.Join(staticDir, ".token_secret"))
	if err != nil {
		return fmt.Errorf("load token secret: %w", err)
	}

	logFile := filepath.Join(logDir, "preview.log")

	// 初始化 HTTP Server
	srv, err := server.New(database, cfg, tokenSecret, logFile)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	app := srv.App()

	slog.Info("server started",
		"url", fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		"port", cfg.Server.Port,
		"static_dir", staticDir,
		"db", dbPath,
		"log_file", logFile,
	)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	if err := app.Run(iris.Addr(addr), iris.WithoutServerError(iris.ErrServerClosed)); err != nil {
		return fmt.Errorf("server run: %w", err)
	}
	return nil
}
