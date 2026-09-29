package gitmem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"asura/internal/storage"
)

// FormatFactsSummary 將客觀事實序列化為結構化 JSON 字串，利於儲存庫保存與結構化讀取
func FormatFactsSummary(facts *CaptureFacts) (string, error) {
	if facts == nil {
		return "{}", nil
	}
	bytes, err := json.Marshal(facts)
	if err != nil {
		return "", fmt.Errorf("marshal facts summary: %w", err)
	}
	return string(bytes), nil
}

// GenerateDraftContent 依模式 1（確定性事實生成）產出結構化專案記憶草稿
// 強調高價值決策模板，嚴禁包含 raw diff，維護 Phase 3F 召回純淨性 (H-TBD-4, H-TBD-5)
func GenerateDraftContent(facts *CaptureFacts) string {
	if facts == nil {
		return ""
	}

	var builder strings.Builder
	baseShort := facts.BaseSHA
	if len(baseShort) > 8 {
		baseShort = baseShort[:8]
	}
	headShort := facts.HeadSHA
	if len(headShort) > 8 {
		headShort = headShort[:8]
	}

	builder.WriteString(fmt.Sprintf("# 專案架構決策與變更摘要 (%s..%s)\n\n", baseShort, headShort))
	builder.WriteString(fmt.Sprintf("- 變更區間：%d 個 commits (%s..%s)\n", facts.CommitCount, baseShort, headShort))

	if len(facts.ChangedFiles) > 0 {
		builder.WriteString("- 涉及核心檔案：\n")
		limit := min(len(facts.ChangedFiles), 10)
		for i := 0; i < limit; i++ {
			builder.WriteString(fmt.Sprintf("  * `%s`\n", facts.ChangedFiles[i]))
		}
		if len(facts.ChangedFiles) > limit {
			builder.WriteString(fmt.Sprintf("  * ... 等共 %d 個檔案\n", len(facts.ChangedFiles)))
		}
	}

	if len(facts.Commits) > 0 {
		builder.WriteString("- 變更主題清單：\n")
		for _, c := range facts.Commits {
			builder.WriteString(fmt.Sprintf("  * %s\n", c.Subject))
		}
	}

	builder.WriteString("\n## 架構決策與技術約束（請於審核時補充說明）\n")
	builder.WriteString("- 核心設計決策：\n")
	builder.WriteString("- 取捨考量與技術限制：\n")

	return builder.String()
}

// BuildProvenance 封裝正式記憶之不可變來源證明 (Provenance)
func BuildProvenance(memoryID, candidateID string, facts *CaptureFacts, userContent string, approvedAt time.Time) (*storage.MemoryProvenance, error) {
	if facts == nil {
		return nil, fmt.Errorf("facts cannot be nil")
	}

	shas := make([]string, 0, len(facts.Commits))
	for _, c := range facts.Commits {
		shas = append(shas, c.SHA)
	}
	shasJSON, err := json.Marshal(shas)
	if err != nil {
		return nil, fmt.Errorf("marshal commit shas: %w", err)
	}

	factsSummary, err := FormatFactsSummary(facts)
	if err != nil {
		return nil, err
	}

	defaultDraft := GenerateDraftContent(facts)
	isUserEdited := strings.TrimSpace(userContent) != strings.TrimSpace(defaultDraft)

	return &storage.MemoryProvenance{
		MemoryID:       memoryID,
		CandidateID:    candidateID,
		Workspace:      facts.Workspace,
		RepoID:         facts.RepoID,
		RefName:        facts.RefName,
		BaseSHA:        facts.BaseSHA,
		HeadSHA:        facts.HeadSHA,
		CommitCount:    facts.CommitCount,
		CommitSHAsJSON: string(shasJSON),
		FactsSummary:   factsSummary,
		IsUserEdited:   isUserEdited,
		ApprovedAt:     approvedAt,
	}, nil
}
