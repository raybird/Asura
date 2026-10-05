package pipeline

import (
	"asura/internal/audit"
	"asura/internal/backend"
	"asura/internal/engine"
	"context"
	"errors"
)

func observe(ctx context.Context, ec *engine.ExecutionContext, e audit.Event) error {
	if ec == nil || ec.Audit == nil {
		return nil
	}
	return ec.Audit.Record(ctx, e)
}
func observedRun(ctx context.Context, ec *engine.ExecutionContext, b backend.Backend, req *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	version := ""
	if ec != nil {
		version = ec.AuditRuntimeVersion
	}
	event := audit.Event{Kind: "run_intent", Stage: req.Role, State: "running", Reason: "none", Layer: "runtime", RuntimeID: b.ID(), RuntimeVersion: version}
	if err := observe(ctx, ec, event); err != nil {
		return nil, err
	}
	res, err := b.Run(ctx, req)
	event.Kind = "run_returned"
	event.State = "completed"
	event.Reason = "none"
	if err != nil {
		event.State = "failed"
		event.Reason = "runtime_failed"
		if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
			event.State = "cancelled"
			event.Reason = "cancelled"
		}
	}
	if saveErr := observe(ctx, ec, event); saveErr != nil {
		return res, errors.Join(err, saveErr)
	}
	if err != nil {
		return res, &audit.Failure{Reason: event.Reason, Layer: "runtime", Cause: err}
	}
	return res, nil
}
func stageOutcome(ctx context.Context, ec *engine.ExecutionContext, role backend.Role, err error, fallbackReason, fallbackLayer string) error {
	reason, layer := audit.Classify(err, fallbackReason, fallbackLayer)
	state := "completed"
	if err != nil {
		state = "failed"
	}
	if reason == "cancelled" {
		state = "cancelled"
	}
	return observe(ctx, ec, audit.Event{Kind: "stage_outcome", Stage: role, State: state, Reason: reason, Layer: layer})
}
