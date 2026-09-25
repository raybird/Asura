package gitmem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapture_Complete(t *testing.T) {
	allowedRoot, repoDir := initTestRepoWithBranches(t)
	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	capturer := NewCapturer(exec)
	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if facts.Truncated {
		t.Errorf("expected complete capture, got truncated=true: %s", facts.TruncatedReason)
	}
	if facts.CommitCount != 1 {
		t.Errorf("expected 1 commit, got %d", facts.CommitCount)
	}
	if len(facts.ChangedFiles) == 0 {
		t.Errorf("expected changed files, got empty")
	}
	if !strings.Contains(facts.DiffStatSummary, "file3.txt") {
		t.Errorf("expected diff stat summary to mention file3.txt, got %s", facts.DiffStatSummary)
	}
}

func TestCapture_Partial_FileLimit(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "base.txt"), []byte("base"), 0644)
	cmd = execCommand(repoDir, "add", "base.txt")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "base commit")
	_ = cmd.Run()

	// 產生 60 個變更檔案
	for i := 1; i <= 60; i++ {
		_ = os.WriteFile(filepath.Join(repoDir, fmt.Sprintf("file_%02d.txt", i)), []byte("content"), 0644)
		cmd = execCommand(repoDir, "add", fmt.Sprintf("file_%02d.txt", i))
		_ = cmd.Run()
	}
	cmd = execCommand(repoDir, "commit", "-m", "add 60 files")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	capturer := NewCapturer(exec)
	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("expected success with partial capture, got error: %v", err)
	}

	if !facts.Truncated {
		t.Fatal("expected truncated=true when files > 50, got false")
	}
	if !strings.Contains(facts.TruncatedReason, "file limit exceeded") {
		t.Errorf("expected file limit exceeded in reason, got %s", facts.TruncatedReason)
	}
	if len(facts.ChangedFiles) != 50 {
		t.Errorf("expected 50 kept files, got %d", len(facts.ChangedFiles))
	}
	if len(facts.OmittedFiles) != 10 {
		t.Errorf("expected 10 omitted files, got %d", len(facts.OmittedFiles))
	}
}

func TestCapture_Partial_DiffSizeLimit(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "base.txt"), []byte("base"), 0644)
	cmd = execCommand(repoDir, "add", "base.txt")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "base commit")
	_ = cmd.Run()

	// 構造超過 256 KiB 的 diff (例如 300 KiB)
	bigContent := strings.Repeat("A", 300*1024)
	_ = os.WriteFile(filepath.Join(repoDir, "big.txt"), []byte(bigContent), 0644)
	cmd = execCommand(repoDir, "add", "big.txt")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "large diff commit")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	capturer := NewCapturer(exec)
	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("expected success with partial capture, got error: %v", err)
	}

	if !facts.Truncated {
		t.Fatal("expected truncated=true when diff > 256KiB, got false")
	}
	if !strings.Contains(facts.TruncatedReason, "diff size limit exceeded") {
		t.Errorf("expected diff size limit exceeded, got: %s", facts.TruncatedReason)
	}
	if len(facts.FilteredDiff) > MaxDiffBytes {
		t.Errorf("expected FilteredDiff <= %d bytes, got %d", MaxDiffBytes, len(facts.FilteredDiff))
	}
}

func TestCapture_SensitiveFileFiltering(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "init.txt"), []byte("init"), 0644)
	cmd = execCommand(repoDir, "add", "init.txt")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "init")
	_ = cmd.Run()

	// 敏感檔案與一般檔案
	_ = os.WriteFile(filepath.Join(repoDir, ".env"), []byte("SECRET_API_KEY=supersecret12345\n"), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, "id_rsa"), []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n"), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, "normal.go"), []byte("package main\n"), 0644)
	cmd = execCommand(repoDir, "add", ".env", "id_rsa", "normal.go")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "add secrets and normal code")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	capturer := NewCapturer(exec)
	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	// 驗證機密內容未出現在 FilteredDiff
	if strings.Contains(facts.FilteredDiff, "supersecret12345") {
		t.Fatal("SECURITY BUG: .env secret leaked in FilteredDiff")
	}
	if strings.Contains(facts.FilteredDiff, "BEGIN RSA PRIVATE KEY") {
		t.Fatal("SECURITY BUG: id_rsa private key leaked in FilteredDiff")
	}
	if !strings.Contains(facts.FilteredDiff, "package main") {
		t.Errorf("expected normal.go content to be in diff, got: %s", facts.FilteredDiff)
	}
}
