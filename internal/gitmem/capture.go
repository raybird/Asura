package gitmem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxAllowedFiles 每次擷取允許的最大變更檔案數上限 (H-TBD-3 / GITMEM-004)
	MaxAllowedFiles = 500
	// MaxDiffBytes 每次擷取允許的最大 diff 位元組上限 256 KiB (H-TBD-3 / GITMEM-004)
	MaxDiffBytes = 256 * 1024
)

// CaptureFacts 代表由 Git 變更中擷取出來的結構化客觀事實
type CaptureFacts struct {
	Workspace       string       `json:"workspace"`
	RepoID          string       `json:"repo_id"`
	RefName         string       `json:"ref_name"`
	BaseSHA         string       `json:"base_sha"`
	HeadSHA         string       `json:"head_sha"`
	CommitCount     int          `json:"commit_count"`
	Commits         []CommitInfo `json:"commits"`
	ChangedFiles    []string     `json:"changed_files"`
	TotalFilesCount int          `json:"total_files_count"`
	Truncated       bool         `json:"truncated"`
	TruncatedReason string       `json:"truncated_reason,omitempty"`
	OmittedFiles    []string     `json:"omitted_files,omitempty"`
	DiffStatSummary string       `json:"diff_stat_summary"`
	FilteredDiff    string       `json:"filtered_diff"`
}

// Capturer 提供有界 Git 變更擷取與三態截斷分流能力
type Capturer struct {
	exec *GitExecutor
}

// NewCapturer 建立新的擷取器實例
func NewCapturer(exec *GitExecutor) *Capturer {
	return &Capturer{exec: exec}
}

// isSensitiveFile 啟發式過濾常見機密檔案，避免私鑰或密鑰外洩
func isSensitiveFile(filename string) bool {
	base := strings.ToLower(filepath.Base(filename))
	ext := strings.ToLower(filepath.Ext(filename))

	// 敏感副檔名
	switch ext {
	case ".pem", ".key", ".p12", ".pfx", ".pkcs12", ".kdbx":
		return true
	}

	// 敏感檔案名稱與前綴
	if strings.HasPrefix(base, ".env") || base == "id_rsa" || base == "id_dsa" ||
		base == "id_ecdsa" || base == "id_ed25519" || base == ".netrc" || base == ".npmrc" {
		return true
	}

	if base == "credentials.json" || base == "service-account.json" || base == "secret.yaml" {
		return true
	}

	return false
}

// ToLiteralPathspec 將路徑轉換為 Git literal pathspec（:(literal)<cleanPath>）
// 確保所有特殊萬用字元（*、?、[等）均被視為字面文字，並規範為正斜線路徑分隔
func ToLiteralPathspec(p string) string {
	clean := filepath.ToSlash(filepath.Clean(p))
	clean = strings.TrimPrefix(clean, "./")
	return ":(literal)" + clean
}

