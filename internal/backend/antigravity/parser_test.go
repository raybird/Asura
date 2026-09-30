package antigravity

import (
	"asura/internal/backend"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 2026-09-30：BADAPT-005／007／009，timeout SUCCESS 的反例不可判成成功。
func TestNativeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		class backend.FailureClassification
		want  string
	}{
		{"access-keyring", backend.FailureNone, "PROBE_OK\n"},
		{"access-expired", backend.FailureAuth, ""},
		{"access-partial", backend.FailureExecution, "PARTIAL_START"},
		{"access-failure", backend.FailureExecution, ""},
		{"side-effect-timeout", backend.FailureExecution, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile("../../../docs/issues/issue-25/probes/agy-1.2.13-" + tc.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var f struct {
				Events []json.RawMessage `json:"events"`
				Stderr string            `json:"stderr"`
			}
			if err = json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, e := range f.Events {
				var compact bytes.Buffer
				if err := json.Compact(&compact, e); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, compact.String())
			}
			r, e := ExtractResultAndEvents(strings.Join(lines, "\n"), f.Stderr)
			if r.FailureClass != tc.class || (e == nil) != (tc.class == backend.FailureNone) {
				t.Fatalf("result=%+v err=%v", r, e)
			}
			if tc.class == backend.FailureNone && r.Output != tc.want {
				t.Fatalf("output %q", r.Output)
			}
			if tc.class != backend.FailureNone && !strings.HasPrefix(r.PartialOutput, tc.want) {
				t.Fatalf("partial %q", r.PartialOutput)
			}
		})
	}
}

// 2026-09-30：BADAPT-007，合成矛盾 carrier 測試，並非真實 vendor quota 證據。
func TestTerminalErrorOverridesSuccess(t *testing.T) {
	r, e := ExtractResultAndEvents(`{"event":"result","result":{"status":"SUCCESS","response":"PARTIAL"}}`, `AGY_ERROR: {"status":"RESOURCE_EXHAUSTED","code_kind":"grpc","error_code":8}`)
	if e == nil || r.FailureClass != backend.FailureRateLimit || r.Output != "" || r.PartialOutput != "PARTIAL" {
		t.Fatalf("result %+v error %v", r, e)
	}
}
