package backend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

const CapabilityContractVersion = "capability-v1"

var ErrInvalidContract = errors.New("invalid execution contract")

type ExecutionRequirements struct {
	Role                 Role         `json:"role"`
	WorkspaceAccess      string       `json:"workspace_access"`
	RequiredCapabilities []Capability `json:"required_capabilities"`
}

type RuntimeDescriptor struct {
	ID           string       `json:"id"`
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
	CapturedAt   time.Time    `json:"captured_at"`
}

type CapabilityMatch struct {
	Compatible     bool         `json:"compatible"`
	Required       []Capability `json:"required"`
	Available      []Capability `json:"available"`
	Missing        []Capability `json:"missing"`
	BackendID      string       `json:"backend_id"`
	BackendVersion string       `json:"backend_version"`
}

type ExecutionContract struct {
	ContractVersion  string                  `json:"contract_version"`
	PolicyVersion    string                  `json:"policy_version"`
	Mode             string                  `json:"mode"`
	Requirements     []ExecutionRequirements `json:"requirements"`
	RequestedBackend string                  `json:"requested_backend"`
	Runtime          RuntimeDescriptor       `json:"runtime"`
	Match            CapabilityMatch         `json:"match"`
}

// 2026-10-03：需求由控制平面政策建立，adapter 只提供能力。
func RequirementsForMode(mode string) ([]ExecutionRequirements, error) {
	striker := ExecutionRequirements{Role: RoleStriker, WorkspaceAccess: "workspace-write", RequiredCapabilities: []Capability{CapWorkspaceWrite}}
	switch mode {
	case "", "direct":
		return []ExecutionRequirements{striker}, nil
	case "three-face":
		return []ExecutionRequirements{{Role: RoleOracle, WorkspaceAccess: "read-only", RequiredCapabilities: []Capability{CapReadOnlySandbox}}, striker, {Role: RoleCritic, WorkspaceAccess: "read-only", RequiredCapabilities: []Capability{CapReadOnlySandbox}}}, nil
	default:
		return nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidContract, mode)
	}
}

