package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"asura/internal/gitmem"
	"asura/internal/storage"
)

func (d *Daemon) handleGitMemoryCapture(ctx context.Context, req IPCRequest, encoder *json.Encoder) {
	if req.Workspace == "" {
		_ = encoder.Encode(IPCResponse{Error: "workspace is required"})
		return
	}
	if d.policy == nil {
		_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
		return
	}
	canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
		return
	}

	exec := gitmem.NewGitExecutor(d.policy.AllowedRoots())
	service := gitmem.NewCaptureService(exec, d.store)

	res, err := service.Capture(ctx, canonicalScope, gitmem.CaptureOptions{
		RangeExpr: req.Range,
		SinceLast: req.SinceLast,
		Force:     req.Force,
	})
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("capture failed: %v", err)})
		return
	}

	_ = encoder.Encode(IPCResponse{
		Candidate: res.Candidate,
		GitFacts:  res.Facts,
		IsReused:  res.IsReused,
		Message:   res.Message,
	})
}

func (d *Daemon) handleGitMemoryCandidates(ctx context.Context, req IPCRequest, encoder *json.Encoder) {
	if req.Workspace == "" {
		_ = encoder.Encode(IPCResponse{Error: "workspace is required"})
		return
	}
	if d.policy == nil {
		_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
		return
	}
	canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
		return
	}

	cands, err := d.store.ListCandidates(ctx, canonicalScope, req.Status, req.Limit)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("list candidates failed: %v", err)})
		return
	}

	_ = encoder.Encode(IPCResponse{
		Candidates: cands,
	})
}

func (d *Daemon) handleGitMemoryApprove(ctx context.Context, req IPCRequest, encoder *json.Encoder) {
	if req.Workspace == "" || req.CandidateID == "" {
		_ = encoder.Encode(IPCResponse{Error: "workspace and candidate_id are required"})
		return
	}
	if d.policy == nil {
		_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
		return
	}
	canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
		return
	}

	memID, err := d.store.ApproveCandidateTx(ctx, canonicalScope, req.CandidateID, storage.ApproveOptions{
		Content: req.Content,
	})
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("approve candidate failed: %v", err)})
		return
	}

	mem, _ := d.store.GetMemory(ctx, canonicalScope, memID)

	_ = encoder.Encode(IPCResponse{
		ApprovedMemoryID: memID,
		Memory:           mem,
		Message:          "candidate approved successfully",
	})
}

func (d *Daemon) handleGitMemoryDiscard(ctx context.Context, req IPCRequest, encoder *json.Encoder) {
	if req.Workspace == "" || req.CandidateID == "" {
		_ = encoder.Encode(IPCResponse{Error: "workspace and candidate_id are required"})
		return
	}
	if d.policy == nil {
		_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
		return
	}
	canonicalScope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("unauthorized workspace: %v", err)})
		return
	}

	cand, err := d.store.GetCandidate(ctx, canonicalScope, req.CandidateID)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("get candidate: %v", err)})
		return
	}
	if cand.Status == "approved" {
		_ = encoder.Encode(IPCResponse{Error: "cannot discard an already approved candidate; delete the formal memory instead"})
		return
	}

	if err := d.store.UpdateCandidateStatus(ctx, canonicalScope, req.CandidateID, "discarded", "", nil); err != nil {
		_ = encoder.Encode(IPCResponse{Error: fmt.Sprintf("discard candidate failed: %v", err)})
		return
	}

	_ = encoder.Encode(IPCResponse{
		Message: "candidate discarded",
	})
}

// CaptureGitMemory 用戶端呼叫 Git 記憶擷取
func (c *Client) CaptureGitMemory(ctx context.Context, workspace string, rangeExpr string, sinceLast bool, force bool) (*IPCResponse, error) {
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}

	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "git_memory_capture",
		Workspace: workspace,
		Range:     rangeExpr,
		SinceLast: sinceLast,
		Force:     force,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return &resp, nil
}

// ListGitCandidates 用戶端查詢候選記憶清單
func (c *Client) ListGitCandidates(ctx context.Context, workspace string, status string, limit int) ([]storage.MemoryCandidate, error) {
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}

	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:    "git_memory_candidates",
		Workspace: workspace,
		Status:    status,
		Limit:     limit,
	}); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.Candidates, nil
}

// ApproveGitCandidate 用戶端審核並核准候選記憶
func (c *Client) ApproveGitCandidate(ctx context.Context, workspace string, candidateID string, content string) (string, *storage.Memory, error) {
	if workspace == "" || candidateID == "" {
		return "", nil, errors.New("workspace and candidate_id are required")
	}

	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:      "git_memory_approve",
		Workspace:   workspace,
		CandidateID: candidateID,
		Content:     content,
	}); err != nil {
		return "", nil, fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return "", nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return "", nil, errors.New(resp.Error)
	}
	return resp.ApprovedMemoryID, resp.Memory, nil
}

// DiscardGitCandidate 用戶端標記捨棄候選記憶
func (c *Client) DiscardGitCandidate(ctx context.Context, workspace string, candidateID string) error {
	if workspace == "" || candidateID == "" {
		return errors.New("workspace and candidate_id are required")
	}

	encoder := json.NewEncoder(c.conn)
	decoder := json.NewDecoder(c.conn)

	if err := encoder.Encode(IPCRequest{
		Action:      "git_memory_discard",
		Workspace:   workspace,
		CandidateID: candidateID,
	}); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	var resp IPCResponse
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}
