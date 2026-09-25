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

	// 產生 510 個變更檔案 (超過 500 上限)
	for i := 1; i <= 510; i++ {
		_ = os.WriteFile(filepath.Join(repoDir, fmt.Sprintf("file_%03d.txt", i)), []byte("content"), 0644)
	}
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "add 510 files")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	capturer := NewCapturer(exec)
	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("expected success with partial capture, got error: %v", err)
	}

	if !facts.Truncated {
		t.Fatal("expected truncated=true when files > 500, got false")
	}
	if !strings.Contains(facts.TruncatedReason, "file limit exceeded") {
		t.Errorf("expected file limit exceeded in reason, got %s", facts.TruncatedReason)
	}
	if len(facts.ChangedFiles) != 500 {
		t.Errorf("expected 500 kept files, got %d", len(facts.ChangedFiles))
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

func TestCapture_SingleFileOver512KiB_Truncated(t *testing.T) {
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

	// 單一檔案產生 600 KiB 變更（超過 GitExecutor 512 KiB 單次 stdout 緩衝區上限）
	largeContent := strings.Repeat("X\n", 300*1024)
	_ = os.WriteFile(filepath.Join(repoDir, "huge.txt"), []byte(largeContent), 0644)
	cmd = execCommand(repoDir, "add", "huge.txt")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "huge file diff")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()

	capturer := NewCapturer(exec)
	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("expected capture to succeed gracefully with partial truncation, got error: %v", err)
	}

	if !facts.Truncated {
		t.Fatal("SECURITY/ACCURACY FAIL: expected facts.Truncated=true for single file > 512KiB stdout buffer limit")
	}
	if !strings.Contains(facts.TruncatedReason, "stdout limit") && !strings.Contains(facts.TruncatedReason, "buffer limit") && !strings.Contains(facts.TruncatedReason, "diff failed") {
		t.Errorf("expected stdout limit or diff failed truncation reason, got: %s", facts.TruncatedReason)
	}
}

