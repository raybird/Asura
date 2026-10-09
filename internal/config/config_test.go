package config

// 2026-10-09：issue-45 Task 3.1，operator config 嚴格載入與 XDG 路徑解析（DIST-010）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secret = "SECRET-SENTINEL-7f3a"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultPaths(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Paths
		err  string
	}{
		{"xdg", map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/xc", "XDG_DATA_HOME": "/xd", "XDG_STATE_HOME": "/xs", "XDG_RUNTIME_DIR": "/run/user/1000"},
			Paths{ConfigFile: "/xc/asura/config.toml", DB: "/xd/asura/asura.db", Socket: "/run/user/1000/asura/asura.sock"}, ""},
		{"home-fallback", map[string]string{"HOME": "/home/u"},
			Paths{ConfigFile: "/home/u/.config/asura/config.toml", DB: "/home/u/.local/share/asura/asura.db", Socket: "/home/u/.local/state/asura/asura.sock"}, ""},
		{"state-without-runtime", map[string]string{"HOME": "/home/u", "XDG_STATE_HOME": "/xs"},
			Paths{ConfigFile: "/home/u/.config/asura/config.toml", DB: "/home/u/.local/share/asura/asura.db", Socket: "/xs/asura/asura.sock"}, ""},
		{"relative-xdg-ignored", map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "rel/c", "XDG_DATA_HOME": "./d", "XDG_STATE_HOME": "s", "XDG_RUNTIME_DIR": "run"},
			Paths{ConfigFile: "/home/u/.config/asura/config.toml", DB: "/home/u/.local/share/asura/asura.db", Socket: "/home/u/.local/state/asura/asura.sock"}, ""},
		{"no-home", map[string]string{"XDG_CONFIG_HOME": "/xc"}, Paths{}, "HOME"},
		{"relative-home", map[string]string{"HOME": "home"}, Paths{}, "HOME"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DefaultPaths(env(c.env))
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v err=%v", got, err)
			}
		})
	}
}

func TestCheckSocketPath(t *testing.T) {
	ok := "/" + strings.Repeat("s", 106)
	if len(ok) != 107 || CheckSocketPath(ok) != nil {
		t.Fatalf("107-byte path rejected")
	}
	if err := CheckSocketPath(ok + "s"); err == nil || !strings.Contains(err.Error(), "108 bytes") {
		t.Fatalf("108-byte path: %v", err)
	}
	if CheckSocketPath("rel/asura.sock") == nil {
		t.Fatal("relative socket accepted")
	}
}

// writeConfig 在私有目錄（0700）以 0600 寫入設定檔。
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "asura")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if cfg != nil || err != nil {
		t.Fatalf("missing config: cfg=%v err=%v", cfg, err)
	}
}

