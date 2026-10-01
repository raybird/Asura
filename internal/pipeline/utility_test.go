package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"asura/internal/engine"
	"asura/internal/recall"
)

func TestCriticUtilityContract(t *testing.T) {
	for _, tc := range []struct{ name, usage, want string }{
		{"valid", `[{"memory_id":"auto","state":"used"}]`, "used"},
		{"missing", `null`, "unknown"},
		{"wrong_type", `42`, "unknown"},
		{"oversized", `[{"memory_id":"auto","state":"used","padding":"` + strings.Repeat("x", 8193) + `"}]`, "unknown"},
		{"over_count", `[{"memory_id":"auto","state":"used"},{"memory_id":"a","state":"unused"},{"memory_id":"b","state":"unknown"},{"memory_id":"c","state":"unknown"},{"memory_id":"d","state":"unknown"}]`, "unknown"},
		{"fabricated", `[{"memory_id":"auto","state":"used"},{"memory_id":"foreign","state":"used"}]`, "unknown"},
		{"explicit", `[{"memory_id":"explicit","state":"used"}]`, "unknown"},
		{"duplicate", `[{"memory_id":"auto","state":"used"},{"memory_id":"auto","state":"unused"}]`, "unknown"},
		{"invalid_state", `[{"memory_id":"auto","state":"helpful"}]`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage := NewCriticStage(func(context.Context, string, string) (string, error) {
				return `{"verdict":"pass","memory_usage":` + tc.usage + `}`, nil
			})
			ec := &engine.ExecutionContext{Recall: &recall.Decision{AutoIDs: []string{"auto"}, ExplicitIDs: []string{"explicit"}}}
			review, err := stage.Execute(context.Background(), ec, &OraclePlan{}, "done")
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(review)
			var wire struct {
				Usage []struct {
					ID         string `json:"memory_id"`
					State      string `json:"state"`
					Provenance string `json:"provenance"`
				} `json:"memory_usage"`
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Usage) != 1 || wire.Usage[0].ID != "auto" || wire.Usage[0].State != tc.want {
				t.Fatalf("UFL-004 expected auto=%s, got %s", tc.want, data)
			}
			if tc.want == "used" && wire.Usage[0].Provenance != "estimated" {
				t.Fatalf("model signal must be estimated: %s", data)
			}
		})
	}
}

func TestCriticUtilityPrompt(t *testing.T) {
	stage := NewCriticStage(nil)
	base, _ := stage.Prepare(&engine.ExecutionContext{OriginalPrompt: "test"}, &OraclePlan{}, "done")
	if strings.Contains(base.Prompt, "memory_usage") {
		t.Fatal("off prompt changed")
	}
	req, _ := stage.Prepare(&engine.ExecutionContext{OriginalPrompt: "test", Recall: &recall.Decision{AutoIDs: []string{"auto"}}}, &OraclePlan{}, "done")
	if !strings.Contains(req.Prompt, "memory_usage") || !strings.Contains(req.Prompt, `["auto"]`) {
		t.Fatal("UFL-004 missing bounded usage schema/allowlist")
	}
}
