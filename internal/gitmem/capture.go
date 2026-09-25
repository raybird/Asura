package gitmem

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// MaxAllowedFiles 每次擷取允許的最大變更檔案數上限 (H-TBD-3 / GITMEM-004)
	MaxAllowedFiles = 50
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

// CaptureRange 執行有界擷取，套用 50 files / 256 KiB 限制與敏感資料過濾
func (c *Capturer) CaptureRange(ctx context.Context, repoDir string, rangeExpr string) (*CaptureFacts, error) {
	// 1. 儲存庫與邊界校驗
	repoInfo, err := c.exec.ValidateAndResolveRepo(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to validate repo: %w", err)
	}

	// 2. 解析 Range 與祖先血統檢查
	resolved, err := c.exec.ResolveRange(ctx, repoDir, rangeExpr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve range: %w", err)
	}

	// 3. 取得變更檔案清單
	namesOut, err := c.exec.Exec(ctx, repoDir, "diff", "--name-only", fmt.Sprintf("%s..%s", resolved.BaseSHA, resolved.HeadSHA))
	if err != nil {
		return nil, fmt.Errorf("failed to list changed files: %w", err)
	}

	rawNames := strings.Split(strings.TrimSpace(string(namesOut)), "\n")
	allFiles := make([]string, 0, len(rawNames))
	for _, n := range rawNames {
		n = strings.TrimSpace(n)
		if n != "" {
			allFiles = append(allFiles, n)
		}
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
	statOut, err := c.exec.Exec(ctx, repoDir, "diff", "--stat", fmt.Sprintf("%s..%s", resolved.BaseSHA, resolved.HeadSHA))
	var diffStat string
	if err == nil {
		diffStat = strings.TrimSpace(string(statOut))
	}

	// 5. 擷取並過濾 Diff 內容
	var diffBuilder strings.Builder
	for _, file := range keptFiles {
		if isSensitiveFile(file) {
			diffBuilder.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n[REDACTED SENSITIVE FILE]\n\n", file, file))
			continue
		}

		fileDiffOut, err := c.exec.Exec(ctx, repoDir, "diff", fmt.Sprintf("%s..%s", resolved.BaseSHA, resolved.HeadSHA), "--", file)
		if err != nil {
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
