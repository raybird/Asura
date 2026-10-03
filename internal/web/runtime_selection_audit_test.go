package web

import (
	"os/exec"
	"strings"
	"testing"
)

// 2026-10-03：RTSEL-009/010，頁面渲染保存值與未知 policy 保守讀取。
func TestRuntimeSelectionAuditRender(t *testing.T) {
	start := strings.Index(indexHTML, "  function renderCapabilityAudit(task) {")
	end := strings.Index(indexHTML, "  function renderMemoryAudit(task) {")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	script := `const capabilityAudit={textContent:''};` + indexHTML[start:end] + `
const assert=require('node:assert/strict');
const decision={request:{mode:'auto'},selected:{id:'codex',version:'v1'},policy_id:'deterministic-priority',policy_version:'v1',priority:['codex','omp'],eligible:['codex'],rejected:[{runtime_id:'omp',reason:'missing read_only_sandbox'}],candidates:[{runtime:{id:'codex'}}]};
const c={contract_version:'capability-v1',policy_version:'capability-v1',requested_backend:'auto',runtime:{id:'codex',version:'v1'},match:{compatible:true},selection_decision:decision};
renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c})});
assert.match(capabilityAudit.textContent,/Requested selection：auto/);assert.match(capabilityAudit.textContent,/codex v1/);assert.match(capabilityAudit.textContent,/deterministic-priority v1/);assert.match(capabilityAudit.textContent,/read_only_sandbox/);
decision.policy_version='future';renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c})});assert.match(capabilityAudit.textContent,/unsupported\/unavailable/);
console.log('PASS selection snapshot audit and unsupported policy');`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v %s", err, out)
	}
}
