package web

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"asura/internal/daemon"
	"asura/internal/storage"
)

// 2026-09-30 WEBGIT-001～008：既有 session/Host/Origin middleware 後的薄 adapter。
func (s *Server) handleGitMemory(w http.ResponseWriter, r *http.Request) {
	req := daemon.IPCRequest{}
	path := r.URL.Path
	if path == "/api/git-memory/capture" && r.Method == http.MethodPost {
		var input struct {
			Workspace string `json:"workspace"`
			Range     string `json:"range"`
			SinceLast bool   `json:"since_last"`
		}
		if len(r.URL.Query()) != 0 || !decodeMemoryAdd(w, r, &input) {
			if len(r.URL.Query()) != 0 {
				http.Error(w, "invalid git request", 400)
			}
			return
		}
		if (input.Range != "") == input.SinceLast || len(input.Range) > 4096 || !utf8.ValidString(input.Range) {
			http.Error(w, "invalid git capture", 400)
			return
		}
		req = daemon.IPCRequest{Action: "git_memory_capture_bounded", Workspace: input.Workspace, Range: input.Range, SinceLast: input.SinceLast}
	} else if path == "/api/git-memory/candidates" && r.Method == http.MethodGet {
		if !validMemoryQuery(r, "workspace", "status", "limit", "cursor") {
			http.Error(w, "invalid git query", 400)
			return
		}
		q := r.URL.Query()
		status := q.Get("status")
		if status == "" {
			status = "pending"
		}
		limit := 20
		if raw := q.Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				http.Error(w, "invalid git limit", 400)
				return
			}
		}
		if !storage.ValidCandidateStatus(status) || limit < 1 || limit > 50 || len(q.Get("cursor")) > 1024 {
			http.Error(w, "invalid git page", 400)
			return
		}
		req = daemon.IPCRequest{Action: "git_memory_page", Workspace: q.Get("workspace"), Status: status, Limit: limit, Cursor: q.Get("cursor")}
	} else if strings.HasPrefix(path, "/api/git-memory/candidates/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/git-memory/candidates/"), "/")
		if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 256 || !utf8.ValidString(parts[0]) {
			http.Error(w, "invalid candidate ID", 400)
			return
		}
		req.CandidateID = parts[0]
		if len(parts) == 1 && r.Method == http.MethodGet {
			if !validMemoryQuery(r, "workspace") {
				http.Error(w, "invalid git query", 400)
				return
			}
			req.Action = "git_memory_detail_bounded"
			req.Workspace = r.URL.Query().Get("workspace")
		} else if len(parts) == 2 && r.Method == http.MethodPost && (parts[1] == "approve" || parts[1] == "discard") {
			if len(r.URL.Query()) != 0 {
				http.Error(w, "invalid git query", 400)
				return
			}
			if parts[1] == "approve" {
				var input struct {
					Workspace string          `json:"workspace"`
					Content   json.RawMessage `json:"content"`
				}
				if !decodeMemoryAdd(w, r, &input) {
					return
				}
				req.Workspace = input.Workspace
				if input.Content != nil {
					var content string
					if string(input.Content) == "null" || json.Unmarshal(input.Content, &content) != nil {
						http.Error(w, "invalid approval content", 400)
						return
					}
					if err := storage.ValidateGitApprovalContent(content); err != nil {
						status := 400
						if errors.Is(err, storage.ErrGitTooLarge) {
							status = 413
						}
						http.Error(w, "invalid approval content", status)
						return
					}
					req.Content = content
					req.ContentProvided = true
				}
				req.Action = "git_memory_approve_bounded"
			} else {
				var input struct {
					Workspace string `json:"workspace"`
				}
				if !decodeMemoryAdd(w, r, &input) {
					return
				}
				req.Workspace = input.Workspace
				req.Action = "git_memory_discard_bounded"
			}
		} else {
			http.NotFound(w, r)
			return
		}
	} else {
		http.NotFound(w, r)
		return
	}
	if req.Workspace == "" || len(req.Workspace) > 4096 || !utf8.ValidString(req.Workspace) {
		http.Error(w, "invalid workspace", 400)
		return
	}
	resp, err := s.exchange(r.Context(), req)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, storage.ErrGitTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			status = http.StatusGatewayTimeout
		}
		http.Error(w, "git daemon request failed", status)
		return
	}
	if resp.Error != "" {
		status := http.StatusInternalServerError
		switch resp.GitError {
		case "invalid":
			status = 400
		case "forbidden":
			status = 403
		case "not_found":
			status = 404
		case "conflict":
			status = 409
		case "too_large":
			status = 413
		case "unsupported":
			status = 422
		case "timeout":
			status = 504
		}
		http.Error(w, "git memory request rejected ("+resp.GitError+")", status)
		return
	}
	var value any
	switch req.Action {
	case "git_memory_page":
		if resp.CandidatePage != nil {
			value = resp.CandidatePage
		}
	case "git_memory_detail_bounded":
		if resp.Candidate != nil {
			value = resp.Candidate
		}
	case "git_memory_capture_bounded":
		if resp.GitCapture != nil {
			value = resp.GitCapture
		}
	case "git_memory_approve_bounded":
		if resp.ApprovedMemoryID != "" {
			value = struct {
				ID string `json:"approved_memory_id"`
			}{resp.ApprovedMemoryID}
		}
	case "git_memory_discard_bounded":
		if resp.Message == "discarded" {
			value = struct {
				Status string `json:"status"`
			}{"discarded"}
		}
	}
	if value == nil {
		http.Error(w, "invalid daemon response", 500)
		return
	}
	writeMemoryJSON(w, value, http.StatusOK)
}
