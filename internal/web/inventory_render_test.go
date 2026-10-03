package web

import (
	"asura/internal/backend"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

type unavailableWebRuntime struct{ webStageRuntime }

func (b *unavailableWebRuntime) Version(context.Context) (string, error) {
	return "", errors.New("secret-sentinel")
}

// 2026-10-04：RTINV-014，真 Go 契約渲染 mixed inventory 與 stage/guard。
func TestInventoryWebRender(t *testing.T) {
	reg := backend.NewRegistry()
	reg.Register(&webStageRuntime{id: "codex", caps: []backend.Capability{backend.CapWorkspaceWrite}})
	reg.Register(&unavailableWebRuntime{webStageRuntime{id: "omp", caps: []backend.Capability{backend.CapWorkspaceWrite}}})
	c, err := backend.AdmitRuntime(context.Background(), reg, "direct", "auto")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node required")
	}
	start := strings.Index(indexHTML, "  function renderCapabilityAudit(task) {")
	end := strings.Index(indexHTML, "  function renderMemoryAudit(task) {")
	script := `const capabilityAudit={textContent:''};` + indexHTML[start:end] + `
 const assert=require('node:assert/strict'); const c=` + string(raw) + `;
 renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c})});
 for(const value of ['Inventory snapshot','codex registration=registered availability=available','omp registration=registered availability=unavailable','probe_error','Stage striker：codex'])assert.ok(capabilityAudit.textContent.includes(value),capabilityAudit.textContent);
 assert.ok(!capabilityAudit.textContent.includes('secret-sentinel'));
 c.inventory_snapshot.policy_version='future'; renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c})});assert.match(capabilityAudit.textContent,/unsupported\/unavailable/);
 c.contract_version='capability-v1';delete c.inventory_snapshot;renderCapabilityAudit({resolved_context:JSON.stringify({execution_contract:c})});assert.match(capabilityAudit.textContent,/Inventory：legacy\/unsealed/);
 console.log('PASS mixed inventory, legacy and unknown policy audit');`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("render: %v %s", err, out)
	}
}
