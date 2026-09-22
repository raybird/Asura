package backend

import (
	"encoding/json"
	"testing"
)

func int64Ptr(v int64) *int64 {
	return &v
}

func TestTokenUsage_NullablePresence(t *testing.T) {
	// 1. 全空 (所有欄位皆為 unknown / nil)
	uEmpty := TokenUsage{}
	bEmpty, err := json.Marshal(uEmpty)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if string(bEmpty) != "{}" {
		t.Errorf("expected empty JSON '{}', got %s", string(bEmpty))
	}

	var parsedEmpty TokenUsage
	if err := json.Unmarshal(bEmpty, &parsedEmpty); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsedEmpty.InputTokens != nil || parsedEmpty.OutputTokens != nil || parsedEmpty.TotalTokens != nil || parsedEmpty.CachedTokens != nil {
		t.Errorf("expected all fields to be nil for unknown usage")
	}

	// 2. 部分缺失 vs 明確 0
	// 模擬 Codex: 有 input, output, cached, reasoning，但明確沒有 total_tokens (nil)；且 cached_tokens 明確回報為 0
	uCodex := TokenUsage{
		InputTokens:     int64Ptr(100),
		OutputTokens:    int64Ptr(20),
		CachedTokens:    int64Ptr(0), // 明確 0
		ReasoningTokens: int64Ptr(0), // 明確 0
		// TotalTokens 為 nil (缺失/未提供)
	}

	bCodex, err := json.Marshal(uCodex)
	if err != nil {
		t.Fatalf("marshal codex error: %v", err)
	}

	var parsedCodex TokenUsage
	if err := json.Unmarshal(bCodex, &parsedCodex); err != nil {
		t.Fatalf("unmarshal codex error: %v", err)
	}

	if parsedCodex.TotalTokens != nil {
		t.Errorf("expected TotalTokens to be nil (unknown), got %v", *parsedCodex.TotalTokens)
	}
	if parsedCodex.CachedTokens == nil || *parsedCodex.CachedTokens != 0 {
		t.Errorf("expected CachedTokens to be explicit 0, got %v", parsedCodex.CachedTokens)
	}
	if parsedCodex.InputTokens == nil || *parsedCodex.InputTokens != 100 {
		t.Errorf("expected InputTokens 100, got %v", parsedCodex.InputTokens)
	}
}

func TestDataProvenance_Constants(t *testing.T) {
	if ProvenanceExact != "exact" {
		t.Errorf("expected ProvenanceExact == 'exact'")
	}
	if ProvenanceEstimated != "estimated" {
		t.Errorf("expected ProvenanceEstimated == 'estimated'")
	}
	if ProvenanceUnknown != "unknown" {
		t.Errorf("expected ProvenanceUnknown == 'unknown'")
	}
}
