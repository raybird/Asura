package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"asura/internal/backend"
	"asura/internal/daemon"
	"asura/internal/storage"
)

type Config struct {
	Key        string
	Origin     string
	SocketPath string
}

type Server struct {
	key        [32]byte
	origin     string
	host       string
	secure     bool
	socketPath string
	mu         sync.Mutex
	sessions   map[[32]byte]time.Time
}

const sessionCookie = "asura_session"

func NewServer(cfg Config) (*Server, error) {
	u, err := url.Parse(cfg.Origin)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("origin must be an absolute http(s) origin without a path")
	}
	if strings.TrimSpace(cfg.Key) == "" || cfg.SocketPath == "" {
		return nil, errors.New("access key and daemon socket are required")
	}
	return &Server{key: sha256.Sum256([]byte(cfg.Key)), origin: cfg.Origin, host: u.Host, secure: u.Scheme == "https", socketPath: cfg.SocketPath, sessions: make(map[[32]byte]time.Time)}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	if r.Host != s.host {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != s.origin {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if (r.URL.Path == "/" || r.URL.Path == "/index.html") && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(indexHTML))
		return
	}
	if r.URL.Path == "/api/login" && r.Method == http.MethodPost {
		s.login(w, r)
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || !s.authenticated(c.Value) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/api/logout" && r.Method == http.MethodPost {
		s.mu.Lock()
		delete(s.sessions, sha256.Sum256([]byte(c.Value)))
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/git-memory/") {
		if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.origin {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		s.handleGitMemory(w, r)
		return
	}
	if r.URL.Path == "/api/memories" || strings.HasPrefix(r.URL.Path, "/api/memories/") {
		s.handleMemories(w, r)
		return
	}
	if r.URL.Path == "/api/runtime-inventory" && r.Method == http.MethodGet {
		if source := r.Header.Get("Origin"); source != "" && source != s.origin {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		resp, err := s.exchange(r.Context(), daemon.IPCRequest{Action: "inventory"})
		if err != nil || resp.Error != "" {
			http.Error(w, "runtime inventory unavailable", http.StatusServiceUnavailable)
			return
		}
		if resp.Inventory == nil || resp.Inventory.Validate() != nil {
			http.Error(w, "invalid inventory", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(resp.Inventory)
		return
	}
	if r.URL.Path == "/api/projects" && r.Method == http.MethodGet {
		resp, err := s.exchange(r.Context(), daemon.IPCRequest{Action: "projects"})
		if err != nil {
			http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
			return
		}
		if resp.Error != "" {
			http.Error(w, resp.Error, http.StatusInternalServerError)
			return
		}
		projs := resp.Projects
		if projs == nil {
			projs = []string{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(projs)
		return
	}
	var req daemon.IPCRequest
	status := http.StatusOK
	switch {
	case r.URL.Path == "/api/tasks" && r.Method == http.MethodPost:
		var input struct {
			ID           string   `json:"id"`
			Workspace    string   `json:"workspace"`
			Prompt       string   `json:"prompt"`
			Backend      string   `json:"backend,omitempty"`
			MemoryIDs    []string `json:"memory_ids,omitempty"`
			MemoryRecall string   `json:"memory_recall,omitempty"`
		}
		if !decode(w, r, &input) {
			return
		}
		switch input.Backend {
		case "", "opencode", "codex", "omp", "antigravity", "auto":
		default:
			http.Error(w, "invalid backend", http.StatusBadRequest)
			return
		}
		if input.ID == "" || strings.TrimSpace(input.Prompt) == "" || input.Workspace == "" {
			http.Error(w, "id, workspace and prompt are required", http.StatusBadRequest)
			return
		}
		if len(input.MemoryIDs) > 32 || (input.MemoryRecall != "" && input.MemoryRecall != "off" && input.MemoryRecall != "bounded") {
			http.Error(w, "invalid memory selection", http.StatusBadRequest)
			return
		}
		for _, id := range input.MemoryIDs {
			if id == "" || len(id) > 256 {
				http.Error(w, "invalid memory selection", http.StatusBadRequest)
				return
			}
		}
		if input.MemoryRecall == "off" {
			input.MemoryRecall = ""
		}
		req = daemon.IPCRequest{Action: "submit", Backend: input.Backend, RequestID: input.ID, Workspace: input.Workspace, Prompt: input.Prompt, MemoryIDs: input.MemoryIDs, MemoryRecall: input.MemoryRecall}
		status = http.StatusAccepted
	case strings.HasPrefix(r.URL.Path, "/api/tasks/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		if r.Method == http.MethodPost && strings.HasSuffix(id, "/cancel") {
			req.Action = "cancel"
			id = strings.TrimSuffix(id, "/cancel")
		} else if r.Method == http.MethodGet && strings.HasSuffix(id, "/result") {
			id = strings.TrimSuffix(id, "/result")
			s.handleResultPagination(w, r, id)
			return
		} else if r.Method == http.MethodGet {
			req.Action = "status"
		} else {
			http.NotFound(w, r)
			return
		}
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		req.TaskID = id
	default:
		http.NotFound(w, r)
		return
	}
	resp, err := s.exchange(r.Context(), req)
	if err != nil {
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
		return
	}
	if resp.Error != "" {
		if resp.Inventory != nil && resp.Inventory.Validate() == nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(struct {
				Error     string                     `json:"error"`
				Inventory *backend.InventorySnapshot `json:"inventory_snapshot"`
			}{Error: "runtime admission rejected", Inventory: resp.Inventory})
			return
		}
		code := http.StatusBadRequest
		message := "task operation rejected"
		switch {
		case strings.HasPrefix(resp.Error, "unauthorized workspace:"):
			code = http.StatusForbidden
			message = "workspace not authorized"
		case resp.Error == daemon.ErrCapacityExceeded.Error():
			code = http.StatusServiceUnavailable
			message = "capacity exceeded"
		case strings.HasPrefix(resp.Error, "task not found"):
			code = http.StatusNotFound
			message = "task not found"
		case strings.Contains(resp.Error, "different payload"):
			code = http.StatusConflict
			message = "request ID reused with different payload"
		case strings.Contains(resp.Error, "cancelled request"):
			code = http.StatusBadRequest
			message = "cannot redeliver cancelled request"
		case strings.HasPrefix(resp.Error, "resolve memories: memory not found"):
			code = http.StatusNotFound
			message = "memory not found"
		}
		http.Error(w, message, code)
		return
	}
	if resp.Task == nil {
		http.Error(w, "invalid daemon response", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp.Task)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		writeDecodeError(w, err)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeDecodeError(w, err)
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid request", http.StatusBadRequest)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	candidate := sha256.Sum256([]byte(input.Key))
	if subtle.ConstantTimeCompare(candidate[:], s.key[:]) != 1 {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(raw[:])
	now := time.Now()
	s.mu.Lock()
	for key, expiry := range s.sessions {
		if !now.Before(expiry) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 128 {
		var oldestKey [32]byte
		var oldestExp time.Time
		first := true
		for key, expiry := range s.sessions {
			if first || expiry.Before(oldestExp) {
				oldestKey = key
				oldestExp = expiry
				first = false
			}
		}
		delete(s.sessions, oldestKey)
	}
	s.sessions[sha256.Sum256([]byte(token))] = now.Add(12 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authenticated(token string) bool {
	key := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.sessions[key]
	if !ok || !time.Now().Before(expiry) {
		delete(s.sessions, key)
		return false
	}
	return true
}

// 2026-09-13：每個 HTTP 操作獨立 IPC 連線，取消等待不影響已接受任務。
func (s *Server) exchange(parent context.Context, req daemon.IPCRequest) (daemon.IPCResponse, error) {
	var resp daemon.IPCResponse
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", s.socketPath)
	if err != nil {
		return resp, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return resp, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, err
	}
	var reader io.Reader = conn
	var bounded *io.LimitedReader
	if strings.HasPrefix(req.Action, "git_memory_") {
		bounded = &io.LimitedReader{R: conn, N: (1 << 20) + 1}
		reader = bounded
	}
	err = json.NewDecoder(reader).Decode(&resp)
	if bounded != nil && bounded.N < 1 {
		return resp, storage.ErrGitTooLarge
	}
	if err != nil && ctx.Err() != nil {
		return resp, ctx.Err()
	}
	return resp, err
}

// 2026-09-13 WEB-007：結果分頁讀取，以 Unicode rune 進行安全邊界切片，避免每頁無界載入。
func (s *Server) handleResultPagination(w http.ResponseWriter, r *http.Request, taskID string) {
	if taskID == "" || strings.Contains(taskID, "/") {
		http.NotFound(w, r)
		return
	}
	offsetStr := r.URL.Query().Get("offset")
	limitStr := r.URL.Query().Get("limit")
	if offsetStr == "" || limitStr == "" {
		http.Error(w, "offset and limit query parameters are required", http.StatusBadRequest)
		return
	}
	offset, err1 := strconv.Atoi(offsetStr)
	limit, err2 := strconv.Atoi(limitStr)
	if err1 != nil || err2 != nil || offset < 0 || limit <= 0 {
		http.Error(w, "invalid pagination parameters", http.StatusBadRequest)
		return
	}
	if limit > 10000 {
		limit = 10000
	}

	resp, err := s.exchange(r.Context(), daemon.IPCRequest{Action: "status", TaskID: taskID})
	if err != nil {
		http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
		return
	}
	if resp.Error != "" {
		code := http.StatusBadRequest
		message := "task operation rejected"
		switch {
		case strings.HasPrefix(resp.Error, "unauthorized workspace:"):
			code = http.StatusForbidden
			message = "workspace not authorized"
		case strings.HasPrefix(resp.Error, "task not found"):
			code = http.StatusNotFound
			message = "task not found"
		}
		http.Error(w, message, code)
		return
	}
	if resp.Task == nil {
		http.Error(w, "invalid daemon response", http.StatusBadGateway)
		return
	}

	runes := []rune(resp.Task.Result)
	total := len(runes)
	data := ""
	hasMore := false
	if offset < total {
		end := offset + limit
		if end < total {
			hasMore = true
		} else {
			end = total
		}
		data = string(runes[offset:end])
	}

	resultPage := struct {
		TaskID  string `json:"task_id"`
		Offset  int    `json:"offset"`
		Limit   int    `json:"limit"`
		Total   int    `json:"total"`
		Data    string `json:"data"`
		HasMore bool   `json:"has_more"`
	}{
		TaskID:  resp.Task.ID,
		Offset:  offset,
		Limit:   limit,
		Total:   total,
		Data:    data,
		HasMore: hasMore,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resultPage)
}
