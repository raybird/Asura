package pi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"asura/internal/backend"
	"asura/internal/backend/clisandbox"
	"asura/internal/backend/verification"
)

type AdapterConfig struct {
	VerificationPath                  string
	OnProcessStarted, OnProcessExited func(context.Context, int)
	BeforeStart                       func(context.Context) (func(), error)
}

type Adapter struct {
	cfg             AdapterConfig
	cli, node, home string
}

func NewAdapter(cfg AdapterConfig) *Adapter {
	cli, _ := exec.LookPath("pi")
	cli, _ = filepath.EvalSymlinks(cli)
	node, _ := exec.LookPath("node")
	node, _ = filepath.EvalSymlinks(node)
	home, _ := os.UserHomeDir()
	return &Adapter{cfg: cfg, cli: cli, node: node, home: home}
}

func (a *Adapter) ID() string { return "pi" }

func (a *Adapter) Capabilities() []backend.Capability {
	return []backend.Capability{backend.CapWorkspaceWrite, backend.CapStructuredEvents, backend.CapProcessCancellation}
}

func (a *Adapter) HasCapability(cap backend.Capability) bool {
	return cap == backend.CapWorkspaceWrite || cap == backend.CapStructuredEvents || cap == backend.CapProcessCancellation
}

func (a *Adapter) ProbeExecutable() string {
	if filepath.IsAbs(a.cli) {
		return a.cli
	}
	return ""
}

func (a *Adapter) Probe(ctx context.Context) (string, error) {
	version, err := a.Version(ctx)
	if err != nil {
		return "", err
	}
	if err := clisandbox.ProbeSandboxDependency(ctx); err != nil {
		return "", err
	}
	return version, nil
}

func (a *Adapter) Version(ctx context.Context) (string, error) {
	record, _, err := a.admit(ctx)
	if err != nil {
		return "", err
	}
	return "pi/" + record.Version, nil
}

func (a *Adapter) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	cfg := clisandbox.Config{ID: a.ID()}
	if req == nil || req.Role != backend.RoleStriker {
		return clisandbox.Run(ctx, req, cfg)
	}
	record, nodeHash, err := a.admit(ctx)
	if err != nil {
		return &backend.ExecutionResult{BackendID: a.ID(), FailureClass: backend.FailureUnavailable}, backend.NewExecutionError(a.ID(), backend.FailureUnavailable, err, "")
	}
	cfg.Version = "pi/" + record.Version
	cfg.Home = a.home
	cfg.ProfileDir = filepath.Join(a.home, ".pi", "asura-agent")
	cfg.Program = []string{a.node, a.cli}
	cfg.ReadOnlyPaths = []string{a.node, a.packageDir()}
	cfg.PinnedFiles = []clisandbox.PinnedFile{{Path: a.node, SHA256: nodeHash}}
	cfg.PinnedTrees = []clisandbox.PinnedFile{{Path: a.packageDir(), SHA256: record.Tree}}
	cfg.Input = strings.NewReader(req.Prompt)
	cfg.Args = func(string) []string {
		args := []string{"--print", "--mode", "json", "--no-session", "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files"}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
		return args
	}
	cfg.Parse = func(stdout, stderr string) (*backend.ExecutionResult, error) {
		return clisandbox.ParseAgentEvents("Pi", stdout, stderr)
	}
	cfg.OnProcessStarted, cfg.OnProcessExited, cfg.BeforeStart = a.cfg.OnProcessStarted, a.cfg.OnProcessExited, a.cfg.BeforeStart
	return clisandbox.Run(ctx, req, cfg)
}

func (a *Adapter) packageDir() string {
	return filepath.Dir(filepath.Dir(a.cli))
}

func (a *Adapter) identity(ctx context.Context) (verification.Identity, string, error) {
	id := verification.Identity{Runtime: a.ID()}
	if !filepath.IsAbs(a.cli) {
		return id, "", &backend.ProbeError{Reason: backend.ExecutableNotFound}
	}
	root := a.packageDir()
	if a.cli != filepath.Join(root, "dist", "cli.js") || filepath.Base(root) != "pi-coding-agent" {
		return id, "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	var pkg struct{ Name, Version string }
	if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Name != "@earendil-works/pi-coding-agent" || pkg.Version == "" {
		return id, "", &backend.ProbeError{Reason: backend.VersionFailed}
	}
	cliHash, err := clisandbox.ProbeFileDigest(ctx, a.cli, backend.ExecutableNotFound)
	if err != nil {
		return id, "", err
	}
	nodeHash, err := clisandbox.ProbeFileDigest(ctx, a.node, backend.DependencyNotFound)
	if err != nil {
		return id, "", err
	}
	id.Version, id.Digest = pkg.Version, verification.CombineDigests(cliHash, nodeHash)
	return id, nodeHash, nil
}

func (a *Adapter) admit(ctx context.Context) (verification.Record, string, error) {
	id, nodeHash, err := a.identity(ctx)
	if err != nil {
		return verification.Record{}, "", err
	}
	record, err := verification.Admit(id.Runtime, id.Version, func() (string, error) { return id.Digest, nil }, nil, a.cfg.VerificationPath)
	if errors.Is(err, verification.ErrUnverified) || err == nil && record.Tree == "" {
		return record, "", &backend.ProbeError{Reason: backend.VersionUnverified}
	}
	return record, nodeHash, err
}

func (a *Adapter) VerificationSpec(ctx context.Context) (verification.Spec, error) {
	id, _, err := a.identity(ctx)
	if err != nil {
		return verification.Spec{}, err
	}
	tree, err := clisandbox.TreeHash(a.packageDir())
	agent := filepath.Join(a.home, ".pi", "agent")
	return verification.Spec{
		Record:    verification.Record{Identity: id, Tree: tree},
		Options:   verification.Options{CredentialDir: agent},
		Protected: []string{filepath.Join(agent, "auth.json")},
	}, err
}
