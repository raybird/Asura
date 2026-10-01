package engine

import (
	"encoding/json"
	"testing"
)

func TestLifecycleLegacySealedSnapshot(t *testing.T) {
	const raw = `{"project_memories":[{"id":"a","workspace":"/a","content":"old","source":"manual","created_at":"2026-09-01T00:00:00Z"}]}`
	var ec ExecutionContext
	if err := json.Unmarshal([]byte(raw), &ec); err != nil {
		t.Fatal(err)
	}
	if len(ec.ProjectMemories) != 1 || ec.ProjectMemories[0].Lifecycle != "active" || ec.ProjectMemories[0].IsDurable {
		t.Fatalf("MLIFE-001 legacy snapshot default %+v", ec.ProjectMemories)
	}
}
