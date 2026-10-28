package verification

import (
	"context"
	"errors"
	"testing"
	"time"

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

// hangingBackend 的每次執行都卡到 context 結束，模擬沒有回應的 runtime。
type hangingBackend struct {
	subjectBackend
	runs int
}

func (b *hangingBackend) Run(ctx context.Context, _ *backend.ExecutionRequest) (*backend.ExecutionResult, error) {
	b.runs++
	<-ctx.Done()
	return nil, ctx.Err()
}

// 2026-10-22：issue-73 Task 3.2，內建身分不呼叫模型；runtime 沒有回應時每個 probe 都以逾時失敗，不會被當成通過。
func TestVerify(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	id := Identity{Runtime: "rt", Version: "2.0", Digest: "abc"}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	builtin := &hangingBackend{subjectBackend: subjectBackend{spec: Spec{Builtin: true, Record: Record{Identity: id}}}}
	spec, results, err := Verify(ctx, func(string, func(context.Context, int)) backend.Backend { return builtin })
	if err != nil || !spec.Builtin || len(results) != 0 || builtin.runs != 0 {
		t.Errorf("builtin: spec = %+v, results = %v, runs = %d, err = %v", spec, results, builtin.runs, err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	hang := &hangingBackend{subjectBackend: subjectBackend{spec: Spec{Record: Record{Identity: id}, Options: Options{ReadOnlyRoles: true}}}}
	_, results, err = Verify(ctx, func(records string, _ func(context.Context, int)) backend.Backend {
		hang.records = records
		return hang
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := Summary(results)
	if ok || len(results) != 4 {
		t.Fatalf("hanging runtime passed:\n%s", summary)
	}
	for _, r := range results {
		if r.Err == nil {
			t.Errorf("probe %s passed against a hanging runtime", r.Name)
		}
	}
}
