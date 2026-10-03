package backend

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type inventoryFixture struct {
	mockBackend
	probe  func(context.Context) (string, error)
	probes atomic.Int32
}

func (b *inventoryFixture) Probe(ctx context.Context) (string, error) {
	b.probes.Add(1)
	return b.probe(ctx)
}
func fixture(id string, p func(context.Context) (string, error)) *inventoryFixture {
	return &inventoryFixture{mockBackend: mockBackend{id: id, caps: []Capability{CapWorkspaceWrite, CapReadOnlySandbox}}, probe: p}
}
func ready(context.Context) (string, error) { return "v1", nil }

// 2026-10-04：RTINV-001/002/003/008/009/013，API 層級共用單迴圈。
func TestInventoryPolicyStatesAndOrdering(t *testing.T) {
	stamp := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for _, reverse := range []bool{false, true} {
		rs := []RegisteredRuntime{{ID: "a", Backend: fixture("a", ready)}, {ID: "b", Backend: fixture("b", func(context.Context) (string, error) { return "", &ProbeError{Reason: ExecutableNotFound} })}, {ID: "c", Backend: fixture("c", func(context.Context) (string, error) { panic("secret-sentinel") })}, {ID: "d", Backend: fixture("d", func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() })}}
		if reverse {
			rs[0], rs[3] = rs[3], rs[0]
		}
		s, err := captureInventory(context.Background(), rs, 20*time.Millisecond, 100*time.Millisecond, stamp)
		if err != nil {
			t.Fatal(err)
		}
		states := []AvailabilityState{Available, Unavailable, Unavailable, Unknown}
		reasons := []ProbeReason{ProbeOK, ExecutableNotFound, ProbePanicked, ProbeTimedOut}
		for i, e := range s.Entries {
			if e.AvailabilityState != states[i] || e.ReasonCode != reasons[i] || e.RuntimeID != []string{"a", "b", "c", "d"}[i] {
				t.Fatalf("entry=%+v", e)
			}
		}
		raw, _ := json.Marshal(s)
		if strings.Contains(string(raw), "secret-sentinel") {
			t.Fatal("panic leaked")
		}
	}
}
func TestInventoryCancellationAndWholeBudget(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	b := fixture("a", func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	if _, err := CaptureInventory(ctx, []RegisteredRuntime{{ID: "a", Backend: b}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cooperative probe leaked")
	}
	s, err := captureInventory(context.Background(), []RegisteredRuntime{{ID: "a", Backend: fixture("a", func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() })}, {ID: "b", Backend: fixture("b", ready)}}, time.Second, 20*time.Millisecond, time.Now().UTC())
	if err != nil || s.Entries[0].ReasonCode != InventoryTimedOut || s.Entries[1].AvailabilityState != Available {
		t.Fatalf("budget=%+v %v", s, err)
	}
	if RuntimeProbeTimeout != 3*time.Second || InventoryTimeout != 5*time.Second {
		t.Fatal("production budget differs from policy")
	}
}
func TestInventoryDeterministicCompletion(t *testing.T) {
	stamp := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	var snapshots [][]byte
	for _, first := range []string{"a", "b"} {
		gate := make(chan struct{})
		arrived := make(chan struct{})
		probe := func(id string) func(context.Context) (string, error) {
			return func(context.Context) (string, error) {
				if id == first {
					close(arrived)
					<-gate
				} else {
					<-arrived
					close(gate)
				}
				return "v1", nil
			}
		}
		a, b := fixture("a", probe("a")), fixture("b", probe("b"))
		a.caps = []Capability{CapWorkspaceWrite, CapReadOnlySandbox, CapWorkspaceWrite}
		s, err := captureInventory(context.Background(), []RegisteredRuntime{{ID: "b", Backend: b}, {ID: "a", Backend: a}}, time.Second, time.Second, stamp)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(s)
		snapshots = append(snapshots, raw)
	}
	if string(snapshots[0]) != string(snapshots[1]) {
		t.Fatal("completion order affected snapshot")
	}
}
func TestInventoryBoundsAndTampering(t *testing.T) {
	reg := NewRegistry()
	a := fixture("a", ready)
	reg.Register(a)
	c, err := AdmitRuntime(context.Background(), reg, "direct", "auto")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	changes := map[string]func(*ExecutionContract){
		"missing":       func(c *ExecutionContract) { c.InventorySnapshot = nil },
		"policy":        func(c *ExecutionContract) { c.InventorySnapshot.PolicyVersion = "future" },
		"registration":  func(c *ExecutionContract) { c.InventorySnapshot.Entries[0].RegistrationState = "future" },
		"state":         func(c *ExecutionContract) { c.InventorySnapshot.Entries[0].AvailabilityState = Unknown },
		"reason":        func(c *ExecutionContract) { c.InventorySnapshot.Entries[0].ReasonCode = ProbeTimedOut },
		"diagnostic":    func(c *ExecutionContract) { c.InventorySnapshot.Entries[0].Diagnostic = strings.Repeat("x", 513) },
		"candidate":     func(c *ExecutionContract) { c.InventorySnapshot.Entries[0].Descriptor.Version = "v2" },
		"kind":          func(c *ExecutionContract) { c.InventorySnapshot.Entries[0].ProbeKind = "future" },
		"contract-size": func(c *ExecutionContract) { c.RequestedBackend = strings.Repeat("x", MaxExecutionContractBytes) },
		"duplicate": func(c *ExecutionContract) {
			c.InventorySnapshot.Entries = append(c.InventorySnapshot.Entries, c.InventorySnapshot.Entries[0])
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var copy ExecutionContract
			json.Unmarshal(raw, &copy)
			change(&copy)
			if copy.Validate("direct", "a") == nil {
				t.Fatal("tamper accepted")
			}
		})
	}
	ds := c.InventorySnapshot.AvailableDescriptors()
	ds[0].Capabilities[0] = "mutated"
	if reflect.DeepEqual(ds[0].Capabilities, c.InventorySnapshot.Entries[0].Descriptor.Capabilities) {
		t.Fatal("descriptor alias")
	}
	for _, version := range []string{"", strings.Repeat("v", 257), "v1\nsecret-sentinel", "v1 token=secret-sentinel"} {
		s, err := CaptureInventory(context.Background(), []RegisteredRuntime{{ID: "a", Backend: fixture("a", func(context.Context) (string, error) { return version, nil })}})
		if err != nil || s.Entries[0].ReasonCode != DescriptorInvalid {
			t.Fatalf("invalid version=%+v %v", s, err)
		}
	}
	a.id = "renamed"
	s, err := CaptureInventory(context.Background(), reg.RegisteredSnapshot())
	if err != nil || s.Entries[0].RuntimeID != "a" || s.Entries[0].ReasonCode != DescriptorInvalid {
		t.Fatal("authoritative identity lost")
	}
}
func TestInventoryExplicitRecoveryAndCoherence(t *testing.T) {
	a := fixture("omp", func(context.Context) (string, error) { return "", &ProbeError{Reason: ExecutableNotFound} })
	b := fixture("codex", ready)
	reg := NewRegistry()
	reg.Register(a)
	reg.Register(b)
	old, err := AdmitRuntime(context.Background(), reg, "three-face", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if a.probes.Load() != 1 || b.probes.Load() != 1 {
		t.Fatal("per-stage reprobe")
	}
	sealed, _ := json.Marshal(old)
	_, err = AdmitRuntime(context.Background(), reg, "direct", "omp")
	var rejection *InventoryAdmissionError
	if !errors.As(err, &rejection) || rejection.Snapshot.Entries[0].ReasonCode != ExecutableNotFound || b.probes.Load() != 1 {
		t.Fatalf("explicit=%v", err)
	}
	if _, err = AdmitRuntime(context.Background(), reg, "direct", "missing"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatal("unknown explicit accepted")
	}
	a.probe = ready
	newTask, err := AdmitRuntime(context.Background(), reg, "direct", "auto", []string{"omp"})
	if err != nil || newTask.Runtime.ID != "omp" {
		t.Fatal("fresh recovery failed")
	}
	after, _ := json.Marshal(old)
	if string(after) != string(sealed) {
		t.Fatal("old snapshot mutated")
	}
}

// 2026-10-04：RTINV-008，不合作的第三方函式也不能阻擋 observer；測試自行釋放 fixture。
func TestInventoryUncooperativeObserverBound(t *testing.T) {
	gate, returned := make(chan struct{}), make(chan struct{})
	b := fixture("a", func(context.Context) (string, error) { <-gate; close(returned); return "v1", nil })
	start := time.Now()
	s, err := captureInventory(context.Background(), []RegisteredRuntime{{ID: "a", Backend: b}}, 20*time.Millisecond, time.Second, time.Now().UTC())
	close(gate)
	if err != nil || s.Entries[0].ReasonCode != ProbeTimedOut || time.Since(start) > time.Second {
		t.Fatalf("unbounded observer: %+v %v", s, err)
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("fixture not released")
	}
}
func TestInventoryLegacyContracts(t *testing.T) {
	reg := NewRegistry()
	reg.Register(fixture("a", ready))
	c, err := AdmitRuntime(context.Background(), reg, "direct", "a")
	if err != nil {
		t.Fatal(err)
	}
	c.InventorySnapshot = nil
	c.ContractVersion = StageBindingContractVersion
	if err = c.Validate("direct", "a"); err != nil {
		t.Fatal("stage-binding-v1 compatibility", err)
	}
	c.StageBindingPlan = nil
	c.SelectionDecision = nil
	c.ContractVersion = CapabilityContractVersion
	if err = c.Validate("direct", "a"); err != nil {
		t.Fatal("capability-v1 compatibility", err)
	}
}
