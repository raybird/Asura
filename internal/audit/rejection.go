package audit

import (
	"asura/internal/backend"
	"encoding/json"
)

type Rejection struct {
	Schema           string                          `json:"schema"`
	RequestDigest    string                          `json:"request_digest,omitempty"`
	Mode             string                          `json:"mode"`
	RequestedBackend string                          `json:"requested_backend"`
	Reason           string                          `json:"reason"`
	Requirements     []backend.ExecutionRequirements `json:"requirements"`
	Runtimes         []Runtime                       `json:"runtimes"`
}

func AdmissionRejection(request, mode, requested string, snapshot *backend.InventorySnapshot) *Rejection {
	if mode == "" {
		mode = "direct"
	}
	if requested == "" {
		requested = "opencode"
	}
	if !safeID(requested, 64) {
		requested = "unknown"
	}
	req, err := backend.RequirementsForMode(mode)
	if err != nil {
		return nil
	}
	r := &Rejection{Schema: Version, RequestDigest: Correlation(request), Mode: mode, RequestedBackend: requested, Reason: "admission_rejected", Requirements: req, Runtimes: []Runtime{}}
	if snapshot != nil && snapshot.Validate() == nil {
		for _, e := range snapshot.Entries {
			x := Runtime{ID: e.RuntimeID, Availability: e.AvailabilityState, Reason: e.ReasonCode, ObservedAt: e.ObservedAt}
			if e.Descriptor != nil {
				x.Version = e.Descriptor.Version
				x.Capabilities = e.Descriptor.Capabilities
			}
			r.Runtimes = append(r.Runtimes, x)
		}
	}
	raw, _ := json.Marshal(r)
	if len(raw) > MaxHeaderBytes {
		return nil
	}
	return r
}
func (r *Rejection) Validate() error {
	if r == nil || r.Schema != Version || r.Reason != "admission_rejected" || !safeID(r.RequestedBackend, 64) || len(r.Runtimes) > 32 || r.RequestDigest != "" && len(r.RequestDigest) != 64 {
		return ErrInvalid
	}
	if _, e := backend.RequirementsForMode(r.Mode); e != nil {
		return ErrInvalid
	}
	for _, x := range r.Runtimes {
		if !safeID(x.ID, 64) || !validVersion(x.Version) || !probeReason(x.Reason) && x.Reason != backend.ProbeOK || len(x.Capabilities) > 4 {
			return ErrInvalid
		}
		for _, cap := range x.Capabilities {
			if !capability(cap) {
				return ErrInvalid
			}
		}
	}
	raw, _ := json.Marshal(r)
	if len(raw) > MaxHeaderBytes {
		return ErrInvalid
	}
	return nil
}
