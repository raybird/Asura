package clisandbox

import (
	"asura/internal/backend"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 2026-09-30：BADAPT-006／007，唯讀能力未證明時必須在程序啟動前拒絕。
func TestUnsupportedRoles(t *testing.T) {
	for _, role := range []backend.Role{backend.RoleOracle, backend.RoleCritic, "unknown"} {
		result, err := Run(context.Background(), &backend.ExecutionRequest{Role: role}, Config{})
		if err == nil || result.FailureClass != backend.FailureUnsupported {
			t.Fatalf("role %s: result=%+v error=%v", role, result, err)
		}
	}
}

// 2026-09-30：BADAPT-007，真實 bwrap 子程序與 deadline；不 mock namespace。
func TestProcessCancellation(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap unavailable")
	}
	home := t.TempDir()
	workspace := t.TempDir()
	ready := filepath.Join(workspace, "ready")
	marker := filepath.Join(workspace, "late")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := Config{ID: "probe", Home: home, AuthTarget: filepath.Join(home, "auth"), Program: []string{"/usr/bin/python3"},
		Auth: func(context.Context) (Authentication, error) {
			return Authentication{Payload: []byte("probe"), Expiry: time.Now().Add(time.Minute)}, nil
		},
		Args: func(string) []string {
			return []string{"-c", `import subprocess,time;from pathlib import Path;subprocess.Popen(['/usr/bin/python3','-c',"import os,time;from pathlib import Path;os.setsid();time.sleep(2);Path('late').write_text('LATE')"]);Path('ready').write_text('READY');time.sleep(30)`}
		},
		Parse: func(string, string) (*backend.ExecutionResult, error) { return &backend.ExecutionResult{}, nil },
	}
	done := make(chan *backend.ExecutionResult, 1)
	go func() {
		r, _ := Run(ctx, &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: workspace}, cfg)
		done <- r
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(ready); e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, e := os.Stat(ready); e != nil {
		cancel()
		r := <-done
		if r.FailureClass == backend.FailureUnavailable {
			t.Skip("namespace unavailable")
		}
		t.Fatalf("helper never ready: %+v", r)
	}
	cancel()
	select {
	case r := <-done:
		if r.FailureClass != backend.FailureCancelled {
			t.Fatalf("class %s", r.FailureClass)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	time.Sleep(2500 * time.Millisecond)
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("escaped child produced delayed side effect")
	}
}

// 2026-09-30：BADAPT-007／009，過期時不啟動模型，原始 access 字串不進結果。
func TestAuthenticationBoundary(t *testing.T) {
	if _, e := exec.LookPath("bwrap"); e != nil {
		t.Skip("bwrap unavailable")
	}
	home, workspace := t.TempDir(), t.TempDir()
	starts := 0
	cfg := Config{ID: "probe", Home: home, AuthTarget: filepath.Join(home, "auth"), Program: []string{"/usr/bin/python3"},
		Args: func(string) []string { return []string{"-c", `print('PROBE_SECRET_SHOULD_NOT_PERSIST')`} },
		Auth: func(context.Context) (Authentication, error) {
			return Authentication{Payload: []byte("probe"), Expiry: time.Now().Add(-time.Second)}, nil
		},
		OnProcessStarted: func(context.Context, int) { starts++ },
		Parse: func(out, err string) (*backend.ExecutionResult, error) {
			return &backend.ExecutionResult{Output: out, PartialOutput: out}, nil
		},
	}
	req := &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: workspace}
	r, e := Run(context.Background(), req, cfg)
	if r.FailureClass == backend.FailureUnavailable {
		t.Skip("namespace unavailable")
	}
	if e == nil || r.FailureClass != backend.FailureAuth || starts != 0 {
		t.Fatalf("expired result %+v starts %d err %v", r, starts, e)
	}
	cfg.Auth = func(context.Context) (Authentication, error) {
		return Authentication{Payload: []byte("probe"), Expiry: time.Now().Add(time.Minute), Secrets: []string{"PROBE_SECRET_SHOULD_NOT_PERSIST"}}, nil
	}
	r, e = Run(context.Background(), req, cfg)
	if e != nil || starts != 1 || r.Output != "<redacted>\n" {
		t.Fatalf("redaction result %+v starts %d err %v", r, starts, e)
	}
}

// 2026-09-30：BADAPT-002／004，來源在驗證後更新，執行仍必須使用已驗證版本。
func TestRuntimePinnedAcrossSourceUpdate(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	script := filepath.Join(t.TempDir(), "runtime")
	original := []byte("#!/bin/sh\nprintf 'VERIFIED'\n")
	os.WriteFile(script, original, 0700)
	hash := fmt.Sprintf("%x", sha256.Sum256(original))
	cfg := Config{ID: "probe", Home: home, AuthTarget: filepath.Join(home, "auth"), Program: []string{script}, ReadOnlyPaths: []string{script}, PinnedFiles: []PinnedFile{{Path: script, SHA256: hash}},
		Auth: func(context.Context) (Authentication, error) {
			return Authentication{Payload: []byte("probe"), Expiry: time.Now().Add(time.Minute)}, nil
		}, Args: func(string) []string { return nil },
		BeforeStart: func(context.Context) (func(), error) {
			return nil, os.WriteFile(script, []byte("#!/bin/sh\nprintf 'UPDATED'\n"), 0700)
		},
		Parse: func(out, err string) (*backend.ExecutionResult, error) {
			return &backend.ExecutionResult{Output: out}, nil
		},
	}
	r, e := Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: workspace}, cfg)
	if r.FailureClass == backend.FailureUnavailable {
		t.Skip("namespace unavailable")
	}
	if e != nil || r.Output != "VERIFIED" {
		t.Fatalf("result %+v error %v", r, e)
	}
}
