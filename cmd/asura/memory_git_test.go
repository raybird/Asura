package main

import (
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"asura/internal/daemon"
	"asura/internal/gitmem"
	"asura/internal/storage"
)

func startMockDaemon(t *testing.T, handler func(req daemon.IPCRequest) daemon.IPCResponse) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "mock_daemon.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req daemon.IPCRequest
				if err := json.NewDecoder(c).Decode(&req); err == nil {
					resp := handler(req)
					_ = json.NewEncoder(c).Encode(resp)
				}
			}(conn)
		}
	}()

	t.Cleanup(func() {
		_ = l.Close()
	})
	return sock
}

func TestCLI_MemoryCapture(t *testing.T) {
	ws := "/workspace/project"
	sock := startMockDaemon(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action != "git_memory_capture" {
			return daemon.IPCResponse{Error: "unexpected action: " + req.Action}
		}
		if req.Workspace != ws {
			return daemon.IPCResponse{Error: "unexpected workspace: " + req.Workspace}
		}
		return daemon.IPCResponse{
			Candidate: &storage.MemoryCandidate{
				ID:           "cand-cli-1",
				Workspace:    ws,
				RepoID:       "repo-1",
				RefName:      "refs/heads/main",
				BaseSHA:      "base11111111",
				HeadSHA:      "head22222222",
				CommitCount:  2,
				DraftContent: "draft decision",
				Status:       "pending",
				CreatedAt:    time.Now().UTC(),
			},
			GitFacts: &gitmem.CaptureFacts{
				Truncated: false,
			},
			Message: "created new candidate draft",
		}
	})

	var out, stderr bytes.Buffer
	code := handleMemory([]string{"capture", "--socket", sock, "--workspace", ws, "--range", "HEAD~1..HEAD"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory capture failed (code %d): %s", code, stderr.String())
	}

	outStr := out.String()
	if !strings.Contains(outStr, "cand-cli-1") || !strings.Contains(outStr, "draft decision") {
		t.Errorf("unexpected capture output:\n%s", outStr)
	}

	// 測試 --json 模式
	out.Reset()
	stderr.Reset()
	code = handleMemory([]string{"capture", "--socket", sock, "--workspace", ws, "--json"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory capture --json failed: %s", stderr.String())
	}
	var jsonResp map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &jsonResp); err != nil {
		t.Fatalf("invalid json output: %v, raw: %s", err, out.String())
	}
	if jsonResp["candidate"] == nil {
		t.Errorf("expected candidate field in json output")
	}
}

func TestCLI_MemoryCandidates(t *testing.T) {
	ws := "/workspace/project"
	sock := startMockDaemon(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action != "git_memory_candidates" {
			return daemon.IPCResponse{Error: "unexpected action: " + req.Action}
		}
		return daemon.IPCResponse{
			Candidates: []storage.CandidateSummary{
				{
					ID:          "cand-list-1",
					Workspace:   ws,
					CommitCount: 3,
					Status:      "pending",
					Summary:     "feat(auth): initial login",
					CreatedAt:   time.Now().UTC(),
				},
			},
		}
	})

	var out, stderr bytes.Buffer
	code := handleMemory([]string{"candidates", "--socket", sock, "--workspace", ws}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory candidates failed: %s", stderr.String())
	}
	if !strings.Contains(out.String(), "cand-list-1") {
		t.Errorf("expected cand-list-1 in candidates table, got:\n%s", out.String())
	}
}

func TestCLI_MemoryApprove(t *testing.T) {
	ws := "/workspace/project"
	sock := startMockDaemon(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action != "git_memory_approve" {
			return daemon.IPCResponse{Error: "unexpected action: " + req.Action}
		}
		if req.CandidateID != "cand-app-1" {
			return daemon.IPCResponse{Error: "unexpected candidate_id: " + req.CandidateID}
		}
		return daemon.IPCResponse{
			ApprovedMemoryID: "mem-formal-1",
			Memory: &storage.Memory{
				ID:        "mem-formal-1",
				Workspace: ws,
				Content:   req.Content,
				Source:    "git-approved",
				CreatedAt: time.Now().UTC(),
			},
			Message: "candidate approved successfully",
		}
	})

	var out, stderr bytes.Buffer
	code := handleMemory([]string{"approve", "--socket", sock, "--workspace", ws, "--candidate", "cand-app-1", "--content", "custom decision"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory approve failed: %s", stderr.String())
	}
	if !strings.Contains(out.String(), "mem-formal-1") {
		t.Errorf("expected approved memory ID in output, got:\n%s", out.String())
	}
}