func TestCapture_SubdirectoryScopeIsolation(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	pkgADir := filepath.Join(repoDir, "pkg_a")
	pkgBDir := filepath.Join(repoDir, "pkg_b")
	_ = os.MkdirAll(pkgADir, 0755)
	_ = os.MkdirAll(pkgBDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "root.txt"), []byte("root"), 0644)
	_ = os.WriteFile(filepath.Join(pkgADir, "a.go"), []byte("package a\nfunc A() {}\n"), 0644)
	_ = os.WriteFile(filepath.Join(pkgBDir, "b.go"), []byte("package b\nfunc B() {}\n"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "initial commit with multi-packages")
	_ = cmd.Run()

	// 修改 pkg_a 與 pkg_b 中的檔案
	_ = os.WriteFile(filepath.Join(pkgADir, "a.go"), []byte("package a\nfunc A() { println(\"A modified\") }\n"), 0644)
	_ = os.WriteFile(filepath.Join(pkgBDir, "b.go"), []byte("package b\nfunc B() { println(\"B modified\") }\n"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "modify both packages")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()
	capturer := NewCapturer(exec)

	// 1. 在 pkgADir workspace 擷取：只能看到 pkg_a/a.go，絕不可洩漏 pkg_b/b.go
	factsA, err := capturer.CaptureRange(ctx, pkgADir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("capture pkgA failed: %v", err)
	}
	if len(factsA.ChangedFiles) != 1 || factsA.ChangedFiles[0] != "pkg_a/a.go" {
		t.Errorf("expected only pkg_a/a.go in pkgA scope, got: %v", factsA.ChangedFiles)
	}
	if strings.Contains(factsA.FilteredDiff, "B modified") {
		t.Fatal("ISOLATION VIOLATION: sibling pkg_b changes leaked into pkg_a diff!")
	}
	if !strings.Contains(factsA.FilteredDiff, "A modified") {
		t.Errorf("expected pkg_a changes in diff, got: %s", factsA.FilteredDiff)
	}

	// 2. 在 pkgBDir workspace 擷取：只能看到 pkg_b/b.go，絕不可洩漏 pkg_a/a.go
	factsB, err := capturer.CaptureRange(ctx, pkgBDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("capture pkgB failed: %v", err)
	}
	if len(factsB.ChangedFiles) != 1 || factsB.ChangedFiles[0] != "pkg_b/b.go" {
		t.Errorf("expected only pkg_b/b.go in pkgB scope, got: %v", factsB.ChangedFiles)
	}
	if strings.Contains(factsB.FilteredDiff, "A modified") {
		t.Fatal("ISOLATION VIOLATION: sibling pkg_a changes leaked into pkg_b diff!")
	}
	if !strings.Contains(factsB.FilteredDiff, "B modified") {
		t.Errorf("expected pkg_b changes in diff, got: %s", factsB.FilteredDiff)
	}
}

func TestCapture_SubdirectoryWithWildcardName_LiteralIsolation(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	// 建立帶有特殊萬用字元 * 的目錄 pkg*，以及兄弟目錄 pkg_a、pkg_b
	pkgStarDir := filepath.Join(repoDir, "pkg*")
	pkgADir := filepath.Join(repoDir, "pkg_a")
	pkgBDir := filepath.Join(repoDir, "pkg_b")
	_ = os.MkdirAll(pkgStarDir, 0755)
	_ = os.MkdirAll(pkgADir, 0755)
	_ = os.MkdirAll(pkgBDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(pkgStarDir, "star.txt"), []byte("initial star"), 0644)
	_ = os.WriteFile(filepath.Join(pkgADir, "a.txt"), []byte("initial a"), 0644)
	_ = os.WriteFile(filepath.Join(pkgBDir, "b.txt"), []byte("initial b"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "init packages")
	_ = cmd.Run()

	// 修改三個目錄下的檔案
	_ = os.WriteFile(filepath.Join(pkgStarDir, "star.txt"), []byte("modified star content"), 0644)
	_ = os.WriteFile(filepath.Join(pkgADir, "a.txt"), []byte("modified a content"), 0644)
	_ = os.WriteFile(filepath.Join(pkgBDir, "b.txt"), []byte("modified b content"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "modify all packages")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()
	capturer := NewCapturer(exec)

	// 在 pkg* 目錄執行 capture：必須使用字面路徑匹配，絕不能因 * 萬用字元而把 pkg_a 或 pkg_b 的變更抓進來！
	facts, err := capturer.CaptureRange(ctx, pkgStarDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("capture pkg* failed: %v", err)
	}

	if len(facts.ChangedFiles) != 1 || facts.ChangedFiles[0] != "pkg*/star.txt" {
		t.Fatalf("LITERAL ISOLATION FAIL: expected exactly [pkg*/star.txt], got: %v", facts.ChangedFiles)
	}
	if strings.Contains(facts.FilteredDiff, "modified a content") || strings.Contains(facts.FilteredDiff, "modified b content") {
		t.Fatalf("LITERAL ISOLATION FAIL: sibling directories pkg_a or pkg_b leaked into pkg* diff! Diff:\n%s", facts.FilteredDiff)
	}
	if !strings.Contains(facts.FilteredDiff, "modified star content") {
		t.Errorf("expected star.txt content in diff, got:\n%s", facts.FilteredDiff)
	}
}

func TestCapture_SpecialFilenamesWithNullDelimiter(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "base.txt"), []byte("base"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "base commit")
	_ = cmd.Run()

	// 建立包含 tab (\t)、空格與特殊引號的檔名
	// Git diff --name-only 預設會對含 \t 的檔名輸出 C-quoted "\"odd\\tfile.txt\""
	// 使用 -z 旗標則保證以 NUL 分隔且無 C-style quoting
	specialFile1 := "odd\tfile.txt"
	specialFile2 := "space file.txt"
	specialFile3 := "quote\"file.txt"

	_ = os.WriteFile(filepath.Join(repoDir, specialFile1), []byte("content of tab file"), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, specialFile2), []byte("content of space file"), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, specialFile3), []byte("content of quote file"), 0644)

	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "add special filenames")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	ctx := context.Background()
	capturer := NewCapturer(exec)

	facts, err := capturer.CaptureRange(ctx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("CaptureRange failed with special filenames: %v", err)
	}

	if facts.Truncated {
		t.Fatalf("expected non-truncated capture, got: %s", facts.TruncatedReason)
	}

	// 驗證 ChangedFiles 精確保留原始 filename bytes，而非 Git C-quoted 顯示名稱
	fileMap := make(map[string]bool)
	for _, f := range facts.ChangedFiles {
		fileMap[f] = true
	}

	if !fileMap[specialFile1] {
		t.Errorf("special file with tab %q not found in ChangedFiles: %v", specialFile1, facts.ChangedFiles)
	}
	if !fileMap[specialFile2] {
		t.Errorf("special file with space %q not found in ChangedFiles: %v", specialFile2, facts.ChangedFiles)
	}
	if !fileMap[specialFile3] {
		t.Errorf("special file with quote %q not found in ChangedFiles: %v", specialFile3, facts.ChangedFiles)
	}

	// 驗證 FilteredDiff 能透過字面 pathspec 順利抓到各檔案的內容，而非取回空 diff
	if !strings.Contains(facts.FilteredDiff, "content of tab file") {
		t.Errorf("diff missing content for tab file:\n%s", facts.FilteredDiff)
	}
	if !strings.Contains(facts.FilteredDiff, "content of space file") {
		t.Errorf("diff missing content for space file:\n%s", facts.FilteredDiff)
	}
	if !strings.Contains(facts.FilteredDiff, "content of quote file") {
		t.Errorf("diff missing content for quote file:\n%s", facts.FilteredDiff)
	}
}

