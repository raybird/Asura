package opencode

import (
	"context"
	"errors"
	"fmt"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// RoleRoutingBackend 封裝 OpenCode 的唯讀（Oracle/Critic）與寫入（Striker）兩個真實 Adapter，
// 形成單一邏輯 Backend ("opencode")，維護 Phase 3C 唯讀沙箱安全邊界
type RoleRoutingBackend struct {
	readOnly backend.Backend
	striker  backend.Backend
}

var _ backend.Backend = (*RoleRoutingBackend)(nil)

// NewRoleRoutingBackend 建立依執行角色自動路由至對應安全邊界 Adapter 的邏輯後端
func NewRoleRoutingBackend(readonly, striker backend.Backend) *RoleRoutingBackend {
	return &RoleRoutingBackend{
		readOnly: readonly,
		striker:  striker,
	}
}

// ID 回傳後端唯一識別
func (b *RoleRoutingBackend) ID() string {
	return "opencode"
}

// Version 回傳目前後端版本號
func (b *RoleRoutingBackend) Version(ctx context.Context) (string, error) {
	if b.striker != nil {
		return b.striker.Version(ctx)
	}
	if b.readOnly != nil {
		return b.readOnly.Version(ctx)
	}
	return "", errors.New("no underlying adapter configured")
}

// Capabilities 回傳 OpenCode 支援的能力清單
func (b *RoleRoutingBackend) Capabilities() []backend.Capability {
	return []backend.Capability{
		backend.CapReadOnlySandbox,
		backend.CapWorkspaceWrite,
		backend.CapProcessCancellation,
		backend.CapStructuredEvents,
	}
}

// HasCapability 檢查是否具備指定能力
func (b *RoleRoutingBackend) HasCapability(cap backend.Capability) bool {
	for _, c := range b.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}

// Run 依據 ExecutionRequest.Role 自動分派至唯讀或寫入 Adapter
func (b *RoleRoutingBackend) Run(ctx context.Context, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if req == nil {
		return nil, errors.New("execution request cannot be nil")
	}

	switch req.Role {
	case backend.RoleOracle, backend.RoleCritic:
		if b.readOnly != nil {
			return b.readOnly.Run(ctx, req)
		}
		return nil, errors.New("read-only adapter not configured for oracle/critic role")
	case backend.RoleStriker:
		if b.striker != nil {
			return b.striker.Run(ctx, req)
		}
		return nil, errors.New("striker adapter not configured for striker role")
	default:
		return nil, fmt.Errorf("unsupported backend role: %q", req.Role)
	}
}

// VerificationSpec 2026-10-22：issue-73，唯讀與寫入角色必須解析到同一個身分，驗證才涵蓋兩者。
func (b *RoleRoutingBackend) VerificationSpec(ctx context.Context) (verification.Spec, error) {
	var specs []verification.Spec
	for _, adapter := range []backend.Backend{b.readOnly, b.striker} {
		subject, ok := adapter.(verification.Subject)
		if !ok {
			return verification.Spec{}, errors.New("opencode role does not support verification")
		}
		spec, err := subject.VerificationSpec(ctx)
		if err != nil {
			return verification.Spec{}, err
		}
		specs = append(specs, spec)
	}
	if specs[0].Record.Identity != specs[1].Record.Identity || specs[0].Builtin != specs[1].Builtin {
		return verification.Spec{}, errors.New("read-only and striker roles resolve to different opencode installations")
	}
	return specs[1], nil
}