func canonicalCapabilities(caps []Capability) ([]Capability, error) {
	set := make(map[Capability]bool, len(caps))
	for _, c := range caps {
		switch c {
		case CapReadOnlySandbox, CapWorkspaceWrite, CapStructuredEvents, CapProcessCancellation:
			set[c] = true
		default:
			return nil, fmt.Errorf("%w: unknown capability %q", ErrInvalidContract, c)
		}
	}
	result := make([]Capability, 0, len(set))
	for c := range set {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func MatchCapabilities(req []ExecutionRequirements, runtime RuntimeDescriptor) (CapabilityMatch, error) {
	m := CapabilityMatch{BackendID: runtime.ID, BackendVersion: runtime.Version, Missing: []Capability{}}
	if len(req) == 0 {
		return m, fmt.Errorf("%w: empty requirements", ErrInvalidContract)
	}
	var all []Capability
	for _, r := range req {
		access := "read-only"
		cap := CapReadOnlySandbox
		switch r.Role {
		case RoleStriker:
			access = "workspace-write"
			cap = CapWorkspaceWrite
		case RoleOracle, RoleCritic:
		default:
			return m, fmt.Errorf("%w: unknown role %q", ErrInvalidContract, r.Role)
		}
		if r.WorkspaceAccess != access {
			return m, fmt.Errorf("%w: role %s workspace access %q", ErrInvalidContract, r.Role, r.WorkspaceAccess)
		}
		required, err := canonicalCapabilities(r.RequiredCapabilities)
		if err != nil {
			return m, err
		}
		found := false
		for _, c := range required {
			if c == cap {
				found = true
			}
		}
		if !found {
			return m, fmt.Errorf("%w: role %s requires %s", ErrInvalidContract, r.Role, cap)
		}
		all = append(all, required...)
	}
	var err error
	m.Required, err = canonicalCapabilities(all)
	if err != nil {
		return m, err
	}
	m.Available, err = canonicalCapabilities(runtime.Capabilities)
	if err != nil {
		return m, err
	}
	available := make(map[Capability]bool, len(m.Available))
	for _, c := range m.Available {
		available[c] = true
	}
	for _, c := range m.Required {
		if !available[c] {
			m.Missing = append(m.Missing, c)
		}
	}
	m.Compatible = len(m.Missing) == 0
	return m, nil
}

func CaptureRuntime(ctx context.Context, b Backend) (RuntimeDescriptor, error) {
	if b == nil {
		return RuntimeDescriptor{}, fmt.Errorf("%w: missing runtime", ErrInvalidContract)
	}
	caps, err := canonicalCapabilities(b.Capabilities())
	if err != nil {
		return RuntimeDescriptor{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := b.Version(probeCtx)
	if err != nil {
		return RuntimeDescriptor{}, fmt.Errorf("backend %q version unavailable: %w", b.ID(), err)
	}
	if strings.TrimSpace(version) == "" {
		return RuntimeDescriptor{}, fmt.Errorf("backend %q version unavailable: empty version", b.ID())
	}
	if err := probeCtx.Err(); err != nil {
		return RuntimeDescriptor{}, err
	}
	if b.ID() == "" {
		return RuntimeDescriptor{}, fmt.Errorf("%w: empty runtime ID", ErrInvalidContract)
	}
	return RuntimeDescriptor{ID: b.ID(), Version: version, Capabilities: caps, CapturedAt: time.Now().UTC()}, nil
}

type CapabilityAdmissionError struct {
	RequestedBackend string
	Match            CapabilityMatch
}

func (e *CapabilityAdmissionError) Error() string {
	return fmt.Sprintf("%s: requested backend %q resolved %q version %q required=%v available=%v missing=%v", ErrUnsupportedCapability, e.RequestedBackend, e.Match.BackendID, e.Match.BackendVersion, e.Match.Required, e.Match.Available, e.Match.Missing)
}
func (e *CapabilityAdmissionError) Unwrap() error { return ErrUnsupportedCapability }

func AdmitRuntime(ctx context.Context, reg Registry, mode, requested string) (*ExecutionContract, error) {
	req, err := RequirementsForMode(mode)
	if err != nil {
		return nil, err
	}
	id := requested
	if id == "" {
		id = "opencode"
	}
	if mode == "" {
		mode = "direct"
	}
	b, err := reg.Get(id)
	if err != nil {
		return nil, err
	}
	// 2026-10-03：先以安全能力拒絕，不因版本探測失敗掩蓋唯讀能力不足。
	match, err := MatchCapabilities(req, RuntimeDescriptor{ID: b.ID(), Capabilities: b.Capabilities()})
	if err != nil {
		return nil, err
	}
	if !match.Compatible {
		return nil, &CapabilityAdmissionError{RequestedBackend: requested, Match: match}
	}
	runtime, err := CaptureRuntime(ctx, b)
	if err != nil {
		return nil, err
	}
	match, err = MatchCapabilities(req, runtime)
	if err != nil {
		return nil, err
	}
	if !match.Compatible {
		return nil, &CapabilityAdmissionError{RequestedBackend: requested, Match: match}
	}
	return &ExecutionContract{ContractVersion: CapabilityContractVersion, PolicyVersion: CapabilityContractVersion, Mode: mode, Requirements: req, RequestedBackend: requested, Runtime: runtime, Match: match}, nil
}

// 2026-10-03：執行時核對封存結構，不以目前 registry 重算接受時判定。
func (c *ExecutionContract) Validate(mode, id string) error {
	if c == nil || c.ContractVersion != CapabilityContractVersion || c.PolicyVersion != CapabilityContractVersion {
		return fmt.Errorf("%w: unsupported contract/policy version", ErrInvalidContract)
	}
	if c.Mode != mode || c.Runtime.ID != id || c.Runtime.Version == "" || c.Runtime.CapturedAt.IsZero() {
		return fmt.Errorf("%w: sealed identity mismatch", ErrInvalidContract)
	}
	expected, err := RequirementsForMode(c.Mode)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, c.Requirements) {
		return fmt.Errorf("%w: sealed requirements mismatch", ErrInvalidContract)
	}
	requested := c.RequestedBackend
	if requested == "" {
		requested = "opencode"
	}
	if requested != id {
		return fmt.Errorf("%w: requested runtime mismatch", ErrInvalidContract)
	}
	m, err := MatchCapabilities(c.Requirements, c.Runtime)
	if err != nil {
		return err
	}
	if !m.Compatible || !reflect.DeepEqual(m, c.Match) {
		return fmt.Errorf("%w: sealed match mismatch", ErrInvalidContract)
	}
	return nil
}
