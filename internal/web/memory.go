package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"asura/internal/daemon"
)

// 2026-09-23：WEBMEM-001～004 共用已登入的 Web 入口。
func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	workspace := r.URL.Query().Get("workspace")
	if path == "/api/memories" {
		switch r.Method {
		case http.MethodGet:
			if workspace == "" || !validMemoryQuery(r, "workspace", "limit", "cursor") {
				http.Error(w, "invalid memory request", http.StatusBadRequest)
				return
			}
			limit := 20
			if raw := r.URL.Query().Get("limit"); raw != "" {
				var err error
				limit, err = strconv.Atoi(raw)
				if err != nil || limit < 1 || limit > 50 {
					http.Error(w, "invalid memory request", http.StatusBadRequest)
					return
				}
			}
			cursor := r.URL.Query().Get("cursor")
			if len(cursor) > 1024 {
				http.Error(w, "invalid memory cursor", http.StatusBadRequest)
				return
			}
			resp, ok := s.memoryExchange(w, r, daemon.IPCRequest{Action: "memory_page", Workspace: workspace, Limit: limit, Cursor: cursor})
			if !ok {
				return
			}
			if resp.MemoryPage == nil {
				http.Error(w, "invalid daemon response", http.StatusInternalServerError)
				return
			}
			writeMemoryJSON(w, resp.MemoryPage, http.StatusOK)
		case http.MethodPost:
			if len(r.URL.Query()) != 0 {
				http.Error(w, "invalid memory request", http.StatusBadRequest)
				return
			}
			var input struct {
				Workspace string `json:"workspace"`
				Content   string `json:"content"`
			}
			if !decodeMemoryAdd(w, r, &input) {
				return
			}
			if input.Workspace == "" || input.Content == "" {
				http.Error(w, "invalid memory request", http.StatusBadRequest)
				return
			}
			if len(input.Content) > 32768 {
				http.Error(w, "memory content too large", http.StatusRequestEntityTooLarge)
				return
			}
			if !utf8.ValidString(input.Content) {
				http.Error(w, "invalid memory request", http.StatusBadRequest)
				return
			}
			resp, ok := s.memoryExchange(w, r, daemon.IPCRequest{Action: "memory_add", Workspace: input.Workspace, Content: input.Content})
			if !ok {
				return
			}
			if resp.Memory == nil {
				http.Error(w, "invalid daemon response", http.StatusInternalServerError)
				return
			}
			writeMemoryJSON(w, resp.Memory, http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if !strings.HasPrefix(path, "/api/memories/") || (r.Method != http.MethodGet && r.Method != http.MethodDelete) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(path, "/api/memories/")
	if id == "" || strings.Contains(id, "/") || len(id) > 256 || !utf8.ValidString(id) || workspace == "" || !validMemoryQuery(r, "workspace") {
		http.Error(w, "invalid memory request", http.StatusBadRequest)
		return
	}
	action := "memory_detail_bounded"
	if r.Method == http.MethodDelete {
		action = "memory_remove"
	}
	resp, ok := s.memoryExchange(w, r, daemon.IPCRequest{Action: action, Workspace: workspace, MemoryID: id})
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if resp.Memory == nil {
		http.Error(w, "invalid daemon response", http.StatusInternalServerError)
		return
	}
	writeMemoryJSON(w, resp.Memory, http.StatusOK)
}

func decodeMemoryAdd(w http.ResponseWriter, r *http.Request, value any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil {
		writeDecodeError(w, err)
		return false
	}
	if !utf8.Valid(body) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeDecodeError(w, err)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeDecodeError(w, err)
		return false
	}
	return true
}

func validMemoryQuery(r *http.Request, allowed ...string) bool {
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			return false
		}
		valid := false
		for _, name := range allowed {
			if key == name {
				valid = true
				break
			}
		}
		if !valid {
			return false
		}
	}
	return true
}

func (s *Server) memoryExchange(w http.ResponseWriter, r *http.Request, req daemon.IPCRequest) (daemon.IPCResponse, bool) {
	resp, err := s.exchange(r.Context(), req)
	if err != nil {
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
		return resp, false
	}
	if resp.Error == "" {
		return resp, true
	}
	status, message := http.StatusInternalServerError, "memory operation failed"
	switch {
	case resp.Error == "unauthorized workspace" || strings.HasPrefix(resp.Error, "unauthorized workspace:"):
		status, message = http.StatusForbidden, "workspace not authorized"
	case resp.Error == "memory not found":
		status, message = http.StatusNotFound, "memory not found"
	case resp.Error == "invalid memory cursor" || resp.Error == "invalid memory request":
		status, message = http.StatusBadRequest, "invalid memory request"
	case resp.Error == "memory content too large" || resp.Error == "memory response too large":
		status, message = http.StatusRequestEntityTooLarge, "memory too large"
	case resp.Error == "invalid memory metadata":
		status, message = http.StatusUnprocessableEntity, "memory data unsupported"
	}
	http.Error(w, message, status)
	return resp, false
}

func writeMemoryJSON(w http.ResponseWriter, value any, status int) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "memory response failed", http.StatusInternalServerError)
		return
	}
	if len(body)+1 > 512<<10 {
		http.Error(w, "memory too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
