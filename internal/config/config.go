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
	Server   ServerConfig      `yaml:"server"`
	DB       DBConfig          `yaml:"database"`
	Auth     AuthConfig        `yaml:"auth"`
	Routes   map[string]string `yaml:"routes"`
	Logging  LogConfig         `yaml:"logging"`
	AI       AIConfig          `yaml:"ai"`
	Schedule ScheduleConfig    `yaml:"schedule"`
	Notify   NotifyConfig      `yaml:"notify"`
}

// ScheduleConfig 定时调度配置（schedule 包：cron 引擎 + 事件总线）
type ScheduleConfig struct {
	// Enabled 开启定时任务（关闭 = 调度器不启动，不影响其他功能）
	Enabled bool `yaml:"enabled"`
	// Timezone cron 表达式求值时区（IANA 名，如 Asia/Shanghai；缺省 UTC）
	Timezone string `yaml:"timezone"`
	// TodoDueCron 到期扫描 cron 表达式（5 段），缺省 "*/5 * * * *"
	TodoDueCron string `yaml:"todo_due_cron"`
}

// NotifyConfig 通知渠道配置（订阅 schedule 事件总线）
type NotifyConfig struct {
	Email EmailNotifyConfig `yaml:"email"`
}

// EmailNotifyConfig 邮箱通知配置。
// SMTP 凭据经环境变量 EMAIL_PASS 注入，不落配置文件。
type EmailNotifyConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Host     string `yaml:"host"` // SMTP 服务器，如 smtp.qq.com
	Port     int    `yaml:"port"` // 587（STARTTLS；465 出站常被云厂商干预）
	User     string `yaml:"user"` // SMTP 认证用户名（仅登录鉴权用，QQ 邮箱为完整地址）
	From     string `yaml:"from"` // 系统发件邮箱（收件人看到的发件地址）
	FromName string `yaml:"from_name"` // 发件人显示名，缺省 "Workbench"
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
	// AdminSetupToken 首次初始化引导码：系统无任何用户时，首个 set-password 请求
	// 必须携带匹配的 setupToken 才能成为超管，消除"部署到注册之间"的抢占窗口。
	// 留空 = 保持旧行为（先到先得，启动日志会告警）。建议经 ADMIN_SETUP_TOKEN 环境变量注入。
	AdminSetupToken string `yaml:"admin_setup_token"`
}

// LogConfig 日志配置
type LogConfig struct {
	Dir   string `yaml:"dir"`
	Level string `yaml:"level"` // debug | info | warn | error，默认 info
}

// AIConfig AI 对话配置（OpenAI 兼容协议：OpenAI / DeepSeek / Ollama 网关等均可）
type AIConfig struct {
	BaseURL string `yaml:"base_url"` // 兼容端点，如 https://api.openai.com/v1
	APIKey  string `yaml:"api_key"`  // 后端持有，不下发前端
	Model   string `yaml:"model"`    // 模型名，如 gpt-4o-mini
	// Tools 开启 AI 工具调用（Agent 模式）：模型可调用 list_dir / read_file /
	// list_tasks / create_task 四个内置工具，由后端执行并回传结果。
	// 要求上游模型支持 function calling。Env: AI_TOOLS=true 开启
	Tools bool `yaml:"tools"`
}

// Enabled AI 功能是否可用（key + base_url 均已配置才视为开启）
func (a AIConfig) Enabled() bool {
	return a.APIKey != "" && a.BaseURL != ""
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
		AI: AIConfig{
			BaseURL: "https://api.openai.com/v1",
			Model:   "gpt-4o-mini",
		},
		Schedule: ScheduleConfig{
			Timezone:    "Asia/Shanghai",
			TodoDueCron: "*/5 * * * *",
		},
		Notify: NotifyConfig{
			Email: EmailNotifyConfig{
				Host: "smtp.qq.com",
				Port: 587,
			},
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
	if src.Auth.AdminSetupToken != "" {
		dst.Auth.AdminSetupToken = src.Auth.AdminSetupToken
	}
	if src.Logging.Dir != "" {
		dst.Logging.Dir = src.Logging.Dir
	}
	if src.Logging.Level != "" {
		dst.Logging.Level = src.Logging.Level
	}
	if src.AI.BaseURL != "" {
		dst.AI.BaseURL = src.AI.BaseURL
	}
	if src.AI.APIKey != "" {
		dst.AI.APIKey = src.AI.APIKey
	}
	if src.AI.Model != "" {
		dst.AI.Model = src.AI.Model
	}
	if src.AI.Tools {
		dst.AI.Tools = true
	}
	if src.Schedule.Enabled {
		dst.Schedule.Enabled = true
	}
	if src.Schedule.Timezone != "" {
		dst.Schedule.Timezone = src.Schedule.Timezone
	}
	if src.Schedule.TodoDueCron != "" {
		dst.Schedule.TodoDueCron = src.Schedule.TodoDueCron
	}
	if src.Notify.Email.Host != "" {
		dst.Notify.Email.Host = src.Notify.Email.Host
	}
	if src.Notify.Email.Port != 0 {
		dst.Notify.Email.Port = src.Notify.Email.Port
	}
	if src.Notify.Email.User != "" {
		dst.Notify.Email.User = src.Notify.Email.User
	}
	if src.Notify.Email.From != "" {
		dst.Notify.Email.From = src.Notify.Email.From
	}
	if src.Notify.Email.FromName != "" {
		dst.Notify.Email.FromName = src.Notify.Email.FromName
	}
	if src.Notify.Email.Enabled {
		dst.Notify.Email.Enabled = true
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

// ApplyEnv 用环境变量覆盖配置（PORT, STATIC_DIR, DB_PATH, LOG_DIR, ALLOW_SYMLINK, ADMIN_SETUP_TOKEN, AI_*）
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
	// ADMIN_SETUP_TOKEN 首次初始化引导码（一次性使用：系统有用户后不再校验）。
	// 不落配置文件，部署时注入：openssl rand -hex 16
	if v := os.Getenv("ADMIN_SETUP_TOKEN"); v != "" {
		cfg.Auth.AdminSetupToken = v
	}
	if v := os.Getenv("AI_BASE_URL"); v != "" {
		cfg.AI.BaseURL = v
	}
	if v := os.Getenv("AI_API_KEY"); v != "" {
		cfg.AI.APIKey = v
	}
	if v := os.Getenv("AI_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	// AI_TOOLS=true 开启工具调用（Agent 模式），默认关闭保持纯对话行为
	if v := os.Getenv("AI_TOOLS"); v == "true" || v == "1" {
		cfg.AI.Tools = true
	}
}
