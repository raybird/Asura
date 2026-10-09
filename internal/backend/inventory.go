package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const InventoryContractVersion = "runtime-inventory-v1"
const InventoryPolicyVersion = "inventory-v1"
const RuntimeProbeTimeout = 3 * time.Second
const InventoryTimeout = 5 * time.Second
const MaxInventoryBytes = 64 * 1024
const MaxExecutionContractBytes = 256 * 1024
const MaxRuntimeIDBytes = 64
const MaxRuntimeVersionBytes = 256
const MaxProbeDiagnosticBytes = 512

type RegistrationState string

const Registered RegistrationState = "registered"

type AvailabilityState string

const (
	Available   AvailabilityState = "available"
	Unavailable AvailabilityState = "unavailable"
	Unknown     AvailabilityState = "unknown"
)

type ProbeKind string

const (
	DescriptorProbe ProbeKind = "descriptor"
	AdapterProbe    ProbeKind = "adapter"
)

type ProbeReason string

const (
	ProbeOK              ProbeReason = "none"
	ProbeNotStarted      ProbeReason = "not_probed"
	ExecutableNotFound   ProbeReason = "executable_not_found"
	DependencyNotFound   ProbeReason = "dependency_not_found"
	ConfigurationInvalid ProbeReason = "configuration_invalid"
	VersionFailed        ProbeReason = "version_failed"
	DescriptorInvalid    ProbeReason = "descriptor_invalid"
	ProbeFailed          ProbeReason = "probe_error"
	ProbePanicked        ProbeReason = "probe_panic"
	ProbeTimedOut        ProbeReason = "probe_timeout"
	ProbeCancelled       ProbeReason = "probe_cancelled"
	InventoryTimedOut    ProbeReason = "inventory_timeout"
)

// 2026-10-04：不將任意 error、stderr 或 panic 值送入 audit。
type ProbeError struct{ Reason ProbeReason }

func (e *ProbeError) Error() string { return string(e.Reason) }

type RuntimeProber interface {
	Probe(context.Context) (string, error)
}
type RegisteredRuntime struct {
	ID      string
	Backend Backend
}
type RuntimeInventoryEntry struct {
	RuntimeID         string             `json:"runtime_id"`
	RegistrationState RegistrationState  `json:"registration_state"`
	AvailabilityState AvailabilityState  `json:"availability_state"`
	Descriptor        *RuntimeDescriptor `json:"descriptor,omitempty"`
	ObservedAt        time.Time          `json:"observed_at"`
	ProbeKind         ProbeKind          `json:"probe_kind"`
	ReasonCode        ProbeReason        `json:"reason_code"`
	Diagnostic        string             `json:"diagnostic,omitempty"`
}
type InventorySnapshot struct {
	ContractVersion string                  `json:"contract_version"`
	PolicyVersion   string                  `json:"policy_version"`
	ObservedAt      time.Time               `json:"observed_at"`
	Entries         []RuntimeInventoryEntry `json:"entries"`
}

