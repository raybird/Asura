package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type mockBackend struct {
	id   string
	caps []Capability
}

func (m *mockBackend) ID() string { return m.id }
func (m *mockBackend) Version(ctx context.Context) (string, error) {
	return "1.0.0", nil
}
func (m *mockBackend) Capabilities() []Capability { return m.caps }
func (m *mockBackend) HasCapability(cap Capability) bool {
	for _, c := range m.caps {
		if c == cap {
			return true
		}
	}
	return false
}
func (m *mockBackend) Run(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error) {
	return &ExecutionResult{
		BackendID: m.id,
		Output:    "mock output",
	}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	b1 := &mockBackend{id: "opencode"}
	b2 := &mockBackend{id: "codex"}

	if err := reg.Register(b1); err != nil {
		t.Fatalf("failed to register b1: %v", err)
	}
	if err := reg.Register(b2); err != nil {
		t.Fatalf("failed to register b2: %v", err)
	}

	// 驗證重複註冊被拒絕
	if err := reg.Register(b1); err == nil {
		t.Errorf("expected error on duplicate register, got nil")
	}

	// 驗證取得
	got, err := reg.Get("opencode")
	if err != nil {
		t.Fatalf("failed to get opencode: %v", err)
	}
	if got.ID() != "opencode" {
		t.Errorf("expected ID opencode, got %s", got.ID())
	}

	// 驗證查無後端返回 ErrBackendNotFound (MBACK-003)
	_, err = reg.Get("unknown-backend")
	if err == nil {
		t.Fatalf("expected error on unknown backend, got nil")
	}
	if !errors.Is(err, ErrBackendNotFound) {
		t.Errorf("expected error to wrap ErrBackendNotFound, got %v", err)
	}

	// 驗證預設後端為第一個註冊的
	if reg.Default().ID() != "opencode" {
		t.Errorf("expected default backend to be opencode, got %v", reg.Default())
	}

	// 驗證設定預設後端
	if err := reg.SetDefault("codex"); err != nil {
		t.Fatalf("failed to set default to codex: %v", err)
	}
	if reg.Default().ID() != "codex" {
		t.Errorf("expected default backend to be codex, got %v", reg.Default())
	}
}

func TestRegistry_Concurrency(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&mockBackend{id: "opencode"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = reg.Get("opencode")
			_ = reg.List()
			_ = reg.Default()
		}()
	}
	wg.Wait()
}
