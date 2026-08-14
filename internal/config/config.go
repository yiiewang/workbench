// Package config 统一配置加载，支持 YAML 格式
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 应用总配置
type Config struct {
	Server  ServerConfig      `yaml:"server"`
	DB      DBConfig          `yaml:"database"`
	Auth    AuthConfig        `yaml:"auth"`
	Routes  map[string]string `yaml:"routes"`
	Logging LogConfig         `yaml:"logging"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Port         int      `yaml:"port"`
	StaticDir    string   `yaml:"static_dir"`
	Hidden       []string `yaml:"hidden"`
	AllowSymlink bool     `yaml:"allow_symlink"`
}

// DBConfig 数据库配置
type DBConfig struct {
	Path string `yaml:"path"`
}

// AuthConfig 鉴权配置
type AuthConfig struct {
	TokenExpiryDays int `yaml:"token_expiry_days"`
}

// LogConfig 日志配置
type LogConfig struct {
	Dir   string `yaml:"dir"`
	Level string `yaml:"level"` // debug | info | warn | error，默认 info
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:      8080,
			StaticDir: "./static",
			Hidden:    []string{".*", "*.zip", "*.tar", "*.gz", "*.tgz", "*.rar", "*.7z", "*.bz2", "*.xz"},
		},
		DB: DBConfig{
			Path: "./data/workbench.db",
		},
		Auth: AuthConfig{
			TokenExpiryDays: 30,
		},
		Routes: map[string]string{
			"/todo":      "/todo.html",
			"/index":     "__listdir__",
			"/todo.html": "Todo Board",
		},
		Logging: LogConfig{
			Dir:   "~/.local/state/workbench",
			Level: "info",
		},
	}
}

// Load 从 YAML 文件加载配置，未设置的字段使用默认值
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	// 先用用户配置覆盖默认值
	fileCfg := &Config{}
	if err := yaml.Unmarshal(data, fileCfg); err != nil {
		return nil, err
	}

	mergeConfig(cfg, fileCfg)
	return cfg, nil
}

// mergeConfig 将 src 中的非零值合并到 dst
func mergeConfig(dst, src *Config) {
	if src.Server.Port != 0 {
		dst.Server.Port = src.Server.Port
	}
	if src.Server.StaticDir != "" {
		dst.Server.StaticDir = src.Server.StaticDir
	}
	if src.Server.Hidden != nil {
		dst.Server.Hidden = src.Server.Hidden
	}
	if src.DB.Path != "" {
		dst.DB.Path = src.DB.Path
	}
	if src.Auth.TokenExpiryDays != 0 {
		dst.Auth.TokenExpiryDays = src.Auth.TokenExpiryDays
	}
	if src.Logging.Dir != "" {
		dst.Logging.Dir = src.Logging.Dir
	}
	if src.Logging.Level != "" {
		dst.Logging.Level = src.Logging.Level
	}
	if src.Routes != nil {
		dst.Routes = src.Routes
	}
}

// ResolvePath 展开路径中的 ~ 为 HOME 目录
func ResolvePath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}
	return p
}

// ApplyEnv 用环境变量覆盖配置（PORT, STATIC_DIR, DB_PATH, LOG_DIR, ALLOW_SYMLINK）
func ApplyEnv(cfg *Config) {
	if v := os.Getenv("PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil && port > 0 {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("STATIC_DIR"); v != "" {
		cfg.Server.StaticDir = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DB.Path = v
	}
	if v := os.Getenv("LOG_DIR"); v != "" {
		cfg.Logging.Dir = v
	}
	// ALLOW_SYMLINK=true 开启符号链接支持（默认关闭，严格校验资源路径必须在 static_dir 下）
	if v := os.Getenv("ALLOW_SYMLINK"); v == "true" || v == "1" {
		cfg.Server.AllowSymlink = true
	}
}
