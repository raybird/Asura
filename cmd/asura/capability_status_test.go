package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/backend"
	"asura/internal/engine"
	"asura/internal/storage"
)

// 2026-10-03：RTCAP-016，保留 shell run，禁止偽造 runtime 快照或探測 CLI。
func TestCapabilityShellRunCompatibility(t *testing.T) {
	t.Setenv("ASURA_SOCKET", "")
	for _, id := range []string{"opencode", "codex"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "runtime-called")
			if err := os.WriteFile(filepath.Join(dir, id), []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 99\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			db := filepath.Join(dir, "tasks.db")
			var out, stderr bytes.Buffer
			if code := handleRun([]string{"--db", db, "--workspace", dir, "--id", "shell", "--backend", id, "printf shell-proof"}, &out, &stderr); code != 0 {
				t.Fatalf("run exit %d: %s", code, stderr.String())
			}
			s, err := storage.NewSQLiteStore(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			task, err := s.GetTask(context.Background(), "shell")
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != storage.StatusCompleted || task.Result != "shell-proof" || task.Backend != id {
				t.Fatalf("shell behavior changed: %+v", task)
			}
			if strings.Contains(task.ResolvedContext+task.PipelineTrace, "execution_contract") {
				t.Fatal("shell run fabricated runtime contract")
			}
			out.Reset()
			if code := handleStatus([]string{"--db", db, "shell"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "Capability contract: legacy/unavailable") {
				t.Fatalf("missing unavailable audit: %s / %s", out.String(), stderr.String())
			}
			stored, err := s.GetTask(context.Background(), "shell")
			if err != nil || stored.ResolvedContext != task.ResolvedContext || stored.PipelineTrace != task.PipelineTrace {
				t.Fatal("audit rewrote shell history")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("shell path called runtime: %v", err)
			}
		})
	}
}

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
