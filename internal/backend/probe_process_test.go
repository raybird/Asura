package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 2026-10-04：RTINV-012/013，真實子程序的輸出與整個進程組界線。
func TestInventoryVersionProcessBounds(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime")
	for _, script := range []string{"echo v1", "head -c 100000 /dev/zero | tr '\\000' x", "echo secret-sentinel >&2; exit 1"} {
		os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0700)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		version, err := ProbeVersionCommand(ctx, path)
		cancel()
		if script == "echo v1" {
			if err != nil || version != "v1" {
				t.Fatal("version fixture failed")
			}
		} else if err == nil || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("output overflow/error leaked")
		}
	}
}
func TestInventoryVersionProcessCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime")
	pidFile := filepath.Join(root, "pid")
	os.WriteFile(path, []byte("#!/bin/sh\necho $$ > '"+pidFile+"'\nsleep 30\n"), 0700)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := ProbeVersionCommand(ctx, path); done <- err }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture not started")
		}
		time.Sleep(time.Millisecond)
	}
	var pid int
	raw, _ := os.ReadFile(pidFile)
	fmt.Sscanf(string(raw), "%d", &pid)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("process failed to stop")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("probe leader remains")
	}
}
