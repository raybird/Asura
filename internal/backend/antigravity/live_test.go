package antigravity

import (
	"asura/internal/backend"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 2026-09-30：BADAPT-005，固定 binary 與新版 keyring access-only 直接對照。
func TestLiveAdapter(t *testing.T) {
	if os.Getenv("ASURA_ISSUE25_LIVE") != "1" {
		t.Skip("live OAuth model call opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	r, e := NewAdapter(AdapterConfig{BinaryPath: os.Getenv("ASURA_ISSUE25_AGY")}).Run(ctx, &backend.ExecutionRequest{Role: backend.RoleStriker, Workspace: t.TempDir(), Prompt: "Reply with exactly PROBE_OK"})
	if e != nil || strings.TrimSpace(r.Output) != "PROBE_OK" {
		t.Fatalf("result %+v error %v", r, e)
	}
}
