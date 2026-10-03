package backend

import (
	"encoding/json"
	"fmt"
	"reflect"
)

const StageBindingContractVersion = "stage-binding-v1"
const MaxStageBindingPlanBytes = 192 * 1024

type StageRuntimeBinding struct {
	Stage             Role                     `json:"stage"`
	Requirements      ExecutionRequirements    `json:"requirements"`
	SelectionDecision RuntimeSelectionDecision `json:"selection_decision"`
}
type StageBindingPlan struct {
	Mode     string                `json:"mode"`
	Bindings []StageRuntimeBinding `json:"bindings"`
}

func (p *StageBindingPlan) Binding(role Role) (*StageRuntimeBinding, error) {
	if p != nil {
		for i := range p.Bindings {
			if p.Bindings[i].Stage == role {
				return &p.Bindings[i], nil
			}
		}
	}
	return nil, fmt.Errorf("%w: missing stage %q", ErrInvalidContract, role)
}

// 2026-10-04：只對接受時的共用值快照選擇，不讀 registry 或執行 runtime。
func planStageBindings(mode string, request RuntimeSelectionRequest, descriptors []RuntimeDescriptor, priority []string) (*StageBindingPlan, error) {
	requirements, err := RequirementsForMode(mode)
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = "direct"
	}
	p := &StageBindingPlan{Mode: mode, Bindings: make([]StageRuntimeBinding, 0, len(requirements))}
	for _, req := range requirements {
		decision, err := resolveSelection(request, []ExecutionRequirements{req}, descriptors, priority)
		if err != nil {
			return nil, fmt.Errorf("stage %s: %w", req.Role, err)
		}
		p.Bindings = append(p.Bindings, StageRuntimeBinding{Stage: req.Role, Requirements: req, SelectionDecision: *decision})
	}
	if err := p.checkSize(); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *StageBindingPlan) checkSize() error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(raw) > MaxStageBindingPlanBytes {
		return fmt.Errorf("%w: stage plan size limit exceeded", ErrInvalidContract)
	}
	return nil
}

func contractFromPlan(p *StageBindingPlan, requested string) (*ExecutionContract, error) {
	striker, err := p.Binding(RoleStriker)
	if err != nil {
		return nil, err
	}
	d := striker.SelectionDecision
	match, err := MatchCapabilities([]ExecutionRequirements{striker.Requirements}, d.Selected)
	if err != nil {
		return nil, err
	}
	requirements, err := RequirementsForMode(p.Mode)
	if err != nil {
		return nil, err
	}
	return &ExecutionContract{ContractVersion: StageBindingContractVersion, PolicyVersion: CapabilityContractVersion, Mode: p.Mode, Requirements: requirements, RequestedBackend: requested, Runtime: d.Selected, Match: match, SelectionDecision: &d, StageBindingPlan: p}, nil
}

// 2026-10-04：以封存資料重建決策查核一致性，不使用當前 registry/priority。
func (c *ExecutionContract) validateStagePlan(mode, id string) error {
	p := c.StageBindingPlan
	if p == nil || p.Mode != mode || c.Mode != mode || c.PolicyVersion != CapabilityContractVersion {
		return fmt.Errorf("%w: stage plan version/mode mismatch", ErrInvalidContract)
	}
	req, err := RequirementsForMode(mode)
	if err != nil {
		return err
	}
	if len(p.Bindings) != len(req) || !reflect.DeepEqual(req, c.Requirements) {
		return fmt.Errorf("%w: stage requirements mismatch", ErrInvalidContract)
	}
	if err = p.checkSize(); err != nil {
		return err
	}
	requested := c.RequestedBackend
	if requested == "" {
		requested = "opencode"
	}
	request := RuntimeSelectionRequest{Mode: "explicit", RuntimeID: requested}
	if requested == "auto" {
		request = RuntimeSelectionRequest{Mode: "auto"}
	}
	var shared []RuntimeDescriptor
	var sharedPriority []string
	for i, b := range p.Bindings {
		if b.Stage != req[i].Role || !reflect.DeepEqual(b.Requirements, req[i]) {
			return fmt.Errorf("%w: stage identity/order/requirements mismatch", ErrInvalidContract)
		}
		d := b.SelectionDecision
		descriptors := make([]RuntimeDescriptor, 0, len(d.Candidates))
		for _, candidate := range d.Candidates {
			descriptors = append(descriptors, candidate.Runtime)
		}
		if i == 0 {
			shared = descriptors
			sharedPriority = d.Priority
		} else if !reflect.DeepEqual(descriptors, shared) || !reflect.DeepEqual(d.Priority, sharedPriority) {
			return fmt.Errorf("%w: stages must share candidates and priority", ErrInvalidContract)
		}
		expected, err := resolveSelection(request, []ExecutionRequirements{b.Requirements}, descriptors, d.Priority)
		if err != nil || !reflect.DeepEqual(expected, &d) {
			return fmt.Errorf("%w: sealed stage %s selection mismatch", ErrInvalidContract, b.Stage)
		}
	}
	expected, err := contractFromPlan(p, c.RequestedBackend)
	if err != nil || expected.Runtime.ID != id || !reflect.DeepEqual(c.Runtime, expected.Runtime) || !reflect.DeepEqual(c.Match, expected.Match) || !reflect.DeepEqual(c.SelectionDecision, expected.SelectionDecision) {
		return fmt.Errorf("%w: Striker compatibility projection mismatch", ErrInvalidContract)
	}
	return nil
}
