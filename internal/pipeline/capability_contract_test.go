package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"asura/internal/backend"
	"asura/internal/engine"
)

// 2026-10-03：RTCAP-007／013／015，接受時契約使用獨立固定 JSON。
const acceptedCapabilityContext = `{"workspace":"/tmp","backend":"opencode","execution_contract":{"contract_version":"capability-v1","policy_version":"capability-v1","mode":"direct","requirements":[{"role":"striker","workspace_access":"workspace-write","required_capabilities":["workspace_write"]}],"requested_backend":"","runtime":{"id":"opencode","version":"V1","capabilities":["workspace_write"],"captured_at":"2026-10-03T00:00:00Z"},"match":{"compatible":true,"required":["workspace_write"],"available":["workspace_write"],"missing":[],"backend_id":"opencode","backend_version":"V1"}}}`

func TestCapabilitySealedGuard(t *testing.T) {
	for _, mutation := range []string{"capability-removed", "runtime-removed", "unknown-version", "unknown-policy", "invalid-match", "upgrade"} {
		t.Run(mutation, func(t *testing.T) {
			var ec engine.ExecutionContext
			fixture := acceptedCapabilityContext
			switch mutation {
			case "unknown-version":
				fixture = strings.Replace(fixture, `"contract_version":"capability-v1"`, `"contract_version":"future"`, 1)
			case "unknown-policy":
				fixture = strings.Replace(fixture, `"policy_version":"capability-v1"`, `"policy_version":"future"`, 1)
			case "invalid-match":
				fixture = strings.Replace(fixture, `"compatible":true`, `"compatible":false`, 1)
			}
			if err := json.Unmarshal([]byte(fixture), &ec); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(ec)
			b := &mockBackendForPipeline{id: "opencode", version: "V2"}
			if mutation != "capability-removed" {
				b.caps = []backend.Capability{backend.CapWorkspaceWrite}
			}
			reg := backend.NewRegistry()
			if mutation != "runtime-removed" {
				reg.Register(b)
			}
			striker := &mockStrikerStage{output: "done"}
			o := NewOrchestrator(&mockOracleStage{}, striker, &mockCriticStage{}).WithRegistry(reg)
			_, trace, err := o.Execute(context.Background(), ModeDirect, &ec)
			after, _ := json.Marshal(ec)
			if string(before) != string(after) {
				t.Fatal("sealed contract mutated")
			}
			if mutation == "upgrade" {
				if err != nil || striker.callCount != 1 || trace == nil {
					t.Fatalf("compatible upgrade refused: %v", err)
				}
				return
			}
			if err == nil || striker.callCount != 0 || b.calls != 0 {
				t.Fatalf("unsafe execution accepted: %v calls %d", err, striker.callCount)
			}
			if trace == nil || trace.ModelCalls != 0 {
				t.Fatal("guard failure not auditable")
			}
			data, _ := json.Marshal(trace)
			var saved map[string]json.RawMessage
			json.Unmarshal(data, &saved)
			if len(saved["execution_contract"]) == 0 || len(saved["guard_error"]) == 0 {
				t.Fatal("guard erased original admission or missing reason")
			}
		})
	}
}

// 2026-10-03：RTCAP-004，registry 在 stage 間改變不造成 mixed runtime 或 fallback。
func TestCapabilityThreeFaceBoundRuntime(t *testing.T) {
	reg := backend.NewRegistry()
	a := &mockBackendForPipeline{id: "opencode", version: "V1", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}}
	b := &mockBackendForPipeline{id: "opencode", version: "V2", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}}
	var roles []backend.Role
	a.runFn = func(_ context.Context, r *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
		roles = append(roles, r.Role)
		out := "done"
		switch r.Role {
		case backend.RoleOracle:
			reg.Unregister("opencode")
			reg.Register(b)
			out = `{"goal":"goal","steps":["one"]}`
		case backend.RoleCritic:
			out = `{"verdict":"pass","summary":"done"}`
		}
		return &backend.ExecutionResult{BackendID: a.id, BackendVersion: a.version, Output: out}, nil
	}
	reg.Register(a)
	o := NewOrchestrator(NewOracleStageWithBackend(a), NewStrikerStageWithBackend(a), NewCriticStageWithBackend(a)).WithRegistry(reg)
	contract, err := o.Preflight(context.Background(), "three-face", "opencode")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-04：此測試刻畫舊 capability-v1 的單一 runtime handle；新版本另測逐 stage guard。
	contract.StageBindingPlan = nil
	contract.ContractVersion = backend.CapabilityContractVersion
	contract.Match, _ = backend.MatchCapabilities(contract.Requirements, contract.Runtime)
	contract.SelectionDecision = nil
	_, trace, err := o.Execute(context.Background(), ModeThreeFace, &engine.ExecutionContext{Backend: "opencode", Workspace: t.TempDir(), ExecutionContract: contract})
	if err != nil || trace.ModelCalls != 3 || a.calls != 3 || b.calls != 0 {
		t.Fatalf("mixed runtime: %v a=%d b=%d", err, a.calls, b.calls)
	}
	if len(roles) != 3 || roles[0] != backend.RoleOracle || roles[1] != backend.RoleStriker || roles[2] != backend.RoleCritic {
		t.Fatalf("roles %v", roles)
	}
}
