package daemon

import (
	"asura/internal/storage"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestUtilityRecallIdentity(t *testing.T) {
	s, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CreateMemory(context.Background(), storage.Memory{ID: "auto", Workspace: "/a", Content: "SQLite", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{store: s}
	got, err := d.recallMemories("/a", "SQLite", "direct", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got.Decision)
	var wire storage.UtilityRecall
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.RecallID == "" || wire.UtilityPolicy != "observe" || wire.UtilityVersion != "utility-v1" || len(wire.Selected) != 1 || wire.Selected[0].WeightPPM != 1000000 {
		t.Fatalf("UFL-002 missing sealed utility identity: %s", b)
	}
}

func TestUtilityPolicyAndFailure(t *testing.T) {
	s, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := &Daemon{store: s, cfg: Config{UtilityPolicy: "invalid"}}
	if err = d.Start(); err == nil {
		t.Fatal("invalid config accepted")
	}
	d.cfg.UtilityPolicy = "apply"
	s.Close()
	if _, err = d.recallMemories("/a", "sqlite", "direct", nil, nil); err == nil {
		t.Fatal("UFL-011 apply DB failure silently accepted")
	}
}

func TestUtilityClientRejectsOversize(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		var req IPCRequest
		_ = json.NewDecoder(server).Decode(&req)
		_, _ = io.WriteString(server, `{"utility_page":{"items":[{"state":"`+strings.Repeat("x", 100000)+`"}]}}`)
	}()
	c := &Client{conn: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.MemoryUtility(ctx, "/a", "m", 1, ""); err == nil {
		t.Fatal("UFL-010 oversized client response accepted")
	}
}
