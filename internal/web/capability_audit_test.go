package web

import (
	"os/exec"
	"strings"
	"testing"
)

// 2026-10-03：RTCAP-007／011／016，執行頁面實際 render 函式，DOM 使用 textContent seam。
func TestCapabilityAuditRender(t *testing.T) {
	start := strings.Index(indexHTML, "  function renderCapabilityAudit(task) {")
	end := strings.Index(indexHTML, "  function renderMemoryAudit(task) {")
	if start < 0 || end < start {
		t.Fatal("capability audit renderer missing")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable; render probe requires Node")
	}
	script := `const capabilityAudit={textContent:''};` + indexHTML[start:end] + `
const assert=require('node:assert/strict');
renderCapabilityAudit({});assert.equal(capabilityAudit.textContent,'Runtime 契約：legacy/unavailable');
renderCapabilityAudit({resolved_context:'bad'});assert.match(capabilityAudit.textContent,/unsupported\/unavailable/);
renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:{contract_version:'future'}})});assert.match(capabilityAudit.textContent,/unsupported\/unavailable/);
const c={contract_version:'capability-v1',policy_version:'capability-v1',requested_backend:'opencode',runtime:{id:'opencode',version:'V1<script>',capabilities:['workspace_write'],captured_at:'2026-10-03T00:00:00Z'},match:{compatible:true,required:['workspace_write'],available:['workspace_write'],missing:[]}};
renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c}),pipeline_trace:JSON.stringify({guard_error:'capability removed'})});
assert.match(capabilityAudit.textContent,/opencode V1<script>/);
assert.match(capabilityAudit.textContent,/workspace_write/);
assert.match(capabilityAudit.textContent,/compatible/);
assert.match(capabilityAudit.textContent,/capability removed/);
console.log('PASS: sealed/legacy/unsupported audit rendering, immutable admission and separate guard');`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
}
