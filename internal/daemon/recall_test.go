package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"asura/internal/contextwin"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

func TestRecallBudget(t *testing.T) {
	s, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := storage.Memory{ID: "a", Workspace: "/a", Content: "SQLite workspace 隔離，禁止跨專案讀取", Source: "manual", CreatedAt: time.Now().UTC()}
	if err = s.CreateMemory(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{store: s}
	got, err := d.recallMemories("/a", "修正 SQLite 隔離", "direct", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision.Budget.EstimatedTokens != 33 || got.Decision.Budget.FormattedBytes != 76 || got.Decision.Budget.FirstStage.LimitSource != contextwin.LimitSourceUnknown {
		t.Fatalf("wrapper estimate: %+v", got.Decision)
	}
	for _, policy := range []contextwin.AdmissionPolicy{contextwin.PolicyObserve, contextwin.PolicyReject} {
		d.cfg.ContextConfig = contextwin.Config{ContextLimit: 1, Policy: policy}
		got, err = d.recallMemories("/a", "SQLite", "direct", []string{"explicit"}, []storage.Memory{{ID: "explicit", Workspace: "/a", Content: "original full content"}})
		if err != nil || len(got.Memories) != 0 || got.Decision.Skipped["stage_limit"] != 1 {
			t.Fatalf("base overrun: %+v %v", got, err)
		}
	}
	d.cfg.ContextConfig = contextwin.Config{}
	got, err = d.recallMemories("/a", "SQLite", "three-face", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed := memoryContext("/a", "SQLite", got.Memories)
	prepared, _ := pipeline.NewOracleStage(nil).Prepare(sealed)
	estimate := contextwin.NewHeuristicEstimator(.2).Estimate("oracle", prepared.Prompt)
	if got.Decision.Budget.FirstStage.Estimate != estimate {
		t.Fatal("first stage differs from actual Prepare")
	}
	// 2026-09-22：格式化 token 上限內外由真實 ComposePrompt 計算。
	large := m
	large.ID = "b"
	large.Content = "SQLite " + strings.Repeat("中", 1100)
	if err = s.CreateMemory(context.Background(), large); err != nil {
		t.Fatal(err)
	}
	got, err = d.recallMemories("/a", "SQLite", "direct", nil, nil)
	if err != nil || got.Decision.Skipped["token_limit"] != 1 {
		t.Fatalf("token bound %+v %v", got, err)
	}
}

func TestRecallJSONCompatibility(t *testing.T) {
	var old engine.ExecutionContext
	if err := json.Unmarshal([]byte(`{"original_prompt":"old","project_memories":[{"id":"a","content":"old"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Recall != nil || engine.ComposePrompt("old", &old) != "### Project Context\n- [a]: old\n\nold" {
		t.Fatal("legacy context changed")
	}
	raw, err := json.Marshal(old)
	if err != nil || strings.Contains(string(raw), `"recall"`) {
		t.Fatalf("off serialization %s %v", raw, err)
	}
	if recallFingerprint("/a", "p", "direct", nil, "opencode") != recallFingerprint("/a", "p", "direct", []string{}, "opencode") {
		t.Fatal("nil/empty drift")
	}
	if recallFingerprint("/a", "p", "direct", nil, "opencode") == calculateTaskFingerprint("/a", "p", "direct", nil, "opencode") {
		t.Fatal("off/bounded collision")
	}
}
