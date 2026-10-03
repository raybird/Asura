package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/backend"
)

// 2026-10-03：RTCAP-014，真版本子程序不能忽略 admission 的有限期限。
func TestCapabilityVersionProbeDeadline(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{cfg: AdapterConfig{BinaryPath: bin, ExpectedVersion: "1.18"}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := backend.CaptureRuntime(ctx, a)
	if err == nil {
		t.Fatal("expired probe accepted")
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("version probe ignored deadline: %s", elapsed)
	}
}
