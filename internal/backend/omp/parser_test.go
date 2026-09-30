package omp

import (
	"asura/internal/backend"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 2026-09-30：BADAPT-003／007／009，期望值取自已保存的 CLI 對照。
func TestNativeFixtures(t *testing.T) {
	cases := []struct {
		name            string
		class           backend.FailureClassification
		output, partial string
	}{
		{"access-empty-refresh", backend.FailureNone, "PROBE_OK", ""},
		{"nested-rate-limit", backend.FailureRateLimit, "", ""},
		{"nested-partial", backend.FailureExecution, "", "PARTIAL_BEFORE_FAILURE"},
		{"nested-after-tool", backend.FailureExecution, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("../../../docs/issues/issue-25/probes/omp-18.4.4-" + tc.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var f struct {
				Events []json.RawMessage `json:"events"`
				Stderr string            `json:"stderr"`
			}
			if err = json.Unmarshal(data, &f); err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, e := range f.Events {
				var msg map[string]json.RawMessage
				json.Unmarshal(e, &msg)
				kind := msg["type"]
				delete(msg, "type")
				body, _ := json.Marshal(map[string]interface{}{"type": json.RawMessage(kind), "message": msg})
				lines = append(lines, string(body))
			}
			r, e := ExtractResultAndEvents(strings.Join(lines, "\n"), f.Stderr)
			if r.FailureClass != tc.class || r.Output != tc.output || (tc.partial != "" && r.PartialOutput != tc.partial) || (e == nil) != (tc.class == backend.FailureNone) {
				t.Fatalf("result=%+v error=%v", r, e)
			}
		})
	}
}
