package verification

import (
	"context"
	"errors"
	"testing"

	"asura/internal/backend"
)

// subjectBackend 只在驗證紀錄含有 id 時放行，模擬 adapter 的 admission。
type subjectBackend struct {
	shellBackend
	spec    Spec
	records string
}

func (b *subjectBackend) VerificationSpec(context.Context) (Spec, error) { return b.spec, nil }
func (b *subjectBackend) Version(context.Context) (string, error) {
	_, err := Admit(b.spec.Record.Runtime, b.spec.Record.Version, func() (string, error) { return b.spec.Record.Digest, nil }, nil, b.records)
	return b.spec.Record.Version, err
}

// 2026-10-22：issue-73 Task 3.1，Prepare 讓未驗證的身分只透過暫時紀錄放行，內建身分不寫紀錄。
func TestPrepare(t *testing.T) {
	id := Identity{Runtime: "rt", Version: "2.0", Digest: "abc"}
	newBackend := func(spec Spec) func(string) backend.Backend {
		return func(records string) backend.Backend { return &subjectBackend{spec: spec, records: records} }
	}
	b, spec, err := Prepare(context.Background(), t.TempDir(), newBackend(Spec{Record: Record{Identity: id}}))
	if err != nil || spec.Record.Identity != id {
		t.Fatalf("spec = %+v, err = %v", spec, err)
	}
	if _, err := b.Version(context.Background()); err != nil {
		t.Errorf("candidate not admitted through the temporary records: %v", err)
	}
	if _, err := (&subjectBackend{spec: spec}).Version(context.Background()); !errors.Is(err, ErrUnverified) {
		t.Errorf("candidate admitted without records: %v", err)
	}

	b, spec, err = Prepare(context.Background(), t.TempDir(), newBackend(Spec{Builtin: true, Record: Record{Identity: id}}))
	if err != nil || !spec.Builtin || b.(*subjectBackend).records != "" {
		t.Errorf("builtin: records = %q, spec = %+v, err = %v", b.(*subjectBackend).records, spec, err)
	}

	if _, _, err := Prepare(context.Background(), t.TempDir(), func(string) backend.Backend { return &shellBackend{} }); err == nil {
		t.Error("runtime without VerificationSpec accepted")
	}
}
