package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/storage"
)

type blockingMemoryQuerier struct {
	started  chan memoryQueryStart
	canceled chan error
}

type memoryQueryStart struct {
	action string
	ctx    context.Context
}

func (q *blockingMemoryQuerier) PageMemories(ctx context.Context, _ string, _ int, _ string) (storage.MemoryPage, error) {
	q.started <- memoryQueryStart{action: "memory_page", ctx: ctx}
	<-ctx.Done()
	q.canceled <- ctx.Err()
	return storage.MemoryPage{}, ctx.Err()
}

func (q *blockingMemoryQuerier) GetMemoryBounded(ctx context.Context, _, _ string) (*storage.Memory, error) {
	q.started <- memoryQueryStart{action: "memory_detail_bounded", ctx: ctx}
	<-ctx.Done()
	q.canceled <- ctx.Err()
	return nil, ctx.Err()
}

func unixConnPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	var server *net.UnixConn
	select {
	case server = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() { _ = server.Close() })
	return server, client
}

func TestBoundedMemoryQueryCanceledOnIPCDisconnect(t *testing.T) {
	for _, test := range []struct {
		action  string
		request IPCRequest
	}{
		{action: "memory_page", request: IPCRequest{Action: "memory_page", Workspace: "/workspace", Limit: 1}},
		{action: "memory_detail_bounded", request: IPCRequest{Action: "memory_detail_bounded", Workspace: "/workspace", MemoryID: "m1"}},
	} {
		t.Run(test.action, func(t *testing.T) {
			server, client := unixConnPair(t)
			connCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			watchIPCDisconnect(connCtx, server, cancel)

			if err := json.NewEncoder(client).Encode(test.request); err != nil {
				t.Fatal(err)
			}
			var req IPCRequest
			if err := json.NewDecoder(server).Decode(&req); err != nil {
				t.Fatal(err)
			}

			store := &blockingMemoryQuerier{
				started:  make(chan memoryQueryStart, 1),
				canceled: make(chan error, 1),
			}
			done := make(chan IPCResponse, 1)
			go func() {
				done <- queryBoundedMemory(connCtx, store, test.action, "/workspace", req)
			}()

			started := <-store.started
			if started.action != test.action {
				t.Fatalf("query action=%s, want %s", started.action, test.action)
			}
			deadline, ok := started.ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > boundedMemoryQueryTimeout {
				t.Fatalf("query deadline=%v, present=%v", deadline, ok)
			}

			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-connCtx.Done():
			case <-time.After(time.Second):
				t.Fatal("IPC disconnect did not cancel the connection context")
			}
			select {
			case err := <-store.canceled:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("query cancellation error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("bounded memory query did not receive IPC cancellation")
			}
			select {
			case resp := <-done:
				if resp.Error != "memory query failed" {
					t.Fatalf("query response=%+v", resp)
				}
			case <-time.After(time.Second):
				t.Fatal("bounded memory query did not return after cancellation")
			}
		})
	}
}

func TestDaemonIPCReplyAfterClientHalfClose(t *testing.T) {
	server, client := unixConnPair(t)
	root := t.TempDir()
	policy := auth.NewPolicy([]string{root})
	d := &Daemon{policy: policy}
	done := make(chan struct{})
	go func() {
		d.handleConn(server)
		close(done)
	}()

	if err := json.NewEncoder(client).Encode(IPCRequest{Action: "projects"}); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var resp IPCResponse
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("half-close reply: %v", err)
	}
	roots := policy.AllowedRoots()
	if len(resp.Projects) != 1 || len(roots) != 1 || resp.Projects[0] != roots[0] {
		t.Fatalf("projects response=%+v, want %v", resp.Projects, roots)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("daemon connection did not finish after half-close")
	}
}
