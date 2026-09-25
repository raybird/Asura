package gitmem

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	// MaxAllowedCommits 每次擷取允許的最大 commit 數上限 (H-TBD-3 / GITMEM-004)
	MaxAllowedCommits = 100
)

// CommitInfo 儲存單一 Commit 的核心中繼資料
type CommitInfo struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

// ResolvedRange 代表經過血統與存在性驗證後的確切區間
type ResolvedRange struct {
	BaseSHA string
	HeadSHA string
	Commits []CommitInfo
}

// ResolveRange 解析 Git range 表達式（例如 "HEAD~2..HEAD" 或單一 commit "HEAD"），並嚴格執行祖先血統檢查
func (g *GitExecutor) ResolveRange(ctx context.Context, repoDir string, rangeExpr string) (*ResolvedRange, error) {
	rangeExpr = strings.TrimSpace(rangeExpr)
	if rangeExpr == "" {
		return nil, fmt.Errorf("range expression cannot be empty")
	}

	var baseRef, headRef string
	if strings.Contains(rangeExpr, "..") {
		parts := strings.Split(rangeExpr, "..")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid range syntax: %s", rangeExpr)
		}
		baseRef = strings.TrimSpace(parts[0])
		headRef = strings.TrimSpace(parts[1])
		if baseRef == "" {
			return nil, fmt.Errorf("base revision cannot be empty in range: %s", rangeExpr)
		}
		if headRef == "" {
			headRef = "HEAD"
		}
	} else {
		// 單一 commit：比對其前一個 commit
		headRef = rangeExpr
		parentOut, err := g.Exec(ctx, repoDir, "rev-parse", "--verify", headRef+"^")
		if err != nil {
			// 若無 parent (root commit)
			return nil, fmt.Errorf("cannot resolve parent for single root commit: %s", headRef)
		}
		baseRef = strings.TrimSpace(string(parentOut))
	}

	// 1. 驗證並解析 base SHA
	baseOut, err := g.Exec(ctx, repoDir, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("invalid revision for base: %s (%w)", baseRef, err)
	}
	baseSHA := strings.TrimSpace(string(baseOut))

	// 2. 驗證並解析 head SHA
	headOut, err := g.Exec(ctx, repoDir, "rev-parse", "--verify", headRef+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("invalid revision for head: %s (%w)", headRef, err)
	}
	headSHA := strings.TrimSpace(string(headOut))

	// 3. 空區間防護
	if baseSHA == headSHA {
		return nil, fmt.Errorf("empty range: base and head are identical (%s)", baseSHA)
	}

	// 4. 祖先血統檢查 (Ancestry Check)
	// git merge-base --is-ancestor <base> <head>
	_, err = g.Exec(ctx, repoDir, "merge-base", "--is-ancestor", baseSHA, headSHA)
	if err != nil {
		return nil, fmt.Errorf("base commit %s is not an ancestor of head commit %s (unrelated history or diverged branch)", baseSHA, headSHA)
	}

	// 5. 檢查 commit 數量上限
	countOut, err := g.Exec(ctx, repoDir, "rev-list", "--count", fmt.Sprintf("%s..%s", baseSHA, headSHA))
	if err != nil {
		return nil, fmt.Errorf("failed to count commits: %w", err)
	}
	countStr := strings.TrimSpace(string(countOut))
	commitCount, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, fmt.Errorf("invalid commit count output '%s': %w", countStr, err)
	}

	if commitCount == 0 {
		return nil, fmt.Errorf("empty range: 0 commits found between %s and %s", baseSHA, headSHA)
	}
	if commitCount > MaxAllowedCommits {
		return nil, fmt.Errorf("commit count %d exceeds maximum limit %d (rejected capture)", commitCount, MaxAllowedCommits)
	}

	// 6. 讀取區間內的 commit 清單 (由舊到新排序)
	logFormat := "%H%x1f%s%x1f%an%x1f%aI"
	logOut, err := g.Exec(ctx, repoDir, "log", "--reverse", fmt.Sprintf("--format=%s", logFormat), fmt.Sprintf("%s..%s", baseSHA, headSHA))
	if err != nil {
		return nil, fmt.Errorf("failed to list commits: %w", err)
	}

	commits := make([]CommitInfo, 0, commitCount)
	lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x1f")
		if len(fields) >= 4 {
			commits = append(commits, CommitInfo{
				SHA:     fields[0],
				Subject: fields[1],
				Author:  fields[2],
				Date:    fields[3],
			})
		}
	}

	return &ResolvedRange{
		BaseSHA: baseSHA,
		HeadSHA: headSHA,
		Commits: commits,
	}, nil
}
