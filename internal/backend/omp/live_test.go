package omp

import (
	"asura/internal/backend"
	"context"
	"os"
	"testing"
	"time"
)

// 2026-09-30：BADAPT-003，明確開啟時驗證真實固定 CLI，不隱藏 capability probe。
func TestLiveAdapter(t *testing.T) {
	if os.Getenv("ASURA_ISSUE25_LIVE") != "1" {
		t.Skip("live OAuth model call opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	r, e := NewAdapter(AdapterConfig{}).Run(ctx, &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: t.TempDir(), Prompt: "Reply with exactly PROBE_OK"})
	if e != nil || r.Output != "PROBE_OK" {
		t.Fatalf("result %+v error %v", r, e)
	}
}
