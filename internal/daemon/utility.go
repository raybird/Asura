package daemon

import (
	"asura/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

func (d *Daemon) handleUtilityQuery(ctx context.Context, req IPCRequest, encoder *json.Encoder) {
	if d.policy == nil {
		_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
		return
	}
	scope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: "unauthorized workspace"})
		return
	}
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	page, err := d.store.GetMemoryUtility(ctx, scope, req.MemoryID, limit, req.Cursor)
	if err != nil {
		_ = encoder.Encode(IPCResponse{Error: err.Error()})
		return
	}
	page.UtilityPolicy = d.cfg.UtilityPolicy
	if page.UtilityPolicy == "" {
		page.UtilityPolicy = "observe"
	}
	if page.UtilityPolicy == "observe" {
		page.WeightPPM = 1000000
	}
	b, err := json.Marshal(IPCResponse{UtilityPage: &page})
	if err != nil || len(b)+1 > 65536 {
		_ = encoder.Encode(IPCResponse{Error: "utility response too large"})
		return
	}
	_ = encoder.Encode(json.RawMessage(b))
}

func (c *Client) MemoryUtility(ctx context.Context, workspace, id string, limit int, cursor string) (storage.UtilityPage, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}
	if err := json.NewEncoder(c.conn).Encode(IPCRequest{Action: "memory_utility", Workspace: workspace, MemoryID: id, Limit: limit, Cursor: cursor}); err != nil {
		return storage.UtilityPage{}, err
	}
	var resp IPCResponse
	reader := &io.LimitedReader{R: c.conn, N: 65537}
	if err := json.NewDecoder(reader).Decode(&resp); err != nil {
		return storage.UtilityPage{}, err
	}
	if reader.N == 0 {
		return storage.UtilityPage{}, errors.New("utility response too large")
	}
	if resp.Error != "" {
		return storage.UtilityPage{}, errors.New(resp.Error)
	}
	if resp.UtilityPage == nil {
		return storage.UtilityPage{}, errors.New("utility response unavailable")
	}
	return *resp.UtilityPage, nil
}
