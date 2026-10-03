package backend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// 2026-10-04：RTINV-001/003/004/007/012，單元红燈只透過既有 admission API。
func TestInventoryContractSnapshot(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockBackend{id: "codex", caps: []Capability{CapWorkspaceWrite, CapReadOnlySandbox}})
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "omp", caps: []Capability{CapWorkspaceWrite}}, err: errors.New(strings.Repeat("secret-sentinel", 10000))})
	c, err := AdmitRuntime(context.Background(), reg, "three-face", "auto")
	if err != nil {
		t.Fatalf("healthy candidate blocked: %v", err)
	}
	raw, _ := json.Marshal(c)
	var view struct {
		ContractVersion string `json:"contract_version"`
		Inventory       *struct {
			Entries []struct {
				RuntimeID    string `json:"runtime_id"`
				Availability string `json:"availability_state"`
				Reason       string `json:"reason_code"`
			} `json:"entries"`
		} `json:"inventory_snapshot"`
	}
	json.Unmarshal(raw, &view)
	if view.ContractVersion != "runtime-inventory-v1" || view.Inventory == nil || len(view.Inventory.Entries) != 2 {
		t.Fatalf("missing inventory: %s", raw)
	}
	if view.Inventory.Entries[0].RuntimeID != "codex" || view.Inventory.Entries[0].Availability != "available" || view.Inventory.Entries[1].RuntimeID != "omp" || view.Inventory.Entries[1].Availability != "unavailable" {
		t.Fatalf("wrong inventory: %s", raw)
	}
	if strings.Contains(string(raw), "secret-sentinel") {
		t.Fatal("probe error leaked")
	}
}
