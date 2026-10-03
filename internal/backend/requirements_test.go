package backend

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// 2026-10-03：RTCAP-002／004／010／013，純 contract 層使用核准政策字面值。
func TestCapabilityRequirementsPolicy(t *testing.T) {
	want := []ExecutionRequirements{{Role: RoleOracle, WorkspaceAccess: "read-only", RequiredCapabilities: []Capability{CapReadOnlySandbox}}, {Role: RoleStriker, WorkspaceAccess: "workspace-write", RequiredCapabilities: []Capability{CapWorkspaceWrite}}, {Role: RoleCritic, WorkspaceAccess: "read-only", RequiredCapabilities: []Capability{CapReadOnlySandbox}}}
	for i := 0; i < 2; i++ {
		got, err := RequirementsForMode("three-face")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("requirements=%+v err=%v", got, err)
		}
	}
	direct, err := RequirementsForMode("direct")
	if err != nil || !reflect.DeepEqual(direct, want[1:2]) {
		t.Fatalf("direct=%+v err=%v", direct, err)
	}
	if _, err := RequirementsForMode("auto"); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("unknown mode: %v", err)
	}
}

func TestCapabilityMatcherSets(t *testing.T) {
	req := []ExecutionRequirements{{Role: RoleStriker, WorkspaceAccess: "workspace-write", RequiredCapabilities: []Capability{CapWorkspaceWrite, CapWorkspaceWrite}}}
	for _, caps := range [][]Capability{{CapStructuredEvents, CapWorkspaceWrite}, {CapWorkspaceWrite, CapStructuredEvents, CapWorkspaceWrite}} {
		m, err := MatchCapabilities(req, RuntimeDescriptor{ID: "a", Version: "V1", Capabilities: caps})
		if err != nil || !m.Compatible || !reflect.DeepEqual(m.Required, []Capability{CapWorkspaceWrite}) || !reflect.DeepEqual(m.Available, []Capability{CapStructuredEvents, CapWorkspaceWrite}) || len(m.Missing) != 0 {
			t.Fatalf("match=%+v err=%v", m, err)
		}
		caps[0] = Capability("mutated")
		if m.Available[0] != CapStructuredEvents {
			t.Fatal("slice aliases runtime")
		}
	}
	m, err := MatchCapabilities(req, RuntimeDescriptor{ID: "a", Version: "V1", Capabilities: []Capability{CapReadOnlySandbox}})
	if err != nil || m.Compatible || !reflect.DeepEqual(m.Missing, []Capability{CapWorkspaceWrite}) || m.BackendID != "a" || m.BackendVersion != "V1" {
		t.Fatalf("missing=%+v err=%v", m, err)
	}
}

func TestCapabilityMatcherUnknownFailsClosed(t *testing.T) {
	valid := ExecutionRequirements{Role: RoleStriker, WorkspaceAccess: "workspace-write", RequiredCapabilities: []Capability{CapWorkspaceWrite}}
	for _, req := range []ExecutionRequirements{{Role: "unknown", WorkspaceAccess: "workspace-write", RequiredCapabilities: []Capability{CapWorkspaceWrite}}, {Role: RoleStriker, WorkspaceAccess: "unknown", RequiredCapabilities: []Capability{CapWorkspaceWrite}}, {Role: RoleStriker, WorkspaceAccess: "workspace-write", RequiredCapabilities: []Capability{"unknown"}}, {Role: RoleStriker, WorkspaceAccess: "read-only", RequiredCapabilities: []Capability{CapReadOnlySandbox}}, {Role: RoleStriker, WorkspaceAccess: "workspace-write"}} {
		if _, err := MatchCapabilities([]ExecutionRequirements{req}, RuntimeDescriptor{ID: "a", Version: "v", Capabilities: []Capability{CapWorkspaceWrite}}); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("invalid %+v accepted: %v", req, err)
		}
	}
	if _, err := MatchCapabilities([]ExecutionRequirements{valid}, RuntimeDescriptor{ID: "a", Version: "v", Capabilities: []Capability{"unknown"}}); !errors.Is(err, ErrInvalidContract) {
		t.Fatal("unknown runtime capability ignored")
	}
	if _, err := MatchCapabilities(nil, RuntimeDescriptor{ID: "a", Version: "v"}); !errors.Is(err, ErrInvalidContract) {
		t.Fatal("empty requirements accepted")
	}
}

type descriptorBackend struct {
	mockBackend
	err error
}

func (b *descriptorBackend) Version(context.Context) (string, error) {
	if b.err != nil {
		return "", b.err
	}
	return b.mockBackend.Version(context.Background())
}

// 2026-10-03：RTCAP-014，不把 unavailable 版本當成功。
func TestCapabilityDescriptorVersion(t *testing.T) {
	b := &descriptorBackend{mockBackend: mockBackend{id: "fixture", caps: []Capability{CapWorkspaceWrite}}, err: errors.New("unavailable")}
	if _, err := CaptureRuntime(context.Background(), b); err == nil {
		t.Fatal("version unavailable accepted")
	}
	b.err = nil
	d, err := CaptureRuntime(context.Background(), b)
	if err != nil || d.ID != "fixture" || d.Version == "" || d.CapturedAt.IsZero() {
		t.Fatalf("descriptor=%+v err=%v", d, err)
	}
}
