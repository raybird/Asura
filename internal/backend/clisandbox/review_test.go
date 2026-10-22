package clisandbox

import (
	"asura/internal/backend"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 2026-09-30：獨立審查發現的工作區覆蓋與 I/O 分類反例。
func TestReviewProtectedWorkspaceAndStartFailure(t *testing.T) {
	home, runtime := t.TempDir(), t.TempDir()
	workspace := filepath.Join(runtime, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ID: "probe", Home: home, AuthTarget: filepath.Join(home, "auth"), Program: []string{"/usr/bin/true"}, ReadOnlyPaths: []string{runtime}, Auth: func(context.Context) (Authentication, error) {
		return Authentication{Payload: []byte("probe"), Expiry: time.Now().Add(time.Minute)}, nil
	}, Args: func(string) []string { return nil }, Parse: func(string, string) (*backend.ExecutionResult, error) { return &backend.ExecutionResult{}, nil }}
	r, e := Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: workspace}, cfg)
	if e == nil || r.FailureClass != backend.FailureUnsupported {
		t.Fatalf("runtime overlap result %+v err %v", r, e)
	}
	cfg.ReadOnlyPaths = nil
	cfg.BeforeStart = func(context.Context) (func(), error) { return nil, errors.New("store unavailable") }
	r, e = Run(context.Background(), &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: t.TempDir()}, cfg)
	if e == nil || r.FailureClass != backend.FailureExecution {
		t.Fatalf("start failure result %+v err %v", r, e)
	}
}

// 2026-09-30：副本摘要捕捉依賴更新並拒絕跨樹連結。
func TestDependencySnapshot(t *testing.T) {
	source := t.TempDir()
	file := filepath.Join(source, "dep.js")
	if err := os.WriteFile(file, []byte("VERIFIED"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "snapshot")
	first, e := SnapshotTree(source, target)
	if e != nil {
		t.Fatal(e)
	}
	if err := os.WriteFile(file, []byte("UPDATED"), 0600); err != nil {
		t.Fatal(err)
	}
	second, e := SnapshotTree(source, filepath.Join(t.TempDir(), "snapshot"))
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(target, "dep.js"))
	if e != nil || string(data) != "VERIFIED" || first == second {
		t.Fatal("dependency snapshot did not preserve fixed bytes")
	}
	if err := os.Symlink("../outside", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, e := SnapshotTree(source, filepath.Join(t.TempDir(), "snapshot")); e == nil {
		t.Fatal("escaping symlink accepted")
	}
}

// 2026-10-21：issue-73，驗證紀錄的相依樹雜湊必須與執行時副本的摘要相同，否則已驗證的版本每次執行都會被拒絕。
func TestTreeHashMatchesSnapshot(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "pkg", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "pkg", "index.js"), []byte("module"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "pkg", "bin", "cli"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../pkg/bin/cli", filepath.Join(source, "pkg", "link")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotTree(source, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := TreeHash(source)
	if err != nil || hash != snapshot {
		t.Fatalf("TreeHash = %q, %v; snapshot = %q", hash, err, snapshot)
	}
	if err := os.Symlink("../../outside", filepath.Join(source, "pkg", "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeHash(source); err == nil {
		t.Fatal("escaping symlink accepted")
	}
}