func TestCapture_TotalTimeoutExceeded_TruncatedAndZeroCheckpointAdvance(t *testing.T) {
	dir := t.TempDir()
	allowedRoot := filepath.Join(dir, "allowed")
	repoDir := filepath.Join(allowedRoot, "project")
	_ = os.MkdirAll(repoDir, 0755)

	cmd := execCommand(repoDir, "init", "-b", "main")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("v1"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "commit 1")
	_ = cmd.Run()

	_ = os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("v2"), 0644)
	cmd = execCommand(repoDir, "add", "-A")
	_ = cmd.Run()
	cmd = execCommand(repoDir, "commit", "-m", "commit 2")
	_ = cmd.Run()

	exec := NewGitExecutor([]string{allowedRoot})
	capturer := NewCapturer(exec)

	// 傳入已經被取消/超時的 Context 模擬執行超時
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// 由於已經超時，CaptureRange 應捕捉此超時，終止 diff 並標記 Truncated
	facts, err := capturer.CaptureRange(canceledCtx, repoDir, "HEAD~1..HEAD")
	if err != nil {
		// 若在早期解析即被阻擋亦屬合法安全行為
		if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}

	// 若產出 facts，必須標記為 Truncated，絕不假裝完整
	if !facts.Truncated {
		t.Fatalf("expected facts.Truncated to be true on timeout, got false")
	}

	// 驗證 Checkpoint 評估：Truncated == true 時絕不前進 Checkpoint（進展為 0）
	service := NewCaptureService(exec, nil)
	cp, err := service.evaluateCheckpointAdvance(context.Background(), repoDir, facts, true)
	if err != nil {
		t.Fatalf("unexpected evaluate error: %v", err)
	}
	if cp != nil {
		t.Fatalf("SECURITY VIOLATION: Checkpoint advanced on truncated capture: %+v", cp)
	}
}


