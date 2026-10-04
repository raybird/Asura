package audit

import (
	"asura/internal/backend"
	"encoding/json"
)

// 2026-10-04：fold 只驗證已持久化資料，不 probe、selection 或執行。
func Fold(h Header, events []Event, taskState string) (Record, error) {
	r := Record{Header: h, Events: events, TaskState: taskState, Completeness: "partial", Integrity: "valid", Stages: []Stage{}}
	if h.Validate() != nil || len(events) > MaxEvents {
		return r, ErrInvalid
	}
	switch taskState {
	case "pending", "queued", "running", "cancelling", "completed", "failed", "cancelled":
	default:
		return r, ErrInvalid
	}
	for _, b := range h.Admission.Bindings {
		ver := ""
		for _, x := range h.Admission.Runtimes {
			if x.ID == b.Selected {
				ver = x.Version
			}
		}
		r.Stages = append(r.Stages, Stage{Name: b.Stage, SealedRuntime: b.Selected, SealedVersion: ver, Status: "not_started", Reachability: "not_reached", Guard: Guard{Scope: "stage_execution", State: "not_reached", Reason: "none"}, Precheck: Guard{Scope: "precheck", State: "not_reached", Reason: "none"}, Reason: "none"})
	}
	previous := Digest(h)
	seen := map[string]bool{}
	recovered := false
	started := false
	for i, e := range events {
		if e.Validate() != nil || e.Seq != i+1 || e.PreviousDigest != previous || seen[e.Key] || r.Terminal != nil {
			return r, ErrInvalid
		}
		previous = e.Digest
		seen[e.Key] = true
		index := -1
		for j := range r.Stages {
			if r.Stages[j].Name == e.Stage {
				index = j
			}
		}
		if e.Stage != "" && index < 0 {
			return r, ErrInvalid
		}
		var s *Stage
		if index >= 0 {
			s = &r.Stages[index]
		}
		switch e.Kind {
		case "task_started":
			if started || i != 0 {
				return r, ErrInvalid
			}
			started = true
		case "guard_started":
			if !started || s.Status != "not_started" || s.Guard.State != "not_reached" {
				return r, ErrInvalid
			}
			for j := 0; j < index; j++ {
				if r.Stages[j].Status != "completed" {
					return r, ErrInvalid
				}
			}
			s.Reachability = "reached"
			s.Status = "running"
			s.StartedAt = &e.At
		case "guard_result":
			g := Guard{Scope: e.Scope, State: e.State, Reason: e.Reason, ObservedID: e.RuntimeID, ObservedVersion: e.RuntimeVersion, At: e.At}
			if e.Scope == "precheck" {
				if s.Precheck.State != "not_reached" || s.RunIntent {
					return r, ErrInvalid
				}
				s.Precheck = g
			} else {
				if s.Reachability != "reached" || s.Guard.State != "not_reached" || s.RunIntent {
					return r, ErrInvalid
				}
				s.Guard = g
			}
			if e.State == "passed" && (e.Reason != "none" || e.RuntimeID != s.SealedRuntime) {
				return r, ErrInvalid
			}
			if e.State == "failed" {
				if e.Reason == "none" {
					return r, ErrInvalid
				}
				s.Status = "failed"
				if e.Reason == "cancelled" {
					s.Status = "cancelled"
				}
				s.Reason = e.Reason
				s.FailureLayer = "guard"
				s.CompletedAt = &e.At
			}
		case "run_intent":
			if s.Guard.State != "passed" || s.Status != "running" || s.RunIntent || e.RuntimeID != s.SealedRuntime || e.Reason != "none" {
				return r, ErrInvalid
			}
			s.RunIntent = true
			s.ObservedRuntime = e.RuntimeID
			s.ObservedVersion = e.RuntimeVersion
		case "run_returned":
			if !s.RunIntent || s.RunReturned || s.Status != "running" || e.RuntimeID != s.ObservedRuntime || e.RuntimeVersion != s.ObservedVersion {
				return r, ErrInvalid
			}
			s.RunReturned = true
			s.ActualRuntime = e.RuntimeID
			s.ActualVersion = e.RuntimeVersion
			s.RunOutcome = e.State
			if e.State != "completed" {
				s.Reason = e.Reason
				s.FailureLayer = e.Layer
			}
		case "stage_outcome":
			if s.Status != "running" || e.State == "completed" && (!s.RunReturned || s.RunOutcome != "completed") {
				return r, ErrInvalid
			}
			s.Status = e.State
			s.Reason = e.Reason
			s.FailureLayer = e.Layer
			s.CompletedAt = &e.At
		case "cancel_requested":
		case "recovery":
			recovered = true
			for j := range r.Stages {
				if r.Stages[j].Status == "running" {
					r.Stages[j].Status = "interrupted"
					r.Stages[j].Reason = "interrupted"
					r.Stages[j].FailureLayer = "recovery"
				}
			}
		case "terminal":
			terminal := &Terminal{State: e.State, Reason: e.Reason, FailureLayer: e.Layer, ModelCalls: "unknown", InvocationEvidence: "confirmed_returns", At: e.At}
			for _, stage := range r.Stages {
				if stage.Status == "completed" {
					terminal.CompletedStages++
				}
				if stage.RunIntent {
					terminal.RunIntents++
				}
				if stage.RunReturned {
					terminal.ConfirmedReturns++
				}
				if stage.Status == "failed" || stage.Status == "cancelled" || stage.Status == "interrupted" {
					if terminal.FailingStage == "" {
						terminal.FailingStage = stage.Name
						if terminal.Reason == "execution_failed" || terminal.Reason == "cancelled" {
							terminal.Reason = stage.Reason
							terminal.FailureLayer = stage.FailureLayer
						}
					}
				}
			}
			if terminal.RunIntents != terminal.ConfirmedReturns {
				terminal.InvocationEvidence = "unknown"
			}
			if e.State != taskState {
				return r, ErrInvalid
			}

			if e.State == "cancelled" {
				terminal.CancelPhase = "before_stage"
				for _, stage := range r.Stages {
					if stage.Status != "completed" {
						if stage.RunIntent {
							terminal.CancelPhase = "during_run"
						} else if stage.Reachability == "reached" || stage.Guard.Reason == "cancelled" || stage.Precheck.Reason == "cancelled" {
							terminal.CancelPhase = "during_guard"
						}
						break
					}
				}
			}
			if e.Layer == "critic" {
				terminal.FailingStage = backend.RoleCritic
			}
			r.Terminal = terminal
		}
	}
	terminalState := taskState == "completed" || taskState == "failed" || taskState == "cancelled"
	if terminalState && r.Terminal == nil {
		return r, ErrInvalid
	}
	if r.Terminal != nil && !recovered && h.ContractVersion == backend.InventoryContractVersion {
		r.Completeness = "complete"
		for _, s := range r.Stages {
			if s.Status == "running" || s.RunIntent && !s.RunReturned || r.Terminal.State == "completed" && s.Status != "completed" {
				r.Completeness = "partial"
			}
		}
	}
	raw, _ := json.Marshal(r)
	if len(raw) > MaxRecordBytes {
		return r, ErrInvalid
	}
	return r, nil
}

// 2026-10-04：跨程序 projection 必須與原事件 fold 相等，不接受第二份 truth。
func (r *Record) Validate() error {
	if r == nil {
		return ErrInvalid
	}
	raw, _ := json.Marshal(r)
	if len(raw) > MaxRecordBytes {
		return ErrInvalid
	}
	if r.Completeness == "legacy" {
		if len(r.Events) != 0 || r.Terminal != nil || len(r.Header.TaskID) > 256 || len(r.Stages) > 3 || r.Integrity != "valid" && r.Integrity != "unavailable" {
			return ErrInvalid
		}
		if r.Header.ContractDigest != "" && r.Header.Validate() != nil {
			return ErrInvalid
		}
		for _, s := range r.Stages {
			if s.Status != "not_started" || s.Reachability != "not_reached" || s.RunIntent || s.RunReturned || s.ActualRuntime != "" {
				return ErrInvalid
			}
		}
		return nil
	}
	expected, err := Fold(r.Header, r.Events, r.TaskState)
	if err != nil || Digest(expected) != Digest(r) {
		return ErrInvalid
	}
	return nil
}
