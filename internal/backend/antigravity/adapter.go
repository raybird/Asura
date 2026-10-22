package antigravity

import (
	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// 2026-10-21：issue-73 Phase 1 的 live probe 需要以已安裝版本覆寫，暫改為 var。
const Version = "1.2.13"
const cliHash = "574b0234656c44564f3ff3b013225c86f14ae79b67d0f3b45f6aaf1b1625440e"

const Model = "gemini-3.8-flash-low"

type AdapterConfig struct {
	BinaryPath, Home                  string
	VerificationPath                  string
	OnProcessStarted, OnProcessExited func(context.Context, int)
	BeforeStart                       func(context.Context) (func(), error)
}
type Adapter struct{ cfg AdapterConfig }

var _ backend.Backend = (*Adapter)(nil)

func NewAdapter(cfg AdapterConfig) *Adapter {
	if cfg.Home == "" {
		cfg.Home, _ = os.UserHomeDir()
	}
	if cfg.BinaryPath == "" {
		cfg.BinaryPath, _ = exec.LookPath("agy")
	}
	if p, e := filepath.EvalSymlinks(cfg.BinaryPath); e == nil {
		cfg.BinaryPath = p
	}
	return &Adapter{cfg}
}
func (a *Adapter) ID() string { return "antigravity" }
func (a *Adapter) Version(ctx context.Context) (string, error) {
	version, _, err := a.admit(ctx)
	return version, err
}
func (a *Adapter) Capabilities() []backend.Capability {
	return []backend.Capability{backend.CapWorkspaceWrite, backend.CapStructuredEvents, backend.CapProcessCancellation}
}
func (a *Adapter) HasCapability(cap backend.Capability) bool {
	for _, c := range a.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}

func decodeSnapshot(data []byte) (clisandbox.Authentication, error) {
	var auth clisandbox.Authentication
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return auth, errors.New("invalid Antigravity credential")
	}
	var token map[string]json.RawMessage
	if json.Unmarshal(raw["token"], &token) != nil {
		return auth, errors.New("missing Antigravity access token")
	}
	var access, refresh, expiry, id string
	if json.Unmarshal(token["access_token"], &access) != nil || access == "" || json.Unmarshal(token["expiry"], &expiry) != nil {
		return auth, errors.New("invalid Antigravity access credential")
	}
	expires, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !expires.After(time.Now()) {
		return auth, errors.New("Antigravity login expired; update original CLI login")
	}
	_ = json.Unmarshal(token["refresh_token"], &refresh)
	_ = json.Unmarshal(raw["id_token"], &id)
	delete(token, "refresh_token")
	clean, err := json.Marshal(token)
	if err != nil {
		return auth, err
	}
	raw["token"] = clean
	auth.Payload, err = json.Marshal(raw)
	auth.Expiry = expires
	auth.Secrets = []string{access, refresh, id}
	return auth, err
}
func (a *Adapter) snapshot(ctx context.Context) (clisandbox.Authentication, error) {
	// 2026-09-30：與原 CLI 相同的 keyring item；不保存更新，也不查其他登入身份。
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(lookupCtx, "secret-tool", "lookup", "service", "gemini", "username", "antigravity").Output()
	if err == nil {
		return decodeSnapshot(data)
	}
	if lookupCtx.Err() != nil {
		return clisandbox.Authentication{}, errors.New("Antigravity keyring lookup unavailable")
	}
	data, err = os.ReadFile(filepath.Join(a.cfg.Home, ".gemini/antigravity-cli/antigravity-oauth-token"))
	if err != nil {
		return clisandbox.Authentication{}, errors.New("Antigravity login missing")
	}
	return decodeSnapshot(data)
}
func (a *Adapter) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if req != nil && req.Role != backend.RoleStriker {
		return clisandbox.Run(ctx, req, clisandbox.Config{ID: a.ID()})
	}
	version, digest, err := a.admit(ctx)
	if err != nil {
		r := &backend.ExecutionResult{BackendID: a.ID(), FailureClass: backend.FailureUnavailable, StartedAt: time.Now(), CompletedAt: time.Now()}
		return r, backend.NewExecutionError(a.ID(), r.FailureClass, err, "")
	}
	if req == nil {
		return nil, errors.New("execution request cannot be nil")
	}
	if req.Model != "" && req.Model != Model {
		return &backend.ExecutionResult{BackendID: a.ID(), FailureClass: backend.FailureUnsupported}, backend.NewExecutionError(a.ID(), backend.FailureUnsupported, errors.New("model differs from fixed contract"), "")
	}
	return clisandbox.Run(ctx, req, clisandbox.Config{ID: a.ID(), Version: version, Home: a.cfg.Home, AuthTarget: filepath.Join(a.cfg.Home, ".gemini/antigravity-cli/antigravity-oauth-token"), Auth: a.snapshot, AuthReadOnly: true,
		Program: []string{a.cfg.BinaryPath}, PinnedFiles: []clisandbox.PinnedFile{{Path: a.cfg.BinaryPath, SHA256: digest}}, ReadOnlyPaths: []string{a.cfg.BinaryPath},
		Args: func(string) []string {
			return []string{"--mode", "accept-edits", "--model", Model, "--disable-slash-commands", "--dangerously-skip-permissions", "--output-format", "stream-json", "--print-timeout", "0", "--print=" + req.Prompt}
		},
		Parse: ExtractResultAndEvents, OnProcessStarted: a.cfg.OnProcessStarted, OnProcessExited: a.cfg.OnProcessExited, BeforeStart: a.cfg.BeforeStart})
}
