package web

import (
	"asura/internal/backend"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// 2026-10-04：STGBIND-014，Go admission 產生的真 plan 經頁面 JavaScript 渲染。
func TestStageBindingAudit(t *testing.T) {
	reg := backend.NewRegistry()
	reg.Register(&webStageRuntime{id: "omp", caps: []backend.Capability{backend.CapWorkspaceWrite}})
	reg.Register(&webStageRuntime{id: "codex", caps: []backend.Capability{backend.CapWorkspaceWrite, backend.CapReadOnlySandbox}})
	c, err := backend.AdmitRuntime(context.Background(), reg, "three-face", "auto", []string{"omp", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	start := strings.Index(indexHTML, "  function renderCapabilityAudit(task) {")
	end := strings.Index(indexHTML, "  function renderMemoryAudit(task) {")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required for audit evidence")
	}
	script := `const capabilityAudit={textContent:''};` + indexHTML[start:end] + `
const assert=require('node:assert/strict');const c=` + string(data) + `;
const trace={stages:[{name:'oracle',backend_id:'codex',backend_version:'v2',execution_guard:{compatible:true}},{name:'striker',backend_id:'omp',backend_version:'v1',execution_guard:{compatible:true}}]};
renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c}),pipeline_trace:JSON.stringify(trace)});
for(const expected of ['Stage oracle：codex v1','Stage striker：omp v1','Stage critic：codex v1','Actual runtime：codex v2','Execution guard：compatible'])assert.ok(capabilityAudit.textContent.includes(expected),capabilityAudit.textContent);
c.stage_binding_plan.bindings[0].selection_decision.policy_version='future';renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c})});assert.match(capabilityAudit.textContent,/unsupported\/unavailable/);
console.log('PASS stage audit and unsupported policy');`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("render %v %s", err, out)
	}
}

type webStageRuntime struct {
	id   string
	caps []backend.Capability
}

func (b *webStageRuntime) ID() string                              { return b.id }
func (b *webStageRuntime) Version(context.Context) (string, error) { return "v1", nil }
func (b *webStageRuntime) Capabilities() []backend.Capability      { return b.caps }
func (b *webStageRuntime) HasCapability(c backend.Capability) bool {
	for _, v := range b.caps {
		if c == v {
			return true
		}
	}
	return false
}
func (b *webStageRuntime) Run(context.Context, *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	panic("audit called Run")
}
