package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

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

func TestBoundedMemoryQueryCanceledOnIPCDisconnect(t *testing.T) {
	for _, test := range []struct {
		action  string
		request IPCRequest
	}{
		{action: "memory_page", request: IPCRequest{Action: "memory_page", Workspace: "/workspace", Limit: 1}},
		{action: "memory_detail_bounded", request: IPCRequest{Action: "memory_detail_bounded", Workspace: "/workspace", MemoryID: "m1"}},
	} {
		t.Run(test.action, func(t *testing.T) {
			server, client := net.Pipe()
			requests, connCtx, cancel := readIPCRequests(server)
			t.Cleanup(cancel)
			t.Cleanup(func() { _ = server.Close() })
			t.Cleanup(func() { _ = client.Close() })

			if err := json.NewEncoder(client).Encode(test.request); err != nil {
				t.Fatal(err)
			}
			decoded, ok := <-requests
			if !ok || decoded.err != nil {
				t.Fatalf("decoded=%+v, open=%v", decoded, ok)
			}

			store := &blockingMemoryQuerier{
				started:  make(chan memoryQueryStart, 1),
				canceled: make(chan error, 1),
			}
			done := make(chan IPCResponse, 1)
			go func() {
				done <- queryBoundedMemory(connCtx, store, test.action, "/workspace", decoded.request)
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
