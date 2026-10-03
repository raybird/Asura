package opencode

import (
	"asura/internal/backend"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 2026-10-04：RTINV-001/002/013，配置與兩角色 adapter 必須同時有效；probe 無 Run callback。
func TestInventoryOpenCodeProbe(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "opencode")
	os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1\" = --version ] || exit 9\necho 1.18.31\n"), 0700)
	config := filepath.Join(root, "config")
	os.MkdirAll(filepath.Join(config, "opencode"), 0700)
	os.WriteFile(filepath.Join(config, "opencode", "opencode.json"), []byte("{}"), 0600)
	read := NewAdapter(AdapterConfig{BinaryPath: binary, ConfigDir: config, Isolated: true, DisableAuto: true, Pure: true, DisableProjectConfig: true, OnProcessStarted: func(context.Context, int) { t.Error("execution callback") }})
	write := NewAdapter(AdapterConfig{BinaryPath: binary})
	logical := NewRoleRoutingBackend(read, write)
	s, err := backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: logical}})
	if err != nil || s.Entries[0].AvailabilityState != backend.Available {
		t.Fatalf("probe=%+v %v", s, err)
	}
	os.Remove(binary)
	s, err = backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: logical}})
	if err != nil || s.Entries[0].ReasonCode != backend.ExecutableNotFound {
		t.Fatal("missing binary admitted")
	}
	missing := NewRoleRoutingBackend(nil, write)
	s, err = backend.CaptureInventory(context.Background(), []backend.RegisteredRuntime{{ID: "opencode", Backend: missing}})
	if err != nil || s.Entries[0].ReasonCode != backend.ConfigurationInvalid {
		t.Fatal("missing readonly adapter admitted")
	}
}
