package audit

import (
	"asura/internal/backend"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Version = "execution-audit-v1"
const MaxEvents = 32
const MaxEventBytes = 2 * 1024
const MaxHeaderBytes = 64 * 1024
const MaxRecordBytes = 128 * 1024
const MaxResponseBytes = 160 * 1024

var ErrInvalid = errors.New("audit_invalid")
var ErrPersistence = errors.New("audit_persistence_failed")
var ErrConflict = errors.New("audit_event_conflict")

// 2026-10-04：只接收有界值與 enum；不接收 prompt、output 或 error 字串。
type Observer interface {
	Record(context.Context, Event) error
}
type Runtime struct {
	ObservedAt   time.Time                 `json:"observed_at"`
	ID           string                    `json:"id"`
	Version      string                    `json:"version,omitempty"`
	Capabilities []backend.Capability      `json:"capabilities,omitempty"`
	Availability backend.AvailabilityState `json:"availability"`
	Reason       backend.ProbeReason       `json:"reason"`
}
type Candidate struct {
	RuntimeID string               `json:"runtime_id"`
	Missing   []backend.Capability `json:"missing"`
}
type Binding struct {
	Stage         backend.Role                  `json:"stage"`
	Requirements  backend.ExecutionRequirements `json:"requirements"`
	Candidates    []Candidate                   `json:"candidates"`
	Priority      []string                      `json:"priority"`
	Selected      string                        `json:"selected"`
	Policy        string                        `json:"policy"`
	PolicyVersion string                        `json:"policy_version"`
}
type Admission struct {
	InventoryVersion string     `json:"inventory_version,omitempty"`
	InventoryPolicy  string     `json:"inventory_policy,omitempty"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	State            string     `json:"state"`
	Reason           string     `json:"reason"`
	Runtimes         []Runtime  `json:"runtimes"`
	Bindings         []Binding  `json:"bindings"`
}
type Header struct {
	Schema           string    `json:"schema"`
	TaskID           string    `json:"task_id"`
	RequestDigest    string    `json:"request_digest,omitempty"`
	Mode             string    `json:"mode"`
	RequestedBackend string    `json:"requested_backend"`
	ContractVersion  string    `json:"contract_version"`
	ContractDigest   string    `json:"contract_digest"`
	CreatedAt        time.Time `json:"created_at"`
	Admission        Admission `json:"admission"`
}
type Event struct {
	Seq            int          `json:"seq"`
	Key            string       `json:"key"`
	Kind           string       `json:"kind"`
	Stage          backend.Role `json:"stage,omitempty"`
	Scope          string       `json:"scope,omitempty"`
	State          string       `json:"state,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	Layer          string       `json:"layer,omitempty"`
	RuntimeID      string       `json:"runtime_id,omitempty"`
	RuntimeVersion string       `json:"runtime_version,omitempty"`
	At             time.Time    `json:"at"`
	PreviousDigest string       `json:"previous_digest"`
	Digest         string       `json:"digest"`
}
type Guard struct {
	Scope           string    `json:"scope"`
	State           string    `json:"state"`
	Reason          string    `json:"reason"`
	ObservedID      string    `json:"observed_id,omitempty"`
	ObservedVersion string    `json:"observed_version,omitempty"`
	At              time.Time `json:"at,omitempty"`
}
type Stage struct {
	Name            backend.Role `json:"name"`
	SealedRuntime   string       `json:"sealed_runtime"`
	SealedVersion   string       `json:"sealed_version"`
	Status          string       `json:"status"`
	Reachability    string       `json:"reachability"`
	Guard           Guard        `json:"guard"`
	Precheck        Guard        `json:"precheck"`
	ObservedRuntime string       `json:"observed_runtime,omitempty"`
	ObservedVersion string       `json:"observed_version,omitempty"`
	ActualRuntime   string       `json:"actual_runtime,omitempty"`
	ActualVersion   string       `json:"actual_version,omitempty"`
	RunIntent       bool         `json:"run_intent"`
	RunReturned     bool         `json:"run_returned"`
	RunOutcome      string       `json:"run_outcome,omitempty"`
	StartedAt       *time.Time   `json:"started_at,omitempty"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
	Reason          string       `json:"reason"`
	FailureLayer    string       `json:"failure_layer,omitempty"`
}
type Terminal struct {
	CancelPhase        string       `json:"cancel_phase,omitempty"`
	State              string       `json:"state"`
	Reason             string       `json:"reason"`
	FailureLayer       string       `json:"failure_layer,omitempty"`
	FailingStage       backend.Role `json:"failing_stage,omitempty"`
	CompletedStages    int          `json:"completed_stages"`
	RunIntents         int          `json:"run_intents"`
	ConfirmedReturns   int          `json:"confirmed_returns"`
	InvocationEvidence string       `json:"invocation_evidence"`
	ModelCalls         string       `json:"model_calls"`
	At                 time.Time    `json:"at"`
}
type Record struct {
	Header       Header    `json:"header"`
	Events       []Event   `json:"events"`
	Stages       []Stage   `json:"stages"`
	Terminal     *Terminal `json:"terminal,omitempty"`
	TaskState    string    `json:"task_state"`
	Completeness string    `json:"completeness"`
	Integrity    string    `json:"integrity"`
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func Correlation(id string) string {
	if id == "" {
		return ""
	}
	s := sha256.Sum256([]byte(id))
	return hex.EncodeToString(s[:])
}
func safeID(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func validIdentity(id string) bool {
	if len(id) == 0 || len(id) > 256 || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validVersion(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > backend.MaxRuntimeVersionBytes {
		return false
	}
	token := strings.TrimPrefix(s, "codex-cli ")
	if token == "" {
		return false
	}
	for _, r := range token {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-/", r)) {
			return false
		}
	}
	return true
}
func NewHeader(id, request string, at time.Time, c *backend.ExecutionContract) (Header, error) {
	h := Header{Schema: Version, TaskID: id, RequestDigest: Correlation(request), CreatedAt: at.UTC()}
	if c == nil || !validIdentity(id) {
		return h, ErrInvalid
	}
	if err := c.Validate(c.Mode, c.Runtime.ID); err != nil {
		return h, ErrInvalid
	}
	h.Mode = c.Mode
	h.RequestedBackend = c.RequestedBackend
	if h.RequestedBackend == "" {
		h.RequestedBackend = "opencode"
	}
	h.ContractVersion = c.ContractVersion
	h.ContractDigest = Digest(c)
	h.Admission = Admission{State: "accepted", Reason: "none", Runtimes: []Runtime{}, Bindings: []Binding{}}
	if c.InventorySnapshot != nil {
		h.Admission.InventoryVersion = c.InventorySnapshot.ContractVersion
		h.Admission.InventoryPolicy = c.InventorySnapshot.PolicyVersion
		at := c.InventorySnapshot.ObservedAt.UTC()
		h.Admission.ObservedAt = &at
		for _, e := range c.InventorySnapshot.Entries {
			r := Runtime{ObservedAt: e.ObservedAt.UTC(), ID: e.RuntimeID, Availability: e.AvailabilityState, Reason: e.ReasonCode}
			if e.Descriptor != nil {
				r.Version = e.Descriptor.Version
				r.Capabilities = append([]backend.Capability{}, e.Descriptor.Capabilities...)
			}
			h.Admission.Runtimes = append(h.Admission.Runtimes, r)
		}
	}
	if c.StageBindingPlan != nil {
		for _, b := range c.StageBindingPlan.Bindings {
			d := b.SelectionDecision
			p := Binding{Stage: b.Stage, Requirements: b.Requirements, Candidates: []Candidate{}, Priority: append([]string{}, d.Priority...), Selected: d.Selected.ID, Policy: d.PolicyID, PolicyVersion: d.PolicyVersion}
			for _, x := range d.Candidates {
				p.Candidates = append(p.Candidates, Candidate{RuntimeID: x.Runtime.ID, Missing: append([]backend.Capability{}, x.Match.Missing...)})
				if c.InventorySnapshot == nil && len(h.Admission.Bindings) == 0 {
					h.Admission.Runtimes = append(h.Admission.Runtimes, Runtime{ObservedAt: x.Runtime.CapturedAt.UTC(), ID: x.Runtime.ID, Version: x.Runtime.Version, Capabilities: x.Runtime.Capabilities, Availability: backend.Available, Reason: backend.ProbeOK})
				}
			}
			h.Admission.Bindings = append(h.Admission.Bindings, p)
		}
	} else if c.SelectionDecision != nil {
		// 2026-10-04：capability-v1 的 selection 是整個 Task 的一次決策，保留原候選 match。
		d := c.SelectionDecision
		for _, x := range d.Candidates {
			h.Admission.Runtimes = append(h.Admission.Runtimes, Runtime{ObservedAt: x.Runtime.CapturedAt.UTC(), ID: x.Runtime.ID, Version: x.Runtime.Version, Capabilities: x.Runtime.Capabilities, Availability: backend.Available, Reason: backend.ProbeOK})
		}
		for _, req := range c.Requirements {
			b := Binding{Stage: req.Role, Requirements: req, Candidates: []Candidate{}, Priority: append([]string{}, d.Priority...), Selected: d.Selected.ID, Policy: d.PolicyID, PolicyVersion: d.PolicyVersion}
			for _, x := range d.Candidates {
				b.Candidates = append(b.Candidates, Candidate{RuntimeID: x.Runtime.ID, Missing: append([]backend.Capability{}, x.Match.Missing...)})
			}
			h.Admission.Bindings = append(h.Admission.Bindings, b)
		}
	} else {
		h.Admission.Runtimes = append(h.Admission.Runtimes, Runtime{ObservedAt: c.Runtime.CapturedAt.UTC(), ID: c.Runtime.ID, Version: c.Runtime.Version, Capabilities: c.Runtime.Capabilities, Availability: backend.Available, Reason: backend.ProbeOK})
		for _, req := range c.Requirements {
			h.Admission.Bindings = append(h.Admission.Bindings, Binding{Stage: req.Role, Requirements: req, Candidates: []Candidate{{RuntimeID: c.Runtime.ID, Missing: []backend.Capability{}}}, Priority: []string{c.Runtime.ID}, Selected: c.Runtime.ID, Policy: "explicit", PolicyVersion: "v1"})
		}
	}
	if err := h.Validate(); err != nil {
		return h, err
	}
	raw, _ := json.Marshal(h)
	var independent Header
	if json.Unmarshal(raw, &independent) != nil {
		return h, ErrInvalid
	}
	return independent, nil
}
func (h Header) Validate() error {
	raw, _ := json.Marshal(h)
	if len(raw) > MaxHeaderBytes || h.Schema != Version || !validIdentity(h.TaskID) || h.CreatedAt.IsZero() || len(h.ContractDigest) != 64 || h.RequestDigest != "" && len(h.RequestDigest) != 64 || !safeID(h.RequestedBackend, 64) || h.Admission.State != "accepted" || h.Admission.Reason != "none" {
		return ErrInvalid
	}
	if h.ContractVersion != backend.CapabilityContractVersion && h.ContractVersion != backend.StageBindingContractVersion && h.ContractVersion != backend.InventoryContractVersion {
		return ErrInvalid
	}
	if h.ContractVersion == backend.InventoryContractVersion {
		if h.Admission.InventoryVersion != backend.InventoryContractVersion || h.Admission.InventoryPolicy != backend.InventoryPolicyVersion || h.Admission.ObservedAt == nil || h.Admission.ObservedAt.IsZero() {
			return ErrInvalid
		}
	} else if h.Admission.InventoryVersion != "" || h.Admission.InventoryPolicy != "" || h.Admission.ObservedAt != nil {
		return ErrInvalid
	}
	req, err := backend.RequirementsForMode(h.Mode)
	if err != nil || len(req) != len(h.Admission.Bindings) || len(h.Admission.Runtimes) == 0 || len(h.Admission.Runtimes) > 32 {
		return ErrInvalid
	}
	runtimes := map[string]Runtime{}
	for _, r := range h.Admission.Runtimes {
		if r.ObservedAt.IsZero() || !safeID(r.ID, 64) || !validVersion(r.Version) || len(r.Capabilities) > 4 {
			return ErrInvalid
		}
		if _, ok := runtimes[r.ID]; ok {
			return ErrInvalid
		}
		switch r.Availability {
		case backend.Available:
			if r.Version == "" || r.Reason != backend.ProbeOK {
				return ErrInvalid
			}
		case backend.Unavailable, backend.Unknown:
			if r.Version != "" || len(r.Capabilities) != 0 || !probeReason(r.Reason) {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		for _, cap := range r.Capabilities {
			if !capability(cap) {
				return ErrInvalid
			}
		}
		runtimes[r.ID] = r
	}
	for i, b := range h.Admission.Bindings {
		if h.RequestedBackend == "auto" {
			if b.Policy != backend.SelectionPolicyID {
				return ErrInvalid
			}
		} else if b.Policy != "explicit" || b.Selected != h.RequestedBackend || len(b.Candidates) != 1 {
			return ErrInvalid
		}
		if b.Stage != req[i].Role || !reflect.DeepEqual(b.Requirements, req[i]) || b.PolicyVersion != "v1" || b.Policy != "explicit" && b.Policy != backend.SelectionPolicyID || len(b.Candidates) == 0 || len(b.Candidates) > 32 || len(b.Priority) != len(b.Candidates) {
			return ErrInvalid
		}
		eligible := map[string]bool{}
		seen := map[string]bool{}
		for _, c := range b.Candidates {
			r, ok := runtimes[c.RuntimeID]
			if !ok || r.Availability != backend.Available || seen[c.RuntimeID] {
				return ErrInvalid
			}
			seen[c.RuntimeID] = true
			requirements := []backend.ExecutionRequirements{b.Requirements}
			if h.ContractVersion == backend.CapabilityContractVersion {
				requirements = req
			}
			m, e := backend.MatchCapabilities(requirements, backend.RuntimeDescriptor{ID: r.ID, Version: r.Version, Capabilities: r.Capabilities})
			if e != nil || !reflect.DeepEqual(m.Missing, c.Missing) {
				return ErrInvalid
			}
			eligible[c.RuntimeID] = m.Compatible
		}
		picked := ""
		ordered := map[string]bool{}
		for _, id := range b.Priority {
			if !seen[id] || ordered[id] {
				return ErrInvalid
			}
			ordered[id] = true
			if picked == "" && eligible[id] {
				picked = id
			}
		}
		if picked == "" || b.Selected != picked {
			return ErrInvalid
		}
	}
	return nil
}
func capability(c backend.Capability) bool {
	switch c {
	case backend.CapReadOnlySandbox, backend.CapWorkspaceWrite, backend.CapStructuredEvents, backend.CapProcessCancellation:
		return true
	}
	return false
}
func probeReason(r backend.ProbeReason) bool {
	switch r {
	case backend.ProbeNotStarted, backend.ExecutableNotFound, backend.DependencyNotFound, backend.ConfigurationInvalid, backend.VersionFailed, backend.DescriptorInvalid, backend.ProbeFailed, backend.ProbePanicked, backend.ProbeTimedOut, backend.ProbeCancelled, backend.InventoryTimedOut:
		return true
	}
	return false
}
func EventKey(e Event) string {
	if e.Stage != "" {
		return string(e.Stage) + "/" + e.Scope + "/" + e.Kind
	}
	return e.Kind
}
func (e Event) Validate() error {
	raw, _ := json.Marshal(e)
	if len(raw) > MaxEventBytes || e.Seq < 1 || e.Seq > MaxEvents || e.Key != EventKey(e) || e.At.IsZero() || len(e.PreviousDigest) != 64 || len(e.Digest) != 64 {
		return ErrInvalid
	}
	if e.Stage != "" && e.Stage != backend.RoleOracle && e.Stage != backend.RoleStriker && e.Stage != backend.RoleCritic {
		return ErrInvalid
	}
	if e.RuntimeID != "" && !safeID(e.RuntimeID, 64) || !validVersion(e.RuntimeVersion) {
		return ErrInvalid
	}
	switch e.Kind {
	case "task_started":
		if e.Stage != "" || e.State != "running" {
			return ErrInvalid
		}
	case "cancel_requested":
		if e.Stage != "" || e.State != "cancelling" {
			return ErrInvalid
		}
	case "guard_started":
		if e.Stage == "" || e.Scope != "stage_execution" || e.State != "running" {
			return ErrInvalid
		}
	case "guard_result":
		if e.Stage == "" || e.Scope != "precheck" && e.Scope != "stage_execution" || e.State != "passed" && e.State != "failed" {
			return ErrInvalid
		}
	case "run_intent":
		if e.Stage == "" || e.State != "running" || e.RuntimeID == "" {
			return ErrInvalid
		}
	case "run_returned":
		if e.Stage == "" || e.State != "completed" && e.State != "failed" && e.State != "cancelled" || e.RuntimeID == "" {
			return ErrInvalid
		}
	case "stage_outcome":
		if e.Stage == "" || e.State != "completed" && e.State != "failed" && e.State != "cancelled" {
			return ErrInvalid
		}
	case "terminal":
		if e.Stage != "" || e.State != "completed" && e.State != "failed" && e.State != "cancelled" {
			return ErrInvalid
		}
	case "recovery":
		if e.Stage != "" || e.State != "failed" || e.Reason != "interrupted" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if !reason(e.Reason) || !layer(e.Layer) {
		return ErrInvalid
	}
	expectedLayer := ""
	switch e.Reason {
	case "runtime_missing", "runtime_unavailable", "identity_mismatch", "capability_mismatch", "contract_invalid":
		expectedLayer = "guard"
	case "runtime_failed":
		expectedLayer = "runtime"
	case "prepare_failed":
		expectedLayer = "prepare"
	case "context_rejected":
		expectedLayer = "context"
	case "processing_failed":
		expectedLayer = "processing"
	case "critic_rejected":
		expectedLayer = "critic"
	case "result_invalid", "execution_failed":
		expectedLayer = "task"
	case "audit_persistence_failed":
		expectedLayer = "persistence"
	case "interrupted":
		expectedLayer = "recovery"
	}
	if expectedLayer != "" && e.Layer != expectedLayer {
		return ErrInvalid
	}
	if e.Reason == "cancelled" && e.Layer != "cancellation" && e.Layer != "guard" && e.Layer != "runtime" {
		return ErrInvalid
	}
	if e.State == "failed" && e.Reason == "none" {
		return ErrInvalid
	}
	if e.Kind == "stage_outcome" && e.State == "completed" && e.Layer != "" {
		return ErrInvalid
	}
	guardEvent := e.Kind == "guard_started" || e.Kind == "guard_result"
	runEvent := e.Kind == "run_intent" || e.Kind == "run_returned"
	if !guardEvent && !runEvent && (e.RuntimeID != "" || e.RuntimeVersion != "") {
		return ErrInvalid
	}
	if !guardEvent && e.Scope != "" {
		return ErrInvalid
	}
	if (e.State == "completed" || e.State == "passed") && e.Reason != "none" {
		return ErrInvalid
	}
	if e.State == "cancelled" && e.Reason != "cancelled" {
		return ErrInvalid
	}
	if e.Kind == "task_started" && (e.Reason != "none" || e.Layer != "") {
		return ErrInvalid
	}
	if e.Kind == "terminal" && e.State == "completed" && e.Layer != "" {
		return ErrInvalid
	}
	copy := e
	copy.Digest = ""
	if Digest(copy) != e.Digest {
		return ErrInvalid
	}
	return nil
}
func reason(r string) bool {
	switch r {
	case "none", "runtime_missing", "runtime_unavailable", "identity_mismatch", "capability_mismatch", "contract_invalid", "cancelled", "runtime_failed", "prepare_failed", "context_rejected", "processing_failed", "critic_rejected", "result_invalid", "execution_failed", "audit_persistence_failed", "interrupted":
		return true
	}
	return false
}
func layer(l string) bool {
	switch l {
	case "", "guard", "runtime", "prepare", "context", "processing", "critic", "task", "persistence", "recovery", "cancellation":
		return true
	}
	return false
}

func validTaskState(state string) bool {
	switch state {
	case "pending", "queued", "running", "cancelling", "completed", "failed", "cancelled":
		return true
	}
	return false
}
