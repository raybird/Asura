package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

// 2026-10-21：issue-73 Task 2.5（SCN-009，Q13）。testdata 是升級前（main 1826d5f）在本機抓的清冊快照，
// 代表升級前就已封存的排隊任務與 cron；加入 version_unverified 後仍必須通過驗證。
func TestInventorySnapshotBeforeVersionVerificationStillValid(t *testing.T) {
	raw, err := os.ReadFile("testdata/inventory-v1-pre-issue-73.json")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot InventorySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("archived snapshot rejected after upgrade: %v", err)
	}
}
