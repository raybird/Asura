package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 2026-10-04：STGBIND-002/003，既有 admission 入口必須封存逐階段計畫。
func TestStageBindingAdmission(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "omp", caps: []Capability{CapWorkspaceWrite}}})
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "codex", caps: []Capability{CapWorkspaceWrite, CapReadOnlySandbox}}})
	c, err := AdmitRuntime(context.Background(), reg, "three-face", "auto", []string{"omp", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	var wire struct {
		Plan *struct {
			Bindings []struct {
				Stage    Role
				Decision RuntimeSelectionDecision `json:"selection_decision"`
			}
		} `json:"stage_binding_plan"`
	}
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Plan == nil || len(wire.Plan.Bindings) != 3 {
		t.Fatalf("missing atomic stage plan: %s", data)
	}
	for i, want := range []string{"codex", "omp", "codex"} {
		if got := wire.Plan.Bindings[i].Decision.Selected.ID; got != want {
			t.Fatalf("stage %d selected %s want %s", i, got, want)
		}
	}
	if c.Runtime.ID != "omp" {
		t.Fatal("compatibility projection must be Striker")
	}
}

// 2026-10-04：STGBIND-001/005/007/008/015，固定字面快照與單欄 mutation。
func TestStageBindingDeterminismAndValidation(t *testing.T) {
	descriptors := []RuntimeDescriptor{
		{ID: "omp", Version: "v1", CapturedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), Capabilities: []Capability{CapWorkspaceWrite}},
		{ID: "codex", Version: "v1", CapturedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), Capabilities: []Capability{CapReadOnlySandbox, CapWorkspaceWrite}},
	}
	request := RuntimeSelectionRequest{Mode: "auto"}
	plan, err := planStageBindings("three-face", request, descriptors, []string{"omp", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	reversed := []RuntimeDescriptor{descriptors[1], descriptors[0]}
	second, err := planStageBindings("three-face", request, reversed, []string{"omp", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(plan)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("snapshot order changed plan")
	}
	for i, want := range []string{"codex", "omp", "codex"} {
		if plan.Bindings[i].SelectionDecision.Selected.ID != want {
			t.Fatal("wrong literal decision")
		}
		if i != 1 && !reflect.DeepEqual(plan.Bindings[i].SelectionDecision.Eligible, []string{"codex"}) {
			t.Fatal("OMP eligible for read-only stage")
		}
	}
	c, err := contractFromPlan(plan, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Validate("three-face", "omp"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	changes := map[string]func(*ExecutionContract){
		"runtime-id": func(c *ExecutionContract) { c.StageBindingPlan.Bindings[0].SelectionDecision.Selected.ID = "omp" },
		"version":    func(c *ExecutionContract) { c.StageBindingPlan.Bindings[0].SelectionDecision.Selected.Version = "v2" },
		"requirements": func(c *ExecutionContract) {
			c.StageBindingPlan.Bindings[0].Requirements.RequiredCapabilities = []Capability{CapWorkspaceWrite}
		},
		"decision": func(c *ExecutionContract) {
			c.StageBindingPlan.Bindings[0].SelectionDecision.Eligible = []string{"omp"}
		},
		"policy":   func(c *ExecutionContract) { c.StageBindingPlan.Bindings[0].SelectionDecision.PolicyVersion = "future" },
		"identity": func(c *ExecutionContract) { c.StageBindingPlan.Bindings[0].Stage = "future" },
		"order": func(c *ExecutionContract) {
			c.StageBindingPlan.Bindings[0], c.StageBindingPlan.Bindings[1] = c.StageBindingPlan.Bindings[1], c.StageBindingPlan.Bindings[0]
		},
		"missing":   func(c *ExecutionContract) { c.StageBindingPlan.Bindings = c.StageBindingPlan.Bindings[:2] },
		"plan-mode": func(c *ExecutionContract) { c.StageBindingPlan.Mode = "direct" },
		"candidate": func(c *ExecutionContract) {
			c.StageBindingPlan.Bindings[0].SelectionDecision.Candidates[0].Runtime.Version = "v2"
		},
		"priority": func(c *ExecutionContract) {
			c.StageBindingPlan.Bindings[0].SelectionDecision.Priority = []string{"codex", "omp"}
		},
		"projection":       func(c *ExecutionContract) { c.Runtime.ID = "codex" },
		"missing-plan":     func(c *ExecutionContract) { c.StageBindingPlan = nil },
		"unknown-version":  func(c *ExecutionContract) { c.ContractVersion = "future" },
		"legacy-with-plan": func(c *ExecutionContract) { c.ContractVersion = CapabilityContractVersion },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var clone ExecutionContract
			json.Unmarshal(raw, &clone)
			change(&clone)
			if clone.Validate("three-face", "omp") == nil {
				t.Fatal("tampered contract accepted")
			}
		})
	}
	direct, err := planStageBindings("direct", request, descriptors, []string{"omp", "codex"})
	if err != nil || len(direct.Bindings) != 1 || direct.Bindings[0].Stage != RoleStriker {
		t.Fatalf("direct %+v %v", direct, err)
	}
	explicit, err := planStageBindings("three-face", RuntimeSelectionRequest{Mode: "explicit", RuntimeID: "codex"}, descriptors[1:], nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range explicit.Bindings {
		if binding.SelectionDecision.Selected.ID != "codex" || binding.SelectionDecision.Request.Mode != "explicit" {
			t.Fatal("explicit drift")
		}
	}
}

func TestStageBindingPlanBounds(t *testing.T) {
	req, _ := RequirementsForMode("three-face")
	for _, size := range []int{33, 1} {
		descriptors := make([]RuntimeDescriptor, size)
		for i := range descriptors {
			descriptors[i] = RuntimeDescriptor{ID: fmt.Sprintf("r%d", i), Version: "v1", CapturedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), Capabilities: []Capability{CapWorkspaceWrite, CapReadOnlySandbox}}
		}
		if size == 1 {
			descriptors[0].Version = strings.Repeat("v", MaxSelectionDecisionBytes)
		}
		if _, err := planStageBindings("three-face", RuntimeSelectionRequest{Mode: "auto"}, descriptors, nil); err == nil {
			t.Fatal("oversize candidates accepted")
		}
	}
	p := &StageBindingPlan{Mode: "three-face", Bindings: []StageRuntimeBinding{{Stage: RoleOracle, Requirements: req[0], SelectionDecision: RuntimeSelectionDecision{PolicyID: strings.Repeat("x", MaxStageBindingPlanBytes)}}}}
	if err := p.checkSize(); err == nil {
		t.Fatal("oversize plan accepted")
	}
}
