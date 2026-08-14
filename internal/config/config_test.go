package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Port != 8080 {
		t.Fatalf("port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Auth.TokenExpiryDays != 30 {
		t.Fatalf("expiry = %d, want 30", cfg.Auth.TokenExpiryDays)
	}
	if cfg.Logging.Level != "info" {
		t.Fatalf("level = %q, want info", cfg.Logging.Level)
	}
	if len(cfg.Server.Hidden) == 0 {
		t.Fatal("default Hidden should be non-empty")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("missing file port = %d, want default 8080", cfg.Server.Port)
	}
}

func TestLoad_YAMLMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	content := `
server:
  port: 9090
  static_dir: /var/www
database:
  path: /data/x.db
auth:
  token_expiry_days: 7
logging:
  level: debug
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9090 {
		t.Fatalf("port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.DB.Path != "/data/x.db" {
		t.Fatalf("db path = %q", cfg.DB.Path)
	}
	if cfg.Auth.TokenExpiryDays != 7 {
		t.Fatalf("expiry = %d, want 7", cfg.Auth.TokenExpiryDays)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("level = %q, want debug", cfg.Logging.Level)
	}
	// 未设置字段保留默认（Hidden 不被覆盖）
	if len(cfg.Server.Hidden) == 0 {
		t.Fatal("Hidden should keep default when unset in YAML")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	// 未闭合的 flow 序列，yaml.v3 应报错
	_ = os.WriteFile(path, []byte("server:\n  port: [1, 2\n"), 0600)
	if _, err := Load(path); err == nil {
		t.Fatal("invalid yaml should error")
	}
}

func TestResolvePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ResolvePath("~/foo/bar"); got != filepath.Join(home, "foo", "bar") {
		t.Fatalf("ResolvePath ~/ = %q", got)
	}
	if got := ResolvePath("/abs/path"); got != "/abs/path" {
		t.Fatalf("ResolvePath /abs = %q, want /abs/path", got)
	}
	// 相对路径透传
	if got := ResolvePath("rel/path"); got != "rel/path" {
		t.Fatalf("ResolvePath rel = %q", got)
	}
}

func TestApplyEnv(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("PORT", "12345")
	t.Setenv("STATIC_DIR", "/srv/static")
	t.Setenv("DB_PATH", "/srv/db.sqlite")
	t.Setenv("LOG_DIR", "/srv/logs")
	t.Setenv("ALLOW_SYMLINK", "true")
	ApplyEnv(cfg)

	if cfg.Server.Port != 12345 {
		t.Fatalf("port = %d, want 12345", cfg.Server.Port)
	}
	if cfg.Server.StaticDir != "/srv/static" {
		t.Fatalf("static = %q", cfg.Server.StaticDir)
	}
	if cfg.DB.Path != "/srv/db.sqlite" {
		t.Fatalf("db = %q", cfg.DB.Path)
	}
	if cfg.Logging.Dir != "/srv/logs" {
		t.Fatalf("log dir = %q", cfg.Logging.Dir)
	}
	if !cfg.Server.AllowSymlink {
		t.Fatal("AllowSymlink should be true")
	}
}

func TestApplyEnv_InvalidPort(t *testing.T) {
	t.Setenv("PORT", "abc")
	cfg := DefaultConfig()
	ApplyEnv(cfg)
	if cfg.Server.Port != 8080 {
		t.Fatalf("invalid port should keep default, got %d", cfg.Server.Port)
	}
}

func TestApplyEnv_AllowSymlinkVariants(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"1":     true,
		"false": false,
		"":      false,
	}
	for val, want := range cases {
		t.Run(val, func(t *testing.T) {
			t.Setenv("ALLOW_SYMLINK", val)
			cfg := DefaultConfig()
			ApplyEnv(cfg)
			if cfg.Server.AllowSymlink != want {
				t.Fatalf("ALLOW_SYMLINK=%q -> %v, want %v", val, cfg.Server.AllowSymlink, want)
			}
		})
	}
}
