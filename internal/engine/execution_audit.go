package engine

import (
	"asura/internal/audit"
	"asura/internal/storage"
	"context"
	"errors"
	"time"
)

type taskAuditObserver struct {
	store *storage.SQLiteStore
	id    string
}

func (o *taskAuditObserver) Record(_ context.Context, e audit.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.store.AppendTaskAudit(ctx, o.id, e); err != nil {
		return errors.Join(audit.ErrPersistence, err)
	}
	return nil
}
