package pi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// 2026-11-19：用本機測試程式驗證 Pi 的版本放行、stdin 與 JSONL 契約。
func TestPiExecution(t *testing.T) {
	root, home, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	pkg := filepath.Join(root, "pi-coding-agent")
	cli := filepath.Join(pkg, "dist", "cli.js")
	node := filepath.Join(root, "node")
	profile := filepath.Join(home, ".pi", "asura-agent")
	for _, dir := range []string{filepath.Dir(cli), profile} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(pkg, "package.json"): `{"name":"@earendil-works/pi-coding-agent","version":"0.78.0"}`,
		cli:                                "fixture",
		node: `#!/bin/sh
set -eu
test "$(cat "$1")" = fixture
shift
test "$1 $2 $3" = "--print --mode json"
test "$PI_CODING_AGENT_DIR" = "$HOME/.pi/asura-agent"
test "$(cat)" = "@literal --prompt"
echo '{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"OK"}]}}'
`,
	} {
		if err := os.WriteFile(path, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	a := &Adapter{cli: cli, node: node, home: home}
	_, err := a.Version(ctx)
	var probe *backend.ProbeError
	if !errors.As(err, &probe) || probe.Reason != backend.VersionUnverified {
		t.Fatalf("unverified version accepted: %v", err)
	}
	spec, err := a.VerificationSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.VerificationPath, err = verification.TempRecords(root, spec.Record)
	if err != nil {
		t.Fatal(err)
	}
	req := &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: workspace, Prompt: "@literal --prompt"}
	res, err := a.Run(ctx, req)
	if res != nil && res.FailureClass == backend.FailureUnavailable &&
		(strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "bwrap namespaces unavailable")) {
		t.Skipf("bwrap unavailable: %v", err)
	}
	if err != nil || res.Output != "OK" || len(res.Events) != 1 {
		t.Fatalf("execution: %+v, %v", res, err)
	}
}
