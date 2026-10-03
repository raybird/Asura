package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 2026-10-03：RTSEL-002/003/004/008/013，純 admission 以既有入口重現缺少 auto。
func TestRuntimeSelectionDefaultAndOrder(t *testing.T) {
	for _, ids := range [][]string{{"omp", "codex", "antigravity", "opencode"}, {"opencode", "antigravity", "codex", "omp"}} {
		reg := NewRegistry()
		for _, id := range ids {
			reg.Register(&descriptorBackend{mockBackend: mockBackend{id: id, caps: []Capability{CapWorkspaceWrite}}})
		}
		c, err := AdmitRuntime(context.Background(), reg, "direct", "auto")
		if err != nil {
			t.Fatal(err)
		}
		if c.Runtime.ID != "opencode" {
			t.Fatalf("selected=%s", c.Runtime.ID)
		}
		raw, _ := json.Marshal(c)
		var obj map[string]json.RawMessage
		json.Unmarshal(raw, &obj)
		if len(obj["selection_decision"]) == 0 {
			t.Fatal("missing decision")
		}
		if err = c.Validate("direct", "opencode"); err != nil {
			t.Fatal(err)
		}
		reg.Unregister("opencode")
		c, err = AdmitRuntime(context.Background(), reg, "direct", "auto")
		if err != nil || c.Runtime.ID != "antigravity" {
			t.Fatalf("second selection=%+v %v", c, err)
		}
	}
}

func TestRuntimeSelectionThreeFaceEligibility(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "omp", caps: []Capability{CapWorkspaceWrite}}})
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "codex", caps: []Capability{CapWorkspaceWrite, CapReadOnlySandbox}}})
	c, err := AdmitRuntime(context.Background(), reg, "three-face", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if c.Runtime.ID != "codex" || !reflect.DeepEqual(c.Match.Required, []Capability{CapWorkspaceWrite}) {
		t.Fatalf("Striker projection=%+v", c)
	}
}

func TestRuntimeSelectionVersionFailureClosed(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "opencode", caps: []Capability{CapWorkspaceWrite}}})
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "omp", caps: []Capability{CapWorkspaceWrite}}, err: errors.New("version unavailable")})
	_, err := AdmitRuntime(context.Background(), reg, "direct", "auto")
	if err == nil || errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("not a snapshot rejection: %v", err)
	}
}

// 2026-10-03：RTSEL-003/014/015，已知字面值及未知能力、大小限制與封存破壞。
func TestRuntimeSelectionPriorityAndBounds(t *testing.T) {
	for _, invalid := range []string{"codex,codex", "codex,", "unknown", " "} {
		if _, err := ParseRuntimePriority(invalid); err == nil {
			t.Fatalf("setting accepted: %q", invalid)
		}
	}
	preferred, err := ParseRuntimePriority("omp,codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := effectivePriority([]string{"z", "omp", "opencode", "codex", "antigravity"}, nil); !reflect.DeepEqual(got, []string{"opencode", "antigravity", "codex", "z", "omp"}) {
		t.Fatalf("default=%v", got)
	}
	if got := effectivePriority([]string{"omp", "opencode", "antigravity"}, preferred); !reflect.DeepEqual(got, []string{"omp", "opencode", "antigravity"}) {
		t.Fatalf("custom=%v", got)
	}
	reg := NewRegistry()
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "opencode", caps: []Capability{CapWorkspaceWrite}}})
	reg.Register(&descriptorBackend{mockBackend: mockBackend{id: "omp", caps: []Capability{CapWorkspaceWrite}}})
	c, err := AdmitRuntime(context.Background(), reg, "direct", "auto", preferred)
	if err != nil || c.Runtime.ID != "omp" {
		t.Fatalf("custom=%+v %v", c, err)
	}
	if err = c.Validate("direct", "omp"); err != nil {
		t.Fatal(err)
	}
	c.SelectionDecision.PolicyVersion = "future"
	if err = c.Validate("direct", "omp"); err == nil {
		t.Fatal("unsupported policy accepted")
	}
	big := NewRegistry()
	for i := 0; i < 33; i++ {
		big.Register(&descriptorBackend{mockBackend: mockBackend{id: fmt.Sprintf("candidate-%02d", i), caps: []Capability{CapWorkspaceWrite}}})
	}
	if _, err = AdmitRuntime(context.Background(), big, "direct", "auto"); err == nil || !strings.Contains(err.Error(), "candidate limit") {
		t.Fatalf("bound=%v", err)
	}
	req, _ := RequirementsForMode("direct")
	descriptor := RuntimeDescriptor{ID: "large", Version: strings.Repeat("v", MaxSelectionDecisionBytes), Capabilities: []Capability{CapWorkspaceWrite}, CapturedAt: time.Now()}
	if _, err = resolveSelection(RuntimeSelectionRequest{Mode: "auto"}, req, []RuntimeDescriptor{descriptor}, nil); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("size=%v", err)
	}
	descriptor.Version = "v1"
	descriptor.Capabilities = []Capability{"future"}
	if _, err = resolveSelection(RuntimeSelectionRequest{Mode: "auto"}, req, []RuntimeDescriptor{descriptor}, nil); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("unknown=%v", err)
	}
}
