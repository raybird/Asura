package daemon

import (
	"asura/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

func (d *Daemon) lifecycleQuery(ctx context.Context, req IPCRequest) IPCResponse {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if d.policy == nil {
		return IPCResponse{Error: "unauthorized workspace"}
	}
	scope, err := d.policy.AuthorizeCanonicalWorkspace(req.Workspace)
	if err != nil {
		return IPCResponse{Error: "unauthorized workspace"}
	}
	if req.MemoryID == "" || len(req.MemoryID) > 256 || !utf8.ValidString(req.MemoryID) {
		return IPCResponse{Error: "invalid memory id"}
	}
	var resp IPCResponse
	switch req.Action {
	case "memory_durable":
		resp.Memory, err = d.store.MarkMemoryDurable(ctx, scope, req.MemoryID)
	case "memory_detail":
		var detail storage.MemoryDetail
		detail, err = d.store.GetMemoryDetail(ctx, scope, req.MemoryID)
		resp.MemoryDetail = &detail
	}
	if err != nil {
		return IPCResponse{Error: err.Error()}
	}
	b, err := json.Marshal(resp)
	if err != nil || len(b) > 131072 {
		return IPCResponse{Error: "memory response too large"}
	}
	return resp
}

func (c *Client) AddMemoryWithOptions(ctx context.Context, workspace, content string, o storage.CreateMemoryOptions) (*storage.Memory, error) {
	resp, err := c.lifecycleExchange(ctx, IPCRequest{Action: "memory_add", Workspace: workspace, Content: content, Supersedes: o.Supersedes, IsDurable: o.Durable, OperationID: o.OperationID, Reason: o.Reason})
	if err != nil {
		return nil, err
	}
	if resp.Memory == nil {
		return nil, errors.New("empty memory in response")
	}
	return resp.Memory, nil
}
func (c *Client) MemoryDetail(ctx context.Context, ws, id string) (*storage.MemoryDetail, error) {
	resp, err := c.lifecycleExchange(ctx, IPCRequest{Action: "memory_detail", Workspace: ws, MemoryID: id})
	if err != nil {
		return nil, err
	}
	if resp.MemoryDetail == nil {
		return nil, errors.New("empty memory detail")
	}
	return resp.MemoryDetail, nil
}
func (c *Client) MarkMemoryDurable(ctx context.Context, ws, id string) (*storage.Memory, error) {
	resp, err := c.lifecycleExchange(ctx, IPCRequest{Action: "memory_durable", Workspace: ws, MemoryID: id})
	if err != nil {
		return nil, err
	}
	return resp.Memory, nil
}
func (c *Client) lifecycleExchange(ctx context.Context, req IPCRequest) (IPCResponse, error) {
	deadline := time.Now().Add(2 * time.Second)
	if at, ok := ctx.Deadline(); ok && at.Before(deadline) {
		deadline = at
	}
	if err := ctx.Err(); err != nil {
		return IPCResponse{}, err
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return IPCResponse{}, err
	}
	defer c.conn.SetDeadline(time.Time{})
	if err := json.NewEncoder(c.conn).Encode(req); err != nil {
		return IPCResponse{}, err
	}
	var resp IPCResponse
	if err := json.NewDecoder(c.conn).Decode(&resp); err != nil {
		return resp, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
