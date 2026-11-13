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
	Host     string `yaml:"host"`      // SMTP 服务器，如 smtp.qq.com
	Port     int    `yaml:"port"`      // 587（STARTTLS；465 出站常被云厂商干预）
	User     string `yaml:"user"`      // SMTP 认证用户名（仅登录鉴权用，QQ 邮箱为完整地址）
	From     string `yaml:"from"`      // 系统发件邮箱（收件人看到的发件地址）
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

// AIConfig AI 配置：对话与向量模型两个独立子段——两者常指向不同服务
// （如聊天走 OpenAI 兼容网关、向量走自部署 Qwen3-Embedding）。
type AIConfig struct {
	Chat      ChatConfig      `yaml:"chat"`
	Embedding EmbeddingConfig `yaml:"embedding"`
}

// ChatConfig 聊天模型配置（OpenAI 兼容 /chat/completions 协议）
type ChatConfig struct {
	BaseURL string `yaml:"base_url"` // 兼容端点，如 https://api.openai.com/v1
	APIKey  string `yaml:"api_key"`  // 后端持有，不下发前端
	Model   string `yaml:"model"`    // 模型名，如 gpt-4o-mini
	// Tools 开启 AI 工具调用（Agent 模式）：模型可调用 list_dir / read_file /
	// list_tasks / create_task 四个内置工具，由后端执行并回传工具结果。
	// 要求上游模型支持 function calling。Env: AI_TOOLS=true 开启
	Tools bool `yaml:"tools"`
}

// Enabled 聊天功能是否可用（key + base_url 均已配置才视为开启）
func (c ChatConfig) Enabled() bool {
	return c.APIKey != "" && c.BaseURL != ""
}

// Enabled AI 对话功能是否可用（委托 Chat 子段）
func (a AIConfig) Enabled() bool {
	return a.Chat.Enabled()
}

// EmbeddingConfig 向量模型配置（OpenAI 兼容 /embeddings 协议）。
// base_url + model 均配置才启用 RAG；api_key 对本地部署（vLLM/Ollama）可留空。
type EmbeddingConfig struct {
	BaseURL string `yaml:"base_url"` // 如 http://localhost:8001/v1
	APIKey  string `yaml:"api_key"`  // 后端持有，不下发前端；本地部署可空
	Model   string `yaml:"model"`    // 如 Qwen3-Embedding-8B
	// Dim 向量维度：建 vec0 虚拟表时固定，改动后需全量重建索引。
	// Qwen3-Embedding 系列原生 4096 维、支持 Matryoshka 降维，默认 1024
	// （精度损失很小，存储与 KNN 扫描量降为 1/4）。Env: AI_EMBEDDING_DIM
	Dim int `yaml:"dim"`
	// MaxDistance 相关度阈值（vec0 L2 距离，向量已归一化）：命中距离超过它
	// 视为无关直接丢弃——KNN 没有"不相关"概念，问天气也会返回距离最近的
	// 5 个垃圾片段。归一化下 L2=0 同向、1 正交（无关）、2 反向；但中文
	// embedding 空间各向异性，无关文本实测距离普遍 <1.0，故默认收紧到 0.9
	// （≈余弦相似度 0.6）。依据 "rag: context injected" 日志的 distance
	// 观测实际分布后微调。Env: AI_EMBEDDING_MAX_DISTANCE
	MaxDistance float64 `yaml:"max_distance"`
	// JudgeModel LLM 相关性预判模型（可选，flash 级小模型如 deepseek-flash）：
	// 距离阈值过滤后仍有命中时，把问题+候选片段一次调用批量判相关性，
	// 拦住阈值拦不住的语义无关命中。复用本网关的 /chat/completions；
	// 留空 = 关闭（只用距离阈值）。判断失败/超时降级保留阈值结果。
	// Env: AI_EMBEDDING_JUDGE_MODEL
	JudgeModel string `yaml:"judge_model"`
	// DigestModel 文件级摘要生成模型（可选，flash 级小模型）：索引时为每个
	// 文件生成语义摘要卡（是什么/核心内容/关键点，~100 字），作为特殊块
	// （chunk_index=-1）与原文块一起向量化——宏观问题命中摘要卡（token 极省、
	// 无噪音），细节问题继续走原文块。文件内容变化时才重新生成（幂等）；
	// 生成失败降级只索引原文块。复用本网关。Env: AI_EMBEDDING_DIGEST_MODEL
	DigestModel string `yaml:"digest_model"`
}