func TestCLI_MemoryDiscard(t *testing.T) {
	ws := "/workspace/project"
	sock := startMockDaemon(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action != "git_memory_discard" {
			return daemon.IPCResponse{Error: "unexpected action: " + req.Action}
		}
		return daemon.IPCResponse{
			Message: "candidate discarded",
		}
	})

	var out, stderr bytes.Buffer
	code := handleMemory([]string{"discard", "--socket", sock, "--workspace", ws, "--candidate", "cand-disc-1"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory discard failed: %s", stderr.String())
	}
	if !strings.Contains(out.String(), "candidate discarded") {
		t.Errorf("expected discarded message in output, got:\n%s", out.String())
	}
}

func TestCLI_MissingFlagsValidation(t *testing.T) {
	var out, stderr bytes.Buffer

	// capture 缺少 workspace
	if code := handleMemory([]string{"capture"}, &out, &stderr); code == 0 {
		t.Errorf("expected failure for capture without workspace")
	}

	// candidates 缺少 workspace
	out.Reset()
	stderr.Reset()
	if code := handleMemory([]string{"candidates"}, &out, &stderr); code == 0 {
		t.Errorf("expected failure for candidates without workspace")
	}

	// approve 缺少 candidate
	out.Reset()
	stderr.Reset()
	if code := handleMemory([]string{"approve", "--workspace", "/ws"}, &out, &stderr); code == 0 {
		t.Errorf("expected failure for approve without candidate")
	}

	// discard 缺少 candidate
	out.Reset()
	stderr.Reset()
	if code := handleMemory([]string{"discard", "--workspace", "/ws"}, &out, &stderr); code == 0 {
		t.Errorf("expected failure for discard without candidate")
	}

	// candidate 缺少 candidate
	out.Reset()
	stderr.Reset()
	if code := handleMemory([]string{"candidate", "--workspace", "/ws"}, &out, &stderr); code == 0 {
		t.Errorf("expected failure for candidate without candidate ID")
	}
}

func TestCLI_MemoryCandidate_Detail(t *testing.T) {
	ws := "/workspace/project"
	sock := startMockDaemon(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action != "git_memory_candidate" {
			return daemon.IPCResponse{Error: "unexpected action: " + req.Action}
		}
		if req.CandidateID != "cand-detail-1" {
			return daemon.IPCResponse{Error: "not found"}
		}
		return daemon.IPCResponse{
			Candidate: &storage.MemoryCandidate{
				ID:           "cand-detail-1",
				Workspace:    ws,
				CommitCount:  2,
				Status:       "pending",
				DraftContent: "Detail draft content here",
				CreatedAt:    time.Now().UTC(),
			},
		}
	})

	var out, stderr bytes.Buffer
	code := handleMemory([]string{"candidate", "--socket", sock, "--workspace", ws, "cand-detail-1"}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory candidate failed: %s", stderr.String())
	}
	if !strings.Contains(out.String(), "Detail draft content here") {
		t.Errorf("expected draft content in output, got:\n%s", out.String())
	}
}

func TestCLI_MemoryClearCandidates(t *testing.T) {
	ws := "/workspace/project"
	sock := startMockDaemon(t, func(req daemon.IPCRequest) daemon.IPCResponse {
		if req.Action != "git_memory_clear_candidates" {
			return daemon.IPCResponse{Error: "unexpected action: " + req.Action}
		}
		return daemon.IPCResponse{
			CandidateCount: 5,
		}
	})

	var out, stderr bytes.Buffer
	code := handleMemory([]string{"clear-candidates", "--socket", sock, "--workspace", ws}, &out, &stderr)
	if code != 0 {
		t.Fatalf("handleMemory clear-candidates failed: %s", stderr.String())
	}
	if !strings.Contains(out.String(), "Cleared 5 processed candidates") {
		t.Errorf("expected cleared message in output, got:\n%s", out.String())
	}
}