func TestLoadValid(t *testing.T) {
	path := writeConfig(t, `schema_version = 1
db = "/data/asura.db"
socket = "/run/asura/asura.sock"
allowed_roots = ["/work/a", "/work/b"]
workers = 4
queue = 20
runtime_priority = ["codex", "opencode"]
memory_utility_policy = "apply"

[context]
limit = 120000
policy = "reject"
reserve_ratio = 0.25
`)
	cfg, err := Load(path)
	if err != nil || cfg == nil {
		t.Fatalf("cfg=%v err=%v", cfg, err)
	}
	if *cfg.DB != "/data/asura.db" || *cfg.Socket != "/run/asura/asura.sock" || strings.Join(cfg.AllowedRoots, ",") != "/work/a,/work/b" ||
		*cfg.Workers != 4 || *cfg.Queue != 20 || strings.Join(cfg.RuntimePriority, ",") != "codex,opencode" || *cfg.MemoryUtilityPolicy != "apply" ||
		*cfg.ContextLimit != 120000 || *cfg.ContextPolicy != "reject" || *cfg.ContextReserveRatio != 0.25 {
		t.Fatalf("cfg=%+v", cfg)
	}
	minimal, err := Load(writeConfig(t, "schema_version = 1\n"))
	if err != nil || minimal == nil {
		t.Fatalf("cfg=%v err=%v", minimal, err)
	}
	if minimal.DB != nil || minimal.Socket != nil || minimal.AllowedRoots != nil || minimal.Workers != nil || minimal.Queue != nil ||
		minimal.RuntimePriority != nil || minimal.MemoryUtilityPolicy != nil || minimal.ContextLimit != nil || minimal.ContextPolicy != nil || minimal.ContextReserveRatio != nil {
		t.Fatalf("unset fields populated: %+v", minimal)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	long := "/" + strings.Repeat("s", 120)
	cases := []struct{ name, body, want string }{
		{"syntax", "schema_version = 1\ndb = \"" + secret + "\n", "syntax error at line 2"},
		{"unknown-field", "schema_version = 1\ntoken = \"" + secret + "\"\n", "unknown field token"},
		{"unknown-nested-field", "schema_version = 1\n[context]\napi_key = \"" + secret + "\"\n", "unknown field context.api_key"},
		{"missing-schema", "db = \"/x.db\"\n", "schema_version"},
		{"unsupported-schema", "schema_version = 2\n", "unsupported schema_version"},
		{"type-mismatch", "schema_version = 1\nworkers = \"" + secret + "\"\n", "workers"},
		{"utility-enum", "schema_version = 1\nmemory_utility_policy = \"" + secret + "\"\n", "memory_utility_policy"},
		{"context-policy-enum", "schema_version = 1\n[context]\npolicy = \"" + secret + "\"\n", "context.policy"},
		{"context-limit", "schema_version = 1\n[context]\nlimit = -5\n", "context.limit"},
		{"context-ratio", "schema_version = 1\n[context]\nreserve_ratio = 1.5\n", "context.reserve_ratio"},
		{"runtime-priority", "schema_version = 1\nruntime_priority = [\"" + secret + "\"]\n", "runtime_priority"},
		{"runtime-priority-comma", "schema_version = 1\nruntime_priority = [\"codex,opencode\"]\n", "runtime_priority"},
		{"relative-db", "schema_version = 1\ndb = \"" + secret + "/asura.db\"\n", "db"},
		{"relative-socket", "schema_version = 1\nsocket = \"" + secret + ".sock\"\n", "socket"},
		{"long-socket", "schema_version = 1\nsocket = \"" + long + "\"\n", "socket"},
		{"relative-root", "schema_version = 1\nallowed_roots = [\"" + secret + "\"]\n", "allowed_roots"},
		{"empty-roots", "schema_version = 1\nallowed_roots = []\n", "allowed_roots"},
		{"zero-workers", "schema_version = 1\nworkers = 0\n", "workers"},
		{"negative-queue", "schema_version = 1\nqueue = -1\n", "queue"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, c.body))
			if err == nil || cfg != nil {
				t.Fatalf("accepted: %+v", cfg)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%q want %q", err, c.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error echoes config value: %q", err)
			}
		})
	}
}

func TestLoadRejectsUnsafeFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("ownership and permission checks require a non-root user")
	}
	body := "schema_version = 1\ndb = \"/" + secret + ".db\"\n"
	cases := []struct {
		name  string
		setup func(path string)
		want  string
	}{
		{"unreadable", func(p string) { os.Chmod(p, 0000) }, "cannot read"},
		{"group-writable-file", func(p string) { os.Chmod(p, 0620) }, "writable by group or others"},
		{"other-writable-file", func(p string) { os.Chmod(p, 0602) }, "writable by group or others"},
		{"other-writable-dir", func(p string) { os.Chmod(filepath.Dir(p), 0707) }, "writable by group or others"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeConfig(t, body)
			c.setup(path)
			t.Cleanup(func() { os.Chmod(filepath.Dir(path), 0700); os.Chmod(path, 0600) })
			cfg, err := Load(path)
			if err == nil || cfg != nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), secret) {
				t.Fatalf("cfg=%v err=%v", cfg, err)
			}
		})
	}
	// 非本人擁有：以 root 擁有的系統檔驗證，內容不是合法 config 也必須先因擁有者被拒。
	if _, err := Load("/etc/passwd"); err == nil || !strings.Contains(err.Error(), "not owned by the current user") {
		t.Fatalf("foreign owner: %v", err)
	}
}
