package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/storage"
)

// 2026-10-03：RTCAP-007／011／016，status 使用保存值，不讀本機 runtime。
func TestCapabilityStatusPersistedAndLegacy(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	for _, state := range []string{"sealed", "legacy", "future"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "status.db")
			s, err := storage.NewSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			task := &storage.Task{ID: "status-contract", Backend: "opencode", Status: storage.StatusQueued}
			if state != "legacy" {
				runtime := backend.RuntimeDescriptor{ID: "opencode", Version: "V1", Capabilities: []backend.Capability{backend.CapWorkspaceWrite}, CapturedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
				req, _ := backend.RequirementsForMode("direct")
				match, _ := backend.MatchCapabilities(req, runtime)
				contract := &backend.ExecutionContract{ContractVersion: "capability-v1", PolicyVersion: "capability-v1", Mode: "direct", Requirements: req, Runtime: runtime, Match: match}
				if state == "future" {
					contract.ContractVersion = "future"
				}
				data, _ := json.Marshal(engine.ExecutionContext{ExecutionContract: contract})
				task.ResolvedContext = string(data)
			}
			if err := s.CreateTask(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			if code := handleStatus([]string{"--db", path, task.ID}, &out, &stderr); code != 0 {
				t.Fatalf("%s", stderr.String())
			}
			want := "Capability contract: legacy/unavailable"
			if state == "sealed" {
				want = "Capability contract: capability-v1"
			}
			if state == "future" {
				want = "Capability contract: unsupported/unavailable"
			}
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing %q: %s", want, out.String())
			}
			if state == "sealed" {
				for _, literal := range []string{"Runtime snapshot: opencode V1", "Requirements: [workspace_write]", "Capability match: compatible"} {
					if !strings.Contains(out.String(), literal) {
						t.Fatalf("missing %q", literal)
					}
				}
			}
			stored, _ := s.GetTask(context.Background(), task.ID)
			if stored.ResolvedContext != task.ResolvedContext {
				t.Fatal("status rewrote sealed context")
			}
		})
	}
}
