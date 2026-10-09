// Package config 載入 operator config（config.toml）並解析 XDG 預設路徑。
// config 只提供 operator defaults，不改變 runtime selection 或 Phase 4 執行權威。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"asura/internal/backend"
	"asura/internal/contextwin"

	"github.com/BurntSushi/toml"
)

// SchemaVersion 是目前唯一支援的 config.toml schema。
const SchemaVersion = 1

var decodeErrorPattern = regexp.MustCompile(`^toml: line (\d+) \(last key "([^"]+)"\)`)

// MaxSocketPathBytes 為 Linux sun_path（108 bytes，含結尾 NUL）可容納的路徑長度。
const MaxSocketPathBytes = 107

// Paths 是沒有 config 覆寫時的 XDG 預設路徑。
type Paths struct{ ConfigFile, DB, Socket string }

// Config 的 nil 欄位代表 config 未設定，由呼叫端依 precedence 取下一層來源。
type Config struct {
	DB, Socket          *string
	AllowedRoots        []string
	Workers, Queue      *int
	RuntimePriority     []string
	MemoryUtilityPolicy *string
	ContextLimit        *int64
	ContextPolicy       *string
	ContextReserveRatio *float64
}

type file struct {
	SchemaVersion       *int     `toml:"schema_version"`
	DB                  *string  `toml:"db"`
	Socket              *string  `toml:"socket"`
	AllowedRoots        []string `toml:"allowed_roots"`
	Workers             *int     `toml:"workers"`
	Queue               *int     `toml:"queue"`
	RuntimePriority     []string `toml:"runtime_priority"`
	MemoryUtilityPolicy *string  `toml:"memory_utility_policy"`
	Context             struct {
		Limit        *int64   `toml:"limit"`
		Policy       *string  `toml:"policy"`
		ReserveRatio *float64 `toml:"reserve_ratio"`
	} `toml:"context"`
}

// DefaultPaths 依 XDG Base Directory 解析路徑；相對路徑的 XDG 變數依規範視為未設定。
func DefaultPaths(getenv func(string) string) (Paths, error) {
	home := getenv("HOME")
	if !filepath.IsAbs(home) {
		return Paths{}, errors.New("HOME must be an absolute path to resolve XDG directories")
	}
	dir := func(name, fallback string) string {
		if v := getenv(name); filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, fallback)
	}
	socketDir := dir("XDG_STATE_HOME", ".local/state")
	if v := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(v) {
		socketDir = v
	}
	return Paths{
		ConfigFile: filepath.Join(dir("XDG_CONFIG_HOME", ".config"), "asura", "config.toml"),
		DB:         filepath.Join(dir("XDG_DATA_HOME", ".local/share"), "asura", "asura.db"),
		Socket:     filepath.Join(socketDir, "asura", "asura.sock"),
	}, nil
}

// CheckSocketPath 確認 socket 路徑為絕對路徑且不超過 sun_path 上限。
func CheckSocketPath(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("socket path must be absolute")
	}
	if len(path) > MaxSocketPathBytes {
		return fmt.Errorf("socket path is %d bytes, exceeding the %d-byte Unix socket limit; set a shorter socket path", len(path), MaxSocketPathBytes)
	}
	return nil
}

// Load 讀取並嚴格驗證 config；檔案不存在時回傳 (nil, nil)。錯誤訊息不回顯設定值。
func Load(path string) (*Config, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	fail := func(format string, args ...any) (*Config, error) {
		return nil, fmt.Errorf("config %s: "+format, append([]any{path}, args...)...)
	}
	if err != nil {
		return fail("cannot read file")
	}
	if err := checkOwnerAndMode(path, info); err != nil {
		return fail("%v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail("cannot read file")
	}
	var f file
	md, err := toml.Decode(string(raw), &f)
	if err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return fail("syntax error at line %d", perr.Position.Line)
		}
		// 型別不符等解碼錯誤只取行號與鍵名，不轉述 parser 原文。
		if m := decodeErrorPattern.FindStringSubmatch(err.Error()); m != nil {
			return fail("invalid value type for %s at line %s", m[2], m[1])
		}
		return fail("invalid file")
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return fail("unknown field %s", undecoded[0])
	}
	if f.SchemaVersion == nil {
		return fail("schema_version is required")
	}
	if *f.SchemaVersion != SchemaVersion {
		return fail("unsupported schema_version (supported: %d)", SchemaVersion)
	}
	if f.DB != nil && !filepath.IsAbs(*f.DB) {
		return fail("db must be an absolute path")
	}
	if f.Socket != nil {
		if err := CheckSocketPath(*f.Socket); err != nil {
			return fail("socket: %v", err)
		}
	}
	if f.AllowedRoots != nil {
		if len(f.AllowedRoots) == 0 {
			return fail("allowed_roots must list at least one directory")
		}
		for _, root := range f.AllowedRoots {
			if !filepath.IsAbs(root) {
				return fail("allowed_roots entries must be absolute paths")
			}
		}
	}
	if f.Workers != nil && *f.Workers < 1 {
		return fail("workers must be at least 1")
	}
	if f.Queue != nil && *f.Queue < 1 {
		return fail("queue must be at least 1")
	}
	if f.RuntimePriority != nil {
		ids, err := backend.ParseRuntimePriority(strings.Join(f.RuntimePriority, ","))
		if err != nil || len(ids) != len(f.RuntimePriority) {
			return fail("runtime_priority must list distinct runtime IDs (opencode, codex, omp, antigravity)")
		}
	}
	if p := f.MemoryUtilityPolicy; p != nil && *p != "observe" && *p != "apply" {
		return fail("memory_utility_policy must be observe or apply")
	}
	// context 沿用 contextwin 的驗證規則；只回報欄位名，不轉述含值的原始錯誤。
	contextFields := []struct {
		name, env, value string
		set              bool
	}{
		{"context.limit", contextwin.EnvContextLimit, formatInt(f.Context.Limit), f.Context.Limit != nil},
		{"context.policy", contextwin.EnvContextPolicy, deref(f.Context.Policy), f.Context.Policy != nil},
		{"context.reserve_ratio", contextwin.EnvContextReserveRatio, formatFloat(f.Context.ReserveRatio), f.Context.ReserveRatio != nil},
	}
	for _, field := range contextFields {
		if !field.set {
			continue
		}
		lookup := func(key string) (string, bool) { return field.value, key == field.env }
		if _, err := contextwin.LoadConfigWithLookup(lookup); err != nil || strings.TrimSpace(field.value) == "" {
			return fail("%s is invalid", field.name)
		}
	}
	return &Config{
		DB: f.DB, Socket: f.Socket, AllowedRoots: f.AllowedRoots, Workers: f.Workers, Queue: f.Queue,
		RuntimePriority: f.RuntimePriority, MemoryUtilityPolicy: f.MemoryUtilityPolicy,
		ContextLimit: f.Context.Limit, ContextPolicy: f.Context.Policy, ContextReserveRatio: f.Context.ReserveRatio,
	}, nil
}

// checkOwnerAndMode 拒絕非本人擁有，或檔案／所在目錄可被 group／others 寫入的設定檔。
func checkOwnerAndMode(path string, info fs.FileInfo) error {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return errors.New("file is not owned by the current user")
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return errors.New("cannot read config directory")
	}
	if info.Mode().Perm()&0o022 != 0 || dir.Mode().Perm()&0o022 != 0 {
		return errors.New("file or its directory is writable by group or others; run chmod 600 on the file and chmod 700 on its directory")
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatInt(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func formatFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}
