package opencode_test

import (
	"context"
	"testing"

	"asura/internal/backend"
	"asura/internal/backend/opencode"
)

type mockSubAdapter struct {
	calledRole backend.Role
	calls      int
}

func (m *mockSubAdapter) ID() string                              { return "mock" }
func (m *mockSubAdapter) Version(ctx context.Context) (string, error) { return "1.0", nil }
func (m *mockSubAdapter) Capabilities() []backend.Capability      { return nil }
func (m *mockSubAdapter) HasCapability(cap backend.Capability) bool { return true }
func (m *mockSubAdapter) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	m.calls++
	m.calledRole = req.Role
	return &backend.ExecutionResult{BackendID: "opencode", Output: "mock output"}, nil
}

func TestRoleRoutingBackend_RoutesByRole(t *testing.T) {
	ro := &mockSubAdapter{}
	wr := &mockSubAdapter{}

	logical := opencode.NewRoleRoutingBackend(ro, wr)
	if logical.ID() != "opencode" {
		t.Fatalf("expected ID 'opencode', got %s", logical.ID())
	}

	ctx := context.Background()

	// 1. Oracle -> ro
	_, err := logical.Run(ctx, &backend.ExecutionRequest{Role: backend.RoleOracle, Prompt: "p1"})
	if err != nil {
		t.Fatalf("run oracle: %v", err)
	}
	if ro.calls != 1 || ro.calledRole != backend.RoleOracle {
		t.Errorf("expected oracle to route to readonly adapter, got ro.calls=%d, role=%s", ro.calls, ro.calledRole)
	}
	if wr.calls != 0 {
		t.Errorf("expected striker adapter not called, got %d", wr.calls)
	}

	// 2. Critic -> ro
	_, err = logical.Run(ctx, &backend.ExecutionRequest{Role: backend.RoleCritic, Prompt: "p2"})
	if err != nil {
		t.Fatalf("run critic: %v", err)
	}
	if ro.calls != 2 || ro.calledRole != backend.RoleCritic {
		t.Errorf("expected critic to route to readonly adapter, got ro.calls=%d, role=%s", ro.calls, ro.calledRole)
	}
	if wr.calls != 0 {
		t.Errorf("expected striker adapter not called, got %d", wr.calls)
	}

	// 3. Striker -> wr
	_, err = logical.Run(ctx, &backend.ExecutionRequest{Role: backend.RoleStriker, Prompt: "p3"})
	if err != nil {
		t.Fatalf("run striker: %v", err)
	}
	if wr.calls != 1 || wr.calledRole != backend.RoleStriker {
		t.Errorf("expected striker to route to striker adapter, got wr.calls=%d, role=%s", wr.calls, wr.calledRole)
	}
	if ro.calls != 2 {
		t.Errorf("expected readonly adapter not called, got %d", ro.calls)
	}

	// 4. Unknown role -> fail-closed
	_, err = logical.Run(ctx, &backend.ExecutionRequest{Role: backend.Role("unknown_role"), Prompt: "p4"})
	if err == nil {
		t.Fatalf("expected error on unknown role, got nil")
	}
}

