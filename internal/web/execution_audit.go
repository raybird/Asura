package web

import (
	"asura/internal/audit"
	"asura/internal/daemon"
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleTaskAudit(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/audit")
	if id == "" || len(id) > 256 || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	response, err := s.exchange(r.Context(), daemon.IPCRequest{Action: "task_audit", TaskID: id})
	if err != nil {
		http.Error(w, "audit unavailable", http.StatusServiceUnavailable)
		return
	}
	if response.Error != "" {
		code := http.StatusServiceUnavailable
		if strings.HasPrefix(response.Error, "unauthorized workspace:") {
			code = http.StatusForbidden
		}
		if response.Error == "task not found" {
			code = http.StatusNotFound
		}
		if response.Error == "audit_invalid" {
			code = http.StatusBadGateway
		}
		http.Error(w, "audit unavailable", code)
		return
	}
	if response.Audit.Validate() != nil {
		http.Error(w, "audit invalid", http.StatusBadGateway)
		return
	}
	raw, err := json.Marshal(response.Audit)
	if err != nil || len(raw)+1 > audit.MaxResponseBytes {
		http.Error(w, "audit invalid", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(append(raw, '\n'))
}
