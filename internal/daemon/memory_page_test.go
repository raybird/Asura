package daemon_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/storage"
)

// 2026-09-23：WEBMEM-002、003 的真實 IPC 授權與有界回應。
func TestDaemonBoundedMemoryIPC(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, path := range []string{a, b} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, m := range []storage.Memory{
		{ID: "a1", Workspace: a, Content: strings.Repeat("x", 1<<20)},
		{ID: "a2", Workspace: a, Content: "visible"},
		{ID: "b1", Workspace: b, Content: "secret"},
	} {
		if err := store.CreateMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	socket := filepath.Join(root, "daemon.sock")
	d := daemon.NewDaemon(daemon.Config{SocketPath: socket}, store, auth.NewPolicy([]string{root}))
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	client, err := daemon.NewClient(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	reader, ok := any(client).(interface {
		PageMemories(context.Context, string, int, string) (storage.MemoryPage, error)
		GetMemoryBounded(context.Context, string, string) (*storage.Memory, error)
	})
	if !ok {
		t.Fatal("WEBMEM-002: bounded IPC contract unavailable")
	}
	page, err := reader.PageMemories(ctx, a, 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "a1" || len(page.Items[0].Summary) != 512 || page.NextCursor == "" {
		t.Fatalf("page=%+v, err=%v", page, err)
	}
	if _, err := reader.GetMemoryBounded(ctx, a, "a1"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large detail=%v", err)
	}
	raw, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(raw).Encode(daemon.IPCRequest{Action: "memory_page", Workspace: a, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	wire, err := bufio.NewReader(raw).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) > 512<<10 || strings.Contains(string(wire), strings.Repeat("x", 1024)) {
		t.Fatalf("unbounded IPC page: %d bytes", len(wire))
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := reader.GetMemoryBounded(ctx, a, "a2"); err != nil || got.Content != "visible" {
		t.Fatalf("detail=%+v, err=%v", got, err)
	}
	for _, id := range []string{"b1", "absent"} {
		if _, err := reader.GetMemoryBounded(ctx, a, id); err == nil || err.Error() != "memory not found" {
			t.Fatalf("cross-scope leak %s: %v", id, err)
		}
	}
	if _, err := reader.PageMemories(ctx, link, 1, ""); err == nil || !strings.Contains(err.Error(), "unauthorized workspace") {
		t.Fatalf("escaped scope=%v", err)
	}
	if _, err := reader.PageMemories(ctx, b, 1, page.NextCursor); err == nil || err.Error() != "invalid memory cursor" {
		t.Fatalf("cross-scope cursor=%v", err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	aliased, err := reader.PageMemories(ctx, alias, 1, "")
	if err != nil || len(aliased.Items) != 1 || aliased.Items[0].ID != "a1" {
		t.Fatalf("canonical alias=%+v, err=%v", aliased, err)
	}
	parent, err := reader.PageMemories(ctx, root, 1, "")
	if err != nil || len(parent.Items) != 0 {
		t.Fatalf("parent scope=%+v, err=%v", parent, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.PageMemories(ctx, alias, 1, ""); err == nil || err.Error() != "unauthorized workspace" {
		t.Fatalf("revoked alias=%v", err)
	}
}
