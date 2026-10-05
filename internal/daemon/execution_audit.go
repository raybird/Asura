package daemon

import (
	"asura/internal/audit"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

func (d *Daemon) taskAudit(ctx context.Context, id string) IPCResponse {
	if id == "" || len(id) > 256 {
		return IPCResponse{Error: "audit_invalid"}
	}
	workspace, err := d.store.TaskAuditWorkspace(ctx, id)
	if err != nil {
		return IPCResponse{Error: "task not found"}
	}
	if d.authorizeTask(&storage.Task{ID: id, Workspace: workspace}) != nil {
		return IPCResponse{Error: "unauthorized workspace: audit access denied"}
	}
	r, err := d.store.GetTaskAudit(ctx, id)
	if err != nil {
		return IPCResponse{Error: err.Error()}
	}
	if r.Validate() != nil {
		return IPCResponse{Error: "audit_invalid"}
	}
	response := IPCResponse{Audit: r}
	raw, _ := json.Marshal(response)
	if len(raw)+1 > audit.MaxResponseBytes {
		return IPCResponse{Error: "audit_invalid"}
	}
	return response
}
func (c *Client) GetTaskAudit(ctx context.Context, id string) (*audit.Record, error) {
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	defer c.conn.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { c.conn.Close() })
	defer stop()
	if err := json.NewEncoder(c.conn).Encode(IPCRequest{Action: "task_audit", TaskID: id}); err != nil {
		return nil, err
	}
	limited := &io.LimitedReader{R: c.conn, N: audit.MaxResponseBytes + 1}
	var response IPCResponse
	if json.NewDecoder(limited).Decode(&response) != nil || limited.N < 1 {
		return nil, audit.ErrInvalid
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	if response.Audit.Validate() != nil {
		return nil, audit.ErrInvalid
	}
	return response.Audit, nil
}
