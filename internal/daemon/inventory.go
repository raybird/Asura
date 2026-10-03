package daemon

import (
	"asura/internal/backend"
	"context"
	"encoding/json"
	"errors"
	"time"
)

// 2026-10-04：fresh inventory 查詢使用與 admission 相同的 Registry/probe。
func (c *Client) Inventory(ctx context.Context) (*backend.InventorySnapshot, error) {
	deadline := time.Now().Add(backend.InventoryTimeout + time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	defer c.conn.SetDeadline(time.Time{})
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	defer stop()
	if err := json.NewEncoder(c.conn).Encode(IPCRequest{Action: "inventory"}); err != nil {
		return nil, err
	}
	var resp IPCResponse
	if err := json.NewDecoder(c.conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if err := resp.Inventory.Validate(); err != nil {
		return nil, err
	}
	return resp.Inventory, nil
}
