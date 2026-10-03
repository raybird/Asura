package pipeline

import (
	"asura/internal/backend"
	"asura/internal/engine"
	"context"
	"fmt"
)

// 2026-10-04：新契約只解析封存 ID，不 select、不使用 constructor fallback。
func (o *PipelineOrchestrator) guardStage(ctx context.Context, ec *engine.ExecutionContext, role backend.Role, version bool) (*engine.ExecutionContext, *backend.CapabilityMatch, string, error) {
	binding, err := ec.ExecutionContract.StageBindingPlan.Binding(role)
	if err != nil {
		return nil, nil, "", err
	}
	id := binding.SelectionDecision.Selected.ID
	if o.registry == nil {
		return nil, nil, "", fmt.Errorf("%w: sealed stage %s runtime %s unavailable", backend.ErrBackendNotFound, role, id)
	}
	b, err := o.registry.Get(id)
	if err != nil {
		return nil, nil, "", err
	}
	if b.ID() != id {
		return nil, nil, "", fmt.Errorf("%w: stage runtime identity mismatch", backend.ErrInvalidContract)
	}
	m, err := backend.MatchCapabilities([]backend.ExecutionRequirements{binding.Requirements}, backend.RuntimeDescriptor{ID: b.ID(), Capabilities: b.Capabilities()})
	if err == nil && !m.Compatible {
		err = &backend.CapabilityAdmissionError{RequestedBackend: id, Match: m}
	}
	if err != nil {
		return nil, &m, "", err
	}
	ver := ""
	if version {
		ver, _ = b.Version(ctx)
	}
	m.BackendVersion = ver
	copy := *ec
	copy.BoundBackend = b
	return &copy, &m, ver, nil
}
