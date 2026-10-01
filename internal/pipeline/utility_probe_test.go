package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/engine"
	"asura/internal/recall"
)

func TestUtilityLiveProbe(t *testing.T) {
	dir := os.Getenv("ASURA_UFL_PROBE_DIR")
	if dir == "" {
		t.Skip("set ASURA_UFL_PROBE_DIR to preserve live probe evidence")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command("opencode", "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "1.18.31" {
		t.Fatalf("fixed probe binary: %s %v", version, err)
	}
	fixtures := []struct{ Prompt, Used, Unused, Output string }{
		{"Explain project retry safety", "Use request key iris-42 for deduplication.", "Backup retention is 73 days.", "Retry uses request key iris-42 for deduplication; backup retention is outside this change."},
		{"Explain workspace isolation", "Require exact canonical workspace equality.", "The UI accent color is amber-17.", "Storage access requires exact canonical workspace equality. UI colors were not involved."},
		{"Explain prompt admission", "Reserve 617 output tokens before admission.", "Release notes use heading violet-9.", "Admission subtracts a reserve of 617 output tokens. Release notes were not part of this task."},
	}
	correct := 0
	for i, f := range fixtures {
		for j := 0; j < 3; j++ {
			ec := &engine.ExecutionContext{Workspace: t.TempDir(), OriginalPrompt: f.Prompt, Recall: &recall.Decision{AutoIDs: []string{"used-memory", "unused-memory", "unknown-memory"}}, ProjectMemories: []engine.ProjectMemorySnapshot{
				{ID: "used-memory", Content: f.Used}, {ID: "unused-memory", Content: f.Unused}, {ID: "unknown-memory", Content: "The execution environment may enforce a hidden prerequisite; no evidence is available in the output."},
			}}
			stage := NewCriticStage(func(ctx context.Context, ws, prompt string) (string, error) {
				name := filepath.Join(dir, string(rune('a'+i))+string(rune('1'+j)))
				if err := os.WriteFile(name+".prompt.txt", []byte(prompt), 0600); err != nil {
					return "", err
				}
				cmd := exec.CommandContext(ctx, "opencode", "run", "--pure", "--model", "openai/gpt-5.6-sol", "--format", "json", prompt)
				cmd.Dir = ws
				cmd.Env = append(os.Environ(), `OPENCODE_PERMISSION={"*":"deny"}`)
				raw, err := cmd.CombinedOutput()
				_ = os.WriteFile(name+".jsonl", raw, 0600)
				if err != nil {
					return "", err
				}
				var output strings.Builder
				for _, line := range strings.Split(string(raw), "\n") {
					var event struct {
						Type string `json:"type"`
						Part struct {
							Text string `json:"text"`
						} `json:"part"`
					}
					if json.Unmarshal([]byte(line), &event) == nil && event.Type == "text" {
						output.WriteString(event.Part.Text)
					}
				}
				return output.String(), nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			review, err := stage.Execute(ctx, ec, &OraclePlan{Goal: f.Prompt}, f.Output)
			cancel()
			if err != nil {
				t.Fatalf("fixture %d trial %d: %v", i, j, err)
			}
			data, _ := json.MarshalIndent(review, "", "  ")
			_ = os.WriteFile(filepath.Join(dir, string(rune('a'+i))+string(rune('1'+j))+".review.json"), data, 0600)
			if len(review.MemoryUsage) == 3 && review.MemoryUsage[0].State == "used" && review.MemoryUsage[1].State == "unused" && review.MemoryUsage[2].State == "unknown" {
				correct++
			}
			t.Logf("fixture=%d trial=%d usage=%+v", i, j, review.MemoryUsage)
		}
	}
	if correct < 8 {
		t.Fatalf("UFL-004 estimated consistency %d/9, need >=8/9", correct)
	}
	t.Logf("UFL-004 fixed OpenCode 1.18.31 / openai/gpt-5.6-sol: %d/9", correct)
}

func TestUtilityArchivedProbeReplay(t *testing.T) {
	for _, fixture := range []string{"a1", "a2", "a3", "b1", "b2", "b3", "c1", "c2", "c3"} {
		t.Run(fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "issues", "issue-29", "evidence", "critic-probe", fixture+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for _, line := range strings.Split(string(raw), "\n") {
				var event struct {
					Type string `json:"type"`
					Part struct {
						Text string `json:"text"`
					} `json:"part"`
				}
				if json.Unmarshal([]byte(line), &event) == nil && event.Type == "text" {
					output.WriteString(event.Part.Text)
				}
			}
			stage := NewCriticStage(func(context.Context, string, string) (string, error) { return output.String(), nil })
			ec := &engine.ExecutionContext{Recall: &recall.Decision{AutoIDs: []string{"used-memory", "unused-memory", "unknown-memory"}}}
			review, err := stage.Execute(context.Background(), ec, &OraclePlan{}, "archived response")
			if err != nil || len(review.MemoryUsage) != 3 || review.MemoryUsage[0].State != "used" || review.MemoryUsage[1].State != "unused" || review.MemoryUsage[2].State != "unknown" {
				t.Fatalf("UFL-004 archived response %+v %v", review, err)
			}
		})
	}
}
