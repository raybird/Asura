package audit

import (
	"testing"
	"time"
)

func signed(e Event) Event {
	e.Key = EventKey(e)
	e.Seq = 1
	e.At = time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	e.PreviousDigest = Correlation("root")
	e.Digest = Digest(e)
	return e
}

// 2026-10-04：EXAUD-014/015，額外自由文字欄位不能躲進合法事件。
func TestAuditRejectForeignFields(t *testing.T) {
	for _, e := range []Event{
		{Kind: "terminal", State: "failed", Reason: "execution_failed", Layer: "task", Scope: "secret-sentinel"},
		{Kind: "terminal", State: "completed", Reason: "runtime_failed", Layer: "runtime"},
		{Kind: "task_started", State: "running", Reason: "none", RuntimeID: "secret-sentinel"},
	} {
		if signed(e).Validate() == nil {
			t.Fatalf("foreign fields accepted: %+v", e)
		}
	}
}
