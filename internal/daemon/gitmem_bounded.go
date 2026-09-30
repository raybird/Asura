package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"asura/internal/gitmem"
	"asura/internal/storage"
)

type GitCandidatePage struct {
	Items      []storage.CandidateSummary `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

type GitCaptureReply struct {
	CandidateID     string `json:"candidate_id,omitempty"`
	Outcome         string `json:"outcome"`
	Reused          bool   `json:"reused"`
	Message         string `json:"message"`
	DetailAvailable bool   `json:"detail_available"`
}

func gitFailure(err error) IPCResponse {
	code := "failed"
	switch {
	case errors.Is(err, storage.ErrCandidateNotFound):
		code = "not_found"
	case errors.Is(err, storage.ErrCandidateStatusConflict):
		code = "conflict"
	case errors.Is(err, storage.ErrGitRequest):
		code = "invalid"
	case errors.Is(err, storage.ErrGitTooLarge):
		code = "too_large"
	case errors.Is(err, storage.ErrGitMetadata):
		code = "unsupported"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		code = "timeout"
	}
	return IPCResponse{Error: "git memory operation rejected", GitError: code}
}

// 2026-09-30 WEBGIT-004：先編碼檢查 escaped JSON，HTTP payload 及 IPC envelope 均有界。
func writeGitIPC(encoder *json.Encoder, resp IPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil || len(data)+1 > 512<<10 {
		resp = gitFailure(storage.ErrGitTooLarge)
	}
	_ = encoder.Encode(resp)
}

func (d *Daemon) handleBoundedGitMemory(ctx context.Context, req IPCRequest, encoder *json.Encoder) {
	reply := func(resp IPCResponse) { writeGitIPC(encoder, resp) }
	if req.Workspace == "" || len(req.Workspace) > 4096 || !utf8.ValidString(req.Workspace) || req.Force || req.AIDraft || len(req.CandidateID) > 256 || !utf8.ValidString(req.CandidateID) {
		reply(gitFailure(storage.ErrGitRequest))
		return
	}
	if d.policy == nil {
		reply(IPCResponse{Error: "unauthorized workspace", GitError: "forbidden"})
		return
	}
	scope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		reply(IPCResponse{Error: "unauthorized workspace", GitError: "forbidden"})
		return
	}
	if len(scope) > 4096 {
		reply(gitFailure(storage.ErrGitTooLarge))
		return
	}
	switch req.Action {
	case "git_memory_page":
		status := req.Status
		if status == "" {
			status = "pending"
		}
		limit := req.Limit
		if limit == 0 {
			limit = 20
		}
		items, next, err := d.store.PageGitCandidates(ctx, scope, status, limit, req.Cursor)
		if err != nil {
			reply(gitFailure(err))
			return
		}
		reply(IPCResponse{CandidatePage: &GitCandidatePage{Items: items, NextCursor: next}})
	case "git_memory_detail_bounded":
		if req.CandidateID == "" {
			reply(gitFailure(storage.ErrGitRequest))
			return
		}
		cand, err := d.store.GetGitCandidateBounded(ctx, scope, req.CandidateID)
		if err != nil {
			reply(gitFailure(err))
			return
		}
		reply(IPCResponse{Candidate: cand})
	case "git_memory_approve_bounded":
		if req.CandidateID == "" {
			reply(gitFailure(storage.ErrGitRequest))
			return
		}
		id, err := d.store.ApproveCandidateTx(ctx, scope, req.CandidateID, storage.ApproveOptions{Bounded: true, Content: req.Content, ContentSet: req.ContentProvided})
		if err != nil {
			reply(gitFailure(err))
			return
		}
		reply(IPCResponse{ApprovedMemoryID: id})
	case "git_memory_discard_bounded":
		if req.CandidateID == "" {
			reply(gitFailure(storage.ErrGitRequest))
			return
		}
		err := d.store.DiscardGitCandidateBounded(ctx, scope, req.CandidateID)
		if err != nil {
			reply(gitFailure(err))
			return
		}
		reply(IPCResponse{Message: "discarded"})
	case "git_memory_capture_bounded":
		if (req.Range != "") == req.SinceLast || len(req.Range) > 4096 || !utf8.ValidString(req.Range) {
			reply(gitFailure(storage.ErrGitRequest))
			return
		}
		exec := gitmem.NewGitExecutor(d.policy.AllowedRoots())
		if _, err := exec.ValidateAndResolveRepo(ctx, scope); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				reply(gitFailure(err))
			} else {
				reply(gitFailure(storage.ErrGitMetadata))
			}
			return
		}
		service := gitmem.NewCaptureService(exec, d.store)
		res, err := service.Capture(ctx, scope, gitmem.CaptureOptions{RangeExpr: req.Range, SinceLast: req.SinceLast, HeadersOnly: true})
		if err != nil {
			switch {
			case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
				reply(gitFailure(err))
			case strings.Contains(err.Error(), "already discarded"):
				reply(gitFailure(storage.ErrCandidateStatusConflict))
			case strings.Contains(err.Error(), "resolve range") || strings.Contains(err.Error(), "checkpoint") || strings.Contains(err.Error(), "range"):
				reply(gitFailure(storage.ErrGitRequest))
			default:
				reply(gitFailure(err))
			}
			return
		}
		outcome := "Complete"
		id := ""
		detailAvailable := false
		if res.Candidate != nil {
			id = res.Candidate.ID
			if res.Candidate.IsTruncated {
				outcome = "Partial"
			}
			_, err = d.store.GetGitCandidateBounded(ctx, scope, id)
			detailAvailable = err == nil
		}
		if len(id) > 256 {
			reply(gitFailure(storage.ErrGitTooLarge))
			return
		}
		message := res.Message
		if len(message) > 4096 {
			message = "capture persisted; query candidate state"
		}
		reply(IPCResponse{GitCapture: &GitCaptureReply{CandidateID: id, Outcome: outcome, Reused: res.IsReused, Message: message, DetailAvailable: detailAvailable}})
	}
}
