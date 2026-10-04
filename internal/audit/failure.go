package audit

import (
	"context"
	"errors"
)

// 2026-10-04：保留既有診斷 error；audit 僅取得固定 reason/layer。
type Failure struct {
	Reason string
	Layer  string
	Cause  error
}

func (e *Failure) Error() string { return e.Cause.Error() }
func (e *Failure) Unwrap() error { return e.Cause }
func Classify(err error, fallbackReason, fallbackLayer string) (string, string) {
	if err == nil {
		return "none", ""
	}
	if errors.Is(err, ErrPersistence) {
		return "audit_persistence_failed", "persistence"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", "cancellation"
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Reason, failure.Layer
	}
	return fallbackReason, fallbackLayer
}

type failureKey struct{}
type failureInfo struct{ Reason, Layer string }

func WithFailure(ctx context.Context, reason, layer string) context.Context {
	return context.WithValue(ctx, failureKey{}, failureInfo{reason, layer})
}
func TerminalEvent(ctx context.Context, state string) Event {
	e := Event{Kind: "terminal", State: state, Reason: "none"}
	if state == "failed" {
		e.Reason = "execution_failed"
		e.Layer = "task"
	}
	if state == "cancelled" {
		e.Reason = "cancelled"
		e.Layer = "cancellation"
	}
	if info, ok := ctx.Value(failureKey{}).(failureInfo); ok {
		e.Reason = info.Reason
		e.Layer = info.Layer
	}
	return e
}
