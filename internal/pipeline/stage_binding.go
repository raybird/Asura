package pipeline

import (
	"asura/internal/audit"
	"asura/internal/backend"
	"asura/internal/engine"
	"context"
	"errors"
	"fmt"
)

// 2026-10-04：新契約只解析封存 ID，不 select、不使用 constructor fallback。
func (o *PipelineOrchestrator) guardStage(ctx context.Context, ec *engine.ExecutionContext, role backend.Role, version bool) (bound *engine.ExecutionContext, match *backend.CapabilityMatch, observedVersion string, resultErr error) {
	scope := "precheck"
	if version {
		scope = "stage_execution"
		if err := observe(ctx, ec, audit.Event{Kind: "guard_started", Stage: role, Scope: scope, State: "running", Reason: "none", Layer: "guard"}); err != nil {
			return nil, nil, "", err
		}
	}
	reason := "contract_invalid"
	observedID := ""
	defer func() {
		state := "passed"
		code := "none"
		if resultErr != nil {
			state = "failed"
			code = reason
			if errors.Is(resultErr, context.Canceled) || ctx.Err() == context.Canceled {
				code = "cancelled"
			}
		}
		if err := observe(ctx, ec, audit.Event{Kind: "guard_result", Stage: role, Scope: scope, State: state, Reason: code, Layer: "guard", RuntimeID: observedID, RuntimeVersion: observedVersion}); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	binding, err := ec.ExecutionContract.StageBindingPlan.Binding(role)
	if err != nil {
		return nil, nil, "", err
	}
	id := binding.SelectionDecision.Selected.ID
	reason = "runtime_missing"
	if o.registry == nil {
		return nil, nil, "", fmt.Errorf("%w: sealed stage %s runtime %s unavailable", backend.ErrBackendNotFound, role, id)
	}
	b, err := o.registry.Get(id)
	if err != nil {
		return nil, nil, "", err
	}
	observedID = b.ID()
	if b.ID() != id {
		observedID = audit.Correlation(b.ID())
		reason = "identity_mismatch"
		return nil, nil, "", fmt.Errorf("%w: stage runtime identity mismatch", backend.ErrInvalidContract)
	}
	reason = "capability_mismatch"
	m, err := backend.MatchCapabilities([]backend.ExecutionRequirements{binding.Requirements}, backend.RuntimeDescriptor{ID: b.ID(), Capabilities: b.Capabilities()})
	if err == nil && !m.Compatible {
		err = &backend.CapabilityAdmissionError{RequestedBackend: id, Match: m}
	}
	if err != nil {
		return nil, &m, "", err
	}
	ver := ""
	if version {
		if ec.ExecutionContract.ContractVersion == backend.InventoryContractVersion {
			reason = "runtime_unavailable"
			snapshot, e := backend.CaptureInventory(ctx, []backend.RegisteredRuntime{{ID: id, Backend: b}})
			if e != nil {
				return nil, &m, "", e
			}
			if snapshot.Entries[0].AvailabilityState != backend.Available {
				return nil, &m, "", &backend.InventoryAdmissionError{Snapshot: snapshot, Cause: backend.ErrBackendUnavailable}
			}
			descriptor := snapshot.Entries[0].Descriptor
			reason = "capability_mismatch"
			m, e = backend.MatchCapabilities([]backend.ExecutionRequirements{binding.Requirements}, *descriptor)
			if e != nil {
				return nil, &m, "", e
			}
			if !m.Compatible {
				return nil, &m, "", &backend.CapabilityAdmissionError{RequestedBackend: id, Match: m}
			}
			ver = descriptor.Version
		} else {
			ver, _ = b.Version(ctx)
		}
	}
	m.BackendVersion = ver
	copy := *ec
	copy.BoundBackend = b
	copy.AuditRuntimeVersion = ver
	return &copy, &m, ver, nil
}