// Enabled 向量功能（RAG）是否可用
func (e EmbeddingConfig) Enabled() bool {
	return e.BaseURL != "" && e.Model != ""
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
			Chat: ChatConfig{
				BaseURL: "https://api.openai.com/v1",
				Model:   "gpt-4o-mini",
			},
			Embedding: EmbeddingConfig{
				Dim:         1024,
				MaxDistance: 0.9,
			},
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
	if src.AI.Chat.BaseURL != "" {
		dst.AI.Chat.BaseURL = src.AI.Chat.BaseURL
	}
	if src.AI.Chat.APIKey != "" {
		dst.AI.Chat.APIKey = src.AI.Chat.APIKey
	}
	if src.AI.Chat.Model != "" {
		dst.AI.Chat.Model = src.AI.Chat.Model
	}
	if src.AI.Chat.Tools {
		dst.AI.Chat.Tools = true
	}
	if src.AI.Embedding.BaseURL != "" {
		dst.AI.Embedding.BaseURL = src.AI.Embedding.BaseURL
	}
	if src.AI.Embedding.APIKey != "" {
		dst.AI.Embedding.APIKey = src.AI.Embedding.APIKey
	}
	if src.AI.Embedding.Model != "" {
		dst.AI.Embedding.Model = src.AI.Embedding.Model
	}
	if src.AI.Embedding.Dim != 0 {
		dst.AI.Embedding.Dim = src.AI.Embedding.Dim
	}
	if src.AI.Embedding.MaxDistance != 0 {
		dst.AI.Embedding.MaxDistance = src.AI.Embedding.MaxDistance
	}
	if src.AI.Embedding.JudgeModel != "" {
		dst.AI.Embedding.JudgeModel = src.AI.Embedding.JudgeModel
	}
	if src.AI.Embedding.DigestModel != "" {
		dst.AI.Embedding.DigestModel = src.AI.Embedding.DigestModel
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
		cfg.AI.Chat.BaseURL = v
	}
	if v := os.Getenv("AI_API_KEY"); v != "" {
		cfg.AI.Chat.APIKey = v
	}
	if v := os.Getenv("AI_MODEL"); v != "" {
		cfg.AI.Chat.Model = v
	}
	// AI_TOOLS=true 开启工具调用（Agent 模式），默认关闭保持纯对话行为
	if v := os.Getenv("AI_TOOLS"); v == "true" || v == "1" {
		cfg.AI.Chat.Tools = true
	}
	// 向量模型（RAG）配置，独立于聊天网关：AI_EMBEDDING_BASE_URL / AI_EMBEDDING_API_KEY /
	// AI_EMBEDDING_MODEL / AI_EMBEDDING_DIM
	if v := os.Getenv("AI_EMBEDDING_BASE_URL"); v != "" {
		cfg.AI.Embedding.BaseURL = v
	}
	if v := os.Getenv("AI_EMBEDDING_API_KEY"); v != "" {
		cfg.AI.Embedding.APIKey = v
	}
	if v := os.Getenv("AI_EMBEDDING_MODEL"); v != "" {
		cfg.AI.Embedding.Model = v
	}
	if v := os.Getenv("AI_EMBEDDING_DIM"); v != "" {
		var dim int
		if _, err := fmt.Sscanf(v, "%d", &dim); err == nil && dim > 0 {
			cfg.AI.Embedding.Dim = dim
		}
	}
	if v := os.Getenv("AI_EMBEDDING_MAX_DISTANCE"); v != "" {
		var d float64
		if _, err := fmt.Sscanf(v, "%g", &d); err == nil && d > 0 {
			cfg.AI.Embedding.MaxDistance = d
		}
	}
	if v := os.Getenv("AI_EMBEDDING_JUDGE_MODEL"); v != "" {
		cfg.AI.Embedding.JudgeModel = v
	}
	if v := os.Getenv("AI_EMBEDDING_DIGEST_MODEL"); v != "" {
		cfg.AI.Embedding.DigestModel = v
	}
}