func diagnostic(reason ProbeReason) string {
	if reason == ProbeOK {
		return ""
	}
	return "Runtime probe: " + string(reason)
}
func failureEntry(e RuntimeInventoryEntry, reason ProbeReason) RuntimeInventoryEntry {
	e.Descriptor = nil
	e.AvailabilityState = Unavailable
	switch reason {
	case ProbeNotStarted, ProbeTimedOut, ProbeCancelled, InventoryTimedOut:
		e.AvailabilityState = Unknown
	}
	e.ReasonCode = reason
	e.Diagnostic = diagnostic(reason)
	return e
}
func validRuntimeID(id string) bool {
	if id == "" || len(id) > MaxRuntimeIDBytes || !utf8.ValidString(id) {
		return false
	}
	for _, c := range id {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func validVersion(version string) bool {
	if version == "" || len(version) > MaxRuntimeVersionBytes {
		return false
	}
	// 2026-10-04：只允許版本 token；Codex CLI 的固定前綴可保留。
	token := strings.TrimPrefix(version, "codex-cli ")
	if token == "" {
		return false
	}
	for _, c := range token {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("._+-/", c)) {
			return false
		}
	}
	return true
}

func probeRuntime(ctx context.Context, r RegisteredRuntime, observed time.Time) (entry RuntimeInventoryEntry) {
	entry = RuntimeInventoryEntry{RuntimeID: r.ID, RegistrationState: Registered, AvailabilityState: Unknown, ObservedAt: observed, ProbeKind: DescriptorProbe, ReasonCode: ProbeNotStarted}
	defer func() {
		if recover() != nil {
			entry = failureEntry(entry, ProbePanicked)
		}
	}()
	if r.Backend == nil || r.Backend.ID() != r.ID {
		return failureEntry(entry, DescriptorInvalid)
	}
	var version string
	var err error
	if p, ok := r.Backend.(RuntimeProber); ok {
		entry.ProbeKind = AdapterProbe
		version, err = p.Probe(ctx)
	} else {
		version, err = r.Backend.Version(ctx)
	}
	if ctx.Err() != nil {
		reason := ProbeTimedOut
		if errors.Is(ctx.Err(), context.Canceled) {
			reason = ProbeCancelled
		}
		return failureEntry(entry, reason)
	}
	if err != nil {
		reason := ProbeFailed
		var pe *ProbeError
		if errors.As(err, &pe) {
			reason = pe.Reason
		}
		if !validUnavailableReason(reason) {
			reason = ProbeFailed
		}
		return failureEntry(entry, reason)
	}
	caps, err := canonicalCapabilities(r.Backend.Capabilities())
	if err != nil || !validVersion(version) || r.Backend.ID() != r.ID {
		return failureEntry(entry, DescriptorInvalid)
	}
	entry.Descriptor = &RuntimeDescriptor{ID: r.ID, Version: version, Capabilities: caps, CapturedAt: observed}
	entry.AvailabilityState = Available
	entry.ReasonCode = ProbeOK
	return entry
}
func CaptureInventory(ctx context.Context, runtimes []RegisteredRuntime) (*InventorySnapshot, error) {
	return captureInventory(ctx, runtimes, RuntimeProbeTimeout, InventoryTimeout, time.Now().UTC())
}

// 2026-10-04：可測試的固定 clock/budget；產品入口只使用政策常數。
func captureInventory(ctx context.Context, runtimes []RegisteredRuntime, perRuntime, budget time.Duration, observed time.Time) (*InventorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(runtimes) > MaxRuntimeCandidates {
		return nil, fmt.Errorf("%w: inventory candidate limit", ErrInvalidContract)
	}
	runtimes = append([]RegisteredRuntime(nil), runtimes...)
	sort.Slice(runtimes, func(i, j int) bool { return runtimes[i].ID < runtimes[j].ID })
	s := &InventorySnapshot{ContractVersion: InventoryContractVersion, PolicyVersion: InventoryPolicyVersion, ObservedAt: observed, Entries: make([]RuntimeInventoryEntry, len(runtimes))}
	for i, r := range runtimes {
		if !validRuntimeID(r.ID) || i > 0 && r.ID == runtimes[i-1].ID {
			return nil, fmt.Errorf("%w: inventory identity", ErrInvalidContract)
		}
		s.Entries[i] = failureEntry(RuntimeInventoryEntry{RuntimeID: r.ID, RegistrationState: Registered, ObservedAt: observed, ProbeKind: DescriptorProbe}, ProbeNotStarted)
	}
	whole, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	type result struct {
		index int
		entry RuntimeInventoryEntry
	}
	results := make(chan result, len(runtimes))
	for i, r := range runtimes {
		go func(i int, r RegisteredRuntime) {
			pc, stop := context.WithTimeout(whole, perRuntime)
			defer stop()
			done := make(chan RuntimeInventoryEntry, 1)
			go func() { done <- probeRuntime(pc, r, observed) }()
			select {
			case e := <-done:
				if whole.Err() != nil && ctx.Err() == nil {
					e = failureEntry(e, InventoryTimedOut)
				}
				results <- result{i, e}
			case <-pc.Done():
				reason := ProbeTimedOut
				if ctx.Err() != nil {
					reason = ProbeCancelled
				} else if whole.Err() != nil {
					reason = InventoryTimedOut
				}
				results <- result{i, failureEntry(RuntimeInventoryEntry{RuntimeID: r.ID, RegistrationState: Registered, ObservedAt: observed, ProbeKind: DescriptorProbe}, reason)}
			}
		}(i, r)
	}
	completed := make([]bool, len(runtimes))
	for remaining := len(runtimes); remaining > 0; remaining-- {
		select {
		case r := <-results:
			s.Entries[r.index] = r.entry
			completed[r.index] = true
		case <-whole.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			for i := range s.Entries {
				if !completed[i] {
					s.Entries[i] = failureEntry(s.Entries[i], InventoryTimedOut)
				}
			}
			return s, s.Validate()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s, s.Validate()
}
func validUnavailableReason(r ProbeReason) bool {
	switch r {
	case ExecutableNotFound, DependencyNotFound, ConfigurationInvalid, VersionFailed, DescriptorInvalid, ProbeFailed, ProbePanicked:
		return true
	}
	return false
}
func (s *InventorySnapshot) Validate() error {
	invalid := func() error { return fmt.Errorf("%w: invalid inventory snapshot", ErrInvalidContract) }
	if s == nil || s.ContractVersion != InventoryContractVersion || s.PolicyVersion != InventoryPolicyVersion || s.ObservedAt.IsZero() || len(s.Entries) > MaxRuntimeCandidates {
		return invalid()
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > MaxInventoryBytes {
		return invalid()
	}
	for i, e := range s.Entries {
		if !validRuntimeID(e.RuntimeID) || i > 0 && s.Entries[i-1].RuntimeID >= e.RuntimeID || e.RegistrationState != Registered || !e.ObservedAt.Equal(s.ObservedAt) || e.ProbeKind != DescriptorProbe && e.ProbeKind != AdapterProbe || len(e.Diagnostic) > MaxProbeDiagnosticBytes || e.Diagnostic != diagnostic(e.ReasonCode) {
			return invalid()
		}
		switch e.AvailabilityState {
		case Available:
			d := e.Descriptor
			if e.ReasonCode != ProbeOK || d == nil || d.ID != e.RuntimeID || !validVersion(d.Version) || !d.CapturedAt.Equal(s.ObservedAt) {
				return invalid()
			}
			caps, err := canonicalCapabilities(d.Capabilities)
			if err != nil || !reflect.DeepEqual(caps, d.Capabilities) {
				return invalid()
			}
		case Unavailable:
			if e.Descriptor != nil || !validUnavailableReason(e.ReasonCode) {
				return invalid()
			}
		case Unknown:
			if e.Descriptor != nil {
				return invalid()
			}
			switch e.ReasonCode {
			case ProbeNotStarted, ProbeTimedOut, ProbeCancelled, InventoryTimedOut:
			default:
				return invalid()
			}
		default:
			return invalid()
		}
	}
	return nil
}
func (s *InventorySnapshot) AvailableDescriptors() []RuntimeDescriptor {
	ds := []RuntimeDescriptor{}
	for _, e := range s.Entries {
		if e.AvailabilityState == Available && e.Descriptor != nil {
			d := *e.Descriptor
			d.Capabilities = append([]Capability{}, d.Capabilities...)
			ds = append(ds, d)
		}
	}
	return ds
}

type InventoryAdmissionError struct {
	Snapshot *InventorySnapshot
	Cause    error
}

func (e *InventoryAdmissionError) Error() string {
	reasons := []string{}
	for _, entry := range e.Snapshot.Entries {
		if entry.AvailabilityState != Available {
			reasons = append(reasons, entry.RuntimeID+":"+string(entry.ReasonCode))
		}
	}
	return fmt.Sprintf("runtime admission rejected: %v; availability=%v", e.Cause, reasons)
}
func (e *InventoryAdmissionError) Unwrap() error { return e.Cause }
func (c *ExecutionContract) validateInventory(mode, id string) error {
	if err := c.InventorySnapshot.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxExecutionContractBytes {
		return fmt.Errorf("%w: execution contract size limit", ErrInvalidContract)
	}
	if err := c.validateStagePlan(mode, id); err != nil {
		return err
	}
	ds := c.InventorySnapshot.AvailableDescriptors()
	if c.RequestedBackend != "auto" {
		requested := c.RequestedBackend
		if requested == "" {
			requested = "opencode"
		}
		if len(c.InventorySnapshot.Entries) != 1 || c.InventorySnapshot.Entries[0].RuntimeID != requested {
			return fmt.Errorf("%w: explicit inventory mismatch", ErrInvalidContract)
		}
	}
	for _, binding := range c.StageBindingPlan.Bindings {
		candidates := []RuntimeDescriptor{}
		for _, candidate := range binding.SelectionDecision.Candidates {
			candidates = append(candidates, candidate.Runtime)
		}
		if !reflect.DeepEqual(ds, candidates) {
			return fmt.Errorf("%w: inventory candidates mismatch", ErrInvalidContract)
		}
	}
	return nil
}
func sealInventory(plan *StageBindingPlan, requested string, s *InventorySnapshot) (*ExecutionContract, error) {
	c, err := contractFromPlan(plan, requested)
	if err != nil {
		return nil, err
	}
	c.ContractVersion = InventoryContractVersion
	c.InventorySnapshot = s
	if err = c.Validate(c.Mode, c.Runtime.ID); err != nil {
		return nil, err
	}
	return c, nil
}

// ExecutableReporter 回報 probe 實際使用的 executable 路徑，僅供 doctor 顯示；
// 不寫入 RuntimeInventoryEntry 或 ExecutionContract。空字串代表無法提供。
type ExecutableReporter interface {
	ProbeExecutable() string
}
