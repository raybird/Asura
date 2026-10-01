package pipeline

import (
	"asura/internal/engine"
	"encoding/json"
)

type MemoryUsage struct {
	MemoryID   string `json:"memory_id"`
	State      string `json:"state"`
	Provenance string `json:"provenance"`
}

type boundedMemoryUsage json.RawMessage

func (b *boundedMemoryUsage) UnmarshalJSON(data []byte) error {
	if len(data) > 8192 {
		*b = nil
		return nil
	}
	*b = append((*b)[:0], data...)
	return nil
}

func validateMemoryUsage(raw json.RawMessage, ec *engine.ExecutionContext) []MemoryUsage {
	if ec == nil || ec.Recall == nil || len(ec.Recall.AutoIDs) == 0 {
		return nil
	}
	ids := ec.Recall.AutoIDs
	result := make([]MemoryUsage, len(ids))
	allowed := make(map[string]bool, len(ids))
	for i, id := range ids {
		result[i] = MemoryUsage{id, "unknown", "unknown"}
		allowed[id] = true
	}
	var input []struct {
		MemoryID string `json:"memory_id"`
		State    string `json:"state"`
	}
	if len(ids) > 4 || len(raw) == 0 || len(raw) > 8192 || json.Unmarshal(raw, &input) != nil || len(input) > 4 {
		return result
	}
	seen := map[string]string{}
	for _, u := range input {
		if !allowed[u.MemoryID] || seen[u.MemoryID] != "" || (u.State != "used" && u.State != "unused" && u.State != "unknown") {
			return result
		}
		seen[u.MemoryID] = u.State
	}
	for i := range result {
		if state := seen[result[i].MemoryID]; state != "" {
			result[i].State = state
			result[i].Provenance = "estimated"
		}
	}
	return result
}