// CaptureRange 執行有界擷取，套用 500 files / 256 KiB 限制、子目錄範圍隔離與敏感資料過濾
func (c *Capturer) CaptureRange(ctx context.Context, repoDir string, rangeExpr string) (*CaptureFacts, error) {
	// 整次 Capture 建立硬性總期限（GITMEM-004），上限不得超過 10s
	totalTimeout := 10 * time.Second
	if c.exec != nil && c.exec.timeout > 0 && c.exec.timeout < totalTimeout {
		totalTimeout = c.exec.timeout
	}
	captureCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	// 1. 儲存庫與邊界校驗
	repoInfo, err := c.exec.ValidateAndResolveRepo(captureCtx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to validate repo: %w", err)
	}

	// 計算 Workspace 相對於 RepoRoot 的子目錄相對路徑
	relPath, err := filepath.Rel(repoInfo.RepoRoot, repoInfo.Workspace)
	var pathspecs []string
	if err == nil && relPath != "." && relPath != "" {
		pathspecs = append(pathspecs, ToLiteralPathspec(relPath))
	}

	// 2. 解析 Range 與祖先血統檢查 (若為子目錄傳入 pathspec)
	resolved, err := c.exec.ResolveRange(captureCtx, repoInfo.RepoRoot, rangeExpr, pathspecs...)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve range: %w", err)
	}

	// 3. 取得變更檔案清單 (若為子目錄僅取子目錄範圍)
	// 使用 -z 旗標輸出 NUL 分隔字串，徹底防止 Git 對特殊檔名（換行、tab、引號等）做 C-style quoting，確保得到原始路徑 bytes
	namesArgs := []string{"diff", "--name-only", "-z", fmt.Sprintf("%s..%s", resolved.BaseSHA, resolved.HeadSHA)}
	if len(pathspecs) > 0 {
		namesArgs = append(namesArgs, "--")
		namesArgs = append(namesArgs, pathspecs...)
	}
	namesOut, err := c.exec.Exec(captureCtx, repoInfo.RepoRoot, namesArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to list changed files: %w", err)
	}

	rawEntries := bytes.Split(namesOut, []byte{0})
	allFiles := make([]string, 0, len(rawEntries))
	for _, entry := range rawEntries {
		if len(entry) == 0 {
			continue
		}
		allFiles = append(allFiles, string(entry))
	}

	var truncated bool
	var reasons []string
	var keptFiles []string
	var omittedFiles []string

	totalFilesCount := len(allFiles)
	if totalFilesCount > MaxAllowedFiles {
		truncated = true
		reasons = append(reasons, fmt.Sprintf("file limit exceeded (%d > %d)", totalFilesCount, MaxAllowedFiles))
		keptFiles = allFiles[:MaxAllowedFiles]
		omittedFiles = allFiles[MaxAllowedFiles:]
	} else {
		keptFiles = allFiles
	}

	// 4. 取得 Diff Stat Summary
	statArgs := []string{"diff", "--stat", fmt.Sprintf("%s..%s", resolved.BaseSHA, resolved.HeadSHA)}
	if len(pathspecs) > 0 {
		statArgs = append(statArgs, "--")
		statArgs = append(statArgs, pathspecs...)
	}
	statOut, err := c.exec.Exec(captureCtx, repoInfo.RepoRoot, statArgs...)
	var diffStat string
	if err == nil {
		diffStat = strings.TrimSpace(string(statOut))
	}

	// 5. 擷取並過濾 Diff 內容
	var diffBuilder strings.Builder
	for _, file := range keptFiles {
		// 檢查整次 capture 的 deadline 是否已經用盡或被取消
		if captureCtx.Err() != nil {
			truncated = true
			reasons = append(reasons, fmt.Sprintf("capture total timeout exceeded: %v", captureCtx.Err()))
			break
		}

		if isSensitiveFile(file) {
			diffBuilder.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n[REDACTED SENSITIVE FILE]\n\n", file, file))
			continue
		}

		fileDiffOut, err := c.exec.Exec(captureCtx, repoInfo.RepoRoot, "diff", fmt.Sprintf("%s..%s", resolved.BaseSHA, resolved.HeadSHA), "--", ToLiteralPathspec(file))
		if err != nil {
			truncated = true
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(captureCtx.Err(), context.DeadlineExceeded) {
				reasons = append(reasons, "capture deadline exceeded")
				break
			} else if errors.Is(err, ErrStdoutTruncated) {
				reasons = append(reasons, fmt.Sprintf("file %s diff exceeded stdout limit (512 KiB)", file))
			} else {
				reasons = append(reasons, fmt.Sprintf("file %s diff failed: %v", file, err))
			}
			continue
		}

		diffBuilder.Write(fileDiffOut)
		diffBuilder.WriteString("\n")

		if diffBuilder.Len() > MaxDiffBytes {
			truncated = true
			reasons = append(reasons, fmt.Sprintf("diff size limit exceeded (%d > %d bytes)", diffBuilder.Len(), MaxDiffBytes))
			break
		}
	}

	finalDiff := diffBuilder.String()
	if len(finalDiff) > MaxDiffBytes {
		finalDiff = finalDiff[:MaxDiffBytes]
	}

	return &CaptureFacts{
		Workspace:       repoInfo.Workspace,
		RepoID:          repoInfo.RepoID,
		RefName:         repoInfo.CurrentRef,
		BaseSHA:         resolved.BaseSHA,
		HeadSHA:         resolved.HeadSHA,
		CommitCount:     len(resolved.Commits),
		Commits:         resolved.Commits,
		ChangedFiles:    keptFiles,
		TotalFilesCount: totalFilesCount,
		Truncated:       truncated,
		TruncatedReason: strings.Join(reasons, "; "),
		OmittedFiles:    omittedFiles,
		DiffStatSummary: diffStat,
		FilteredDiff:    finalDiff,
	}, nil
}
