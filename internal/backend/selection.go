package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const SelectionPolicyID = "deterministic-priority"
const SelectionPolicyVersion = "v1"
const MaxRuntimeCandidates = 32
const MaxSelectionDecisionBytes = 64 * 1024

type RuntimeSelectionRequest struct {
	Mode      string `json:"mode"`
	RuntimeID string `json:"runtime_id,omitempty"`
}
type RuntimeCandidate struct {
	Runtime RuntimeDescriptor `json:"runtime"`
	Match   CapabilityMatch   `json:"match"`
}
type RuntimeRejection struct {
	RuntimeID string `json:"runtime_id"`
	Reason    string `json:"reason"`
}
type RuntimeSelectionDecision struct {
	Request       RuntimeSelectionRequest `json:"request"`
	Candidates    []RuntimeCandidate      `json:"candidates"`
	Eligible      []string                `json:"eligible"`
	Rejected      []RuntimeRejection      `json:"rejected"`
	Selected      RuntimeDescriptor       `json:"selected"`
	PolicyID      string                  `json:"policy_id"`
	PolicyVersion string                  `json:"policy_version"`
	Priority      []string                `json:"priority"`
}

// 2026-10-03：設定只表達偏好；未列者由候選集合按預設規則補齊。
func ParseRuntimePriority(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	result := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		id := strings.TrimSpace(part)
		switch id {
		case "opencode", "codex", "antigravity", "omp":
		default:
			return nil, fmt.Errorf("invalid runtime priority ID %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate runtime priority ID %q", id)
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func effectivePriority(ids, preferred []string) []string {
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	present := map[string]bool{}
	for _, id := range ids {
		present[id] = true
	}
	for _, id := range preferred {
		if present[id] && !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	remaining := append([]string(nil), ids...)
	sort.Slice(remaining, func(i, j int) bool {
		a, b := remaining[i], remaining[j]
		if a == "opencode" || b == "omp" {
			return a != b
		}
		if b == "opencode" || a == "omp" {
			return false
		}
		return a < b
	})
	for _, id := range remaining {
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result
}

func resolveSelection(request RuntimeSelectionRequest, requirements []ExecutionRequirements, descriptors []RuntimeDescriptor, preferred []string) (*RuntimeSelectionDecision, error) {
	if request.Mode != "auto" && request.Mode != "explicit" || request.Mode == "auto" && request.RuntimeID != "" || request.Mode == "explicit" && request.RuntimeID == "" {
		return nil, fmt.Errorf("%w: invalid selection request", ErrInvalidContract)
	}
	if len(descriptors) > MaxRuntimeCandidates {
		return nil, fmt.Errorf("%w: candidate limit exceeded", ErrInvalidContract)
	}
	if request.Mode == "explicit" && (len(descriptors) != 1 || descriptors[0].ID != request.RuntimeID) {
		return nil, fmt.Errorf("%w: explicit candidate mismatch", ErrInvalidContract)
	}
	d := &RuntimeSelectionDecision{Request: request, Candidates: []RuntimeCandidate{}, Eligible: []string{}, Rejected: []RuntimeRejection{}, PolicyID: SelectionPolicyID, PolicyVersion: SelectionPolicyVersion, Priority: []string{}}
	if request.Mode == "explicit" {
		d.PolicyID = "explicit"
	}
	descriptors = append([]RuntimeDescriptor(nil), descriptors...)
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	ids := []string{}
	eligible := map[string]bool{}
	for i, r := range descriptors {
		if r.ID == "" || r.Version == "" || r.CapturedAt.IsZero() || i > 0 && descriptors[i-1].ID == r.ID {
			return nil, fmt.Errorf("%w: invalid candidate identity", ErrInvalidContract)
		}
		m, err := MatchCapabilities(requirements, r)
		if err != nil {
			return nil, err
		}
		d.Candidates = append(d.Candidates, RuntimeCandidate{Runtime: r, Match: m})
		ids = append(ids, r.ID)
		if m.Compatible {
			d.Eligible = append(d.Eligible, r.ID)
			eligible[r.ID] = true
		} else {
			d.Rejected = append(d.Rejected, RuntimeRejection{RuntimeID: r.ID, Reason: fmt.Sprintf("missing capabilities: %v", m.Missing)})
		}
	}
	if request.Mode == "auto" {
		d.Priority = effectivePriority(ids, preferred)
	} else {
		d.Priority = ids
	}
	for _, id := range d.Priority {
		if eligible[id] {
			for _, c := range d.Candidates {
				if c.Runtime.ID == id {
					d.Selected = c.Runtime
					break
				}
			}
			break
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxSelectionDecisionBytes {
		return nil, fmt.Errorf("%w: selection decision size limit exceeded", ErrInvalidContract)
	}
	if d.Selected.ID == "" {
		return nil, fmt.Errorf("%w: no eligible runtime requirements=%v candidates=%v rejected=%v", ErrUnsupportedCapability, requirements, d.Candidates, d.Rejected)
	}
	return d, nil
}

func admitAutoRuntime(ctx context.Context, reg Registry, mode string, requirements []ExecutionRequirements, priority [][]string) (*ExecutionContract, error) {
	if mode == "" {
		mode = "direct"
	}
	runtimes := reg.Snapshot()
	if len(runtimes) > MaxRuntimeCandidates {
		return nil, fmt.Errorf("%w: candidate limit exceeded", ErrInvalidContract)
	}
	descriptors := make([]RuntimeDescriptor, 0, len(runtimes))
	for _, b := range runtimes {
		r, err := CaptureRuntime(ctx, b)
		if err != nil {
			return nil, fmt.Errorf("auto candidate %q: %w", b.ID(), err)
		}
		descriptors = append(descriptors, r)
	}
	var preferred []string
	if len(priority) > 0 {
		preferred = priority[0]
	}
	d, err := resolveSelection(RuntimeSelectionRequest{Mode: "auto"}, requirements, descriptors, preferred)
	if err != nil {
		return nil, err
	}
	match, err := MatchCapabilities(requirements, d.Selected)
	if err != nil {
		return nil, err
	}
	return &ExecutionContract{ContractVersion: CapabilityContractVersion, PolicyVersion: CapabilityContractVersion, Mode: mode, Requirements: requirements, RequestedBackend: "auto", Runtime: d.Selected, Match: match, SelectionDecision: d}, nil
}
