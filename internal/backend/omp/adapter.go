package omp

import (
	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const Version = "18.4.4"
const cliHash = "ca9b8832ea05299f02db83a6f479ab92e4646af7f0140b54f9c2592c07803441"
const bunHash = "a83d263767d839e4d2649ca8e35d07159c7afc99afdc96d731ced29e056dda0c"
const Model = "openai-codex/gpt-6-luna"
const settings = `{"retry":{"enabled":false,"modelFallback":false,"usageAwareFallback":false,"waitForUsageReset":false,"fallbackChains":{}},"memories":{"enabled":false},"compaction":{"enabled":false},"plan":{"defaultOnStartup":false}}`

type AdapterConfig struct {
	BinaryPath, BunPath, Home         string
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
		cfg.BinaryPath, _ = exec.LookPath("omp")
	}
	if cfg.BunPath == "" {
		cfg.BunPath, _ = exec.LookPath("bun")
	}
	if p, e := filepath.EvalSymlinks(cfg.BinaryPath); e == nil {
		cfg.BinaryPath = p
	}
	if p, e := filepath.EvalSymlinks(cfg.BunPath); e == nil {
		cfg.BunPath = p
	}
	return &Adapter{cfg}
}
func (a *Adapter) ID() string { return "omp" }
func (a *Adapter) Version(ctx context.Context) (string, error) {
	p, err := a.admit(ctx)
	if err != nil {
		return "", err
	}
	return "omp/" + p.version, nil
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
func (a *Adapter) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if req != nil && req.Role != backend.RoleStriker {
		return clisandbox.Run(ctx, req, clisandbox.Config{ID: a.ID()})
	}
	version, files, trees, err := a.pins(ctx)
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
	return clisandbox.Run(ctx, req, clisandbox.Config{
		ID: a.ID(), Version: version, Home: a.cfg.Home, AuthTarget: filepath.Join(a.cfg.Home, ".omp/agent/agent.db"),
		Auth: func(c context.Context) (clisandbox.Authentication, error) {
			return snapshot(c, filepath.Join(a.cfg.Home, ".omp/agent/agent.db"))
		},
		Program: []string{a.cfg.BunPath, a.cfg.BinaryPath}, PinnedFiles: files, PinnedTrees: trees, ReadOnlyPaths: []string{a.cfg.BunPath, a.modules()}, Settings: []byte(settings),
		Args: func(config string) []string {
			return []string{"--model", Model, "--thinking", "low", "--config", config, "--no-session", "--no-lsp", "--no-title", "--no-extensions", "--no-skills", "--no-rules", "--no-prewalk", "--approval-mode", "yolo", "--mode", "json", "--print", "--", req.Prompt}
		},
		Parse: ExtractResultAndEvents, OnProcessStarted: a.cfg.OnProcessStarted, OnProcessExited: a.cfg.OnProcessExited, BeforeStart: a.cfg.BeforeStart,
	})
}
