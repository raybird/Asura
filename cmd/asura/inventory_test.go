package main

import (
	"bytes"
	"strings"
	"testing"
)

// 2026-10-04：RTINV-014，CLI 新命令可觀察 fixed registry 與 availability。
func TestInventoryCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var out, err bytes.Buffer
	code := RunCLI([]string{"runtime", "inventory"}, &out, &err)
	if code != 0 {
		t.Fatalf("inventory unavailable: %s", err.String())
	}
	for _, want := range []string{"Runtime Inventory", "opencode", "codex", "omp", "antigravity", "registered", "unavailable", "executable_not_found"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}
