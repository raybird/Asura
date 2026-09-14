package cron

import (
	"testing"
	"time"
)

func TestCronParser_ValidExpressions(t *testing.T) {
	loc := time.Local

	t.Run("每分鐘表達式 * * * * *", func(t *testing.T) {
		sched, err := Parse("* * * * *")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		base := time.Date(2026, 9, 13, 10, 30, 15, 0, loc)
		expected := time.Date(2026, 9, 13, 10, 31, 0, 0, loc)
		next := sched.Next(base)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})

	t.Run("間隔表達式 */5 * * * *", func(t *testing.T) {
		sched, err := Parse("*/5 * * * *")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		base := time.Date(2026, 9, 13, 10, 32, 0, 0, loc)
		expected := time.Date(2026, 9, 13, 10, 35, 0, 0, loc)
		next := sched.Next(base)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})

	t.Run("指定時間與平日 0 2 * * 1-5", func(t *testing.T) {
		sched, err := Parse("0 2 * * 1-5")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 2026-09-13 是週日 (Weekday 0)
		base := time.Date(2026, 9, 13, 10, 0, 0, 0, loc)
		// 下次應該是 2026-09-14 週一 02:00
		expected := time.Date(2026, 9, 14, 2, 0, 0, 0, loc)
		next := sched.Next(base)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})

	t.Run("列表表達式 30 8 1,15 * *", func(t *testing.T) {
		sched, err := Parse("30 8 1,15 * *")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		base := time.Date(2026, 9, 2, 9, 0, 0, 0, loc)
		expected := time.Date(2026, 9, 15, 8, 30, 0, 0, loc)
		next := sched.Next(base)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})

	t.Run("跨年計算 0 0 1 1 *", func(t *testing.T) {
		sched, err := Parse("0 0 1 1 *")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		base := time.Date(2026, 9, 13, 10, 0, 0, 0, loc)
		expected := time.Date(2027, 1, 1, 0, 0, 0, 0, loc)
		next := sched.Next(base)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})
}

func TestCronParser_InvalidExpressions(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"4 欄位不足", "* * * *"},
		{"6 欄位過多", "* * * * * *"},
		{"空字串", ""},
		{"分鐘超過 59", "60 * * * *"},
		{"分鐘小於 0", "-1 * * * *"},
		{"小時超過 23", "* 24 * * *"},
		{"日期超過 31", "* * 32 * *"},
		{"月份超過 12", "* * * 13 *"},
		{"月份為 0", "* * * 0 *"},
		{"星期超過 7", "* * * * 8"},
		{"非數字無效文字", "abc * * * *"},
		{"無效步進語法", "*/abc * * * *"},
		{"反向範圍", "5-2 * * * *"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.expr)
			if err == nil {
				t.Errorf("expected error for %s (%s), got nil", tt.name, tt.expr)
			}
		})
	}
}

func TestCronParser_UnreachableDate(t *testing.T) {
	sched, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatalf("unexpected error parsing 0 0 31 2 *: %v", err)
	}
	next := sched.Next(time.Now())
	if !next.IsZero() {
		t.Errorf("expected zero time for unreachable Feb 31, got %v", next)
	}
	if sched.IsReachable(time.Now()) {
		t.Errorf("expected IsReachable to return false for Feb 31")
	}
}

func TestCronParser_DaylightSavingTime(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("skipping DST test: America/New_York not available: %v", err)
	}

	t.Run("2027-03-14 DST transition 0 9 * * *", func(t *testing.T) {
		sched, err := Parse("0 9 * * *")
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		base := time.Date(2027, 3, 13, 10, 0, 0, 0, loc)
		done := make(chan time.Time, 1)
		go func() {
			done <- sched.Next(base)
		}()

		select {
		case next := <-done:
			expected := time.Date(2027, 3, 14, 9, 0, 0, 0, loc)
			if !next.Equal(expected) {
				t.Errorf("expected %v, got %v", expected, next)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timeout: Schedule.Next infinite loop on DST transition")
		}
	})

	t.Run("2027-03-14 DST transition 30 2 * * * (missing hour)", func(t *testing.T) {
		sched, err := Parse("30 2 * * *")
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		base := time.Date(2027, 3, 14, 1, 0, 0, 0, loc)
		done := make(chan time.Time, 1)
		go func() {
			done <- sched.Next(base)
		}()

		select {
		case next := <-done:
			// 2:30 在 3/14 當天不存在，下次觸發為 3/15 02:30
			expected := time.Date(2027, 3, 15, 2, 30, 0, 0, loc)
			if !next.Equal(expected) {
				t.Errorf("expected %v, got %v", expected, next)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timeout: Schedule.Next infinite loop on missing hour")
		}
	})

	t.Run("America/Santiago DST transition", func(t *testing.T) {
		santiagoLoc, err := time.LoadLocation("America/Santiago")
		if err != nil {
			t.Skipf("skipping Santiago test: %v", err)
		}
		sched, err := Parse("0 3 * * *")
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		base := time.Date(2026, 9, 5, 22, 0, 0, 0, santiagoLoc)
		done := make(chan time.Time, 1)
		go func() {
			done <- sched.Next(base)
		}()

		select {
		case next := <-done:
			if next.IsZero() {
				t.Fatalf("expected non-zero next time")
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timeout on Santiago DST transition")
		}
	})

}

func TestCronParser_LeapYearLongWindow(t *testing.T) {
	sched, err := Parse("0 0 29 2 1")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	base := time.Date(2026, 9, 14, 0, 0, 0, 0, time.Local)
	if !sched.IsReachable(base) {
		t.Errorf("expected 0 0 29 2 1 to be reachable within extended window")
	}
	next := sched.Next(base)
	if next.IsZero() {
		t.Fatalf("expected next to be found, got zero")
	}
	if next.Year() != 2044 || next.Month() != 2 || next.Day() != 29 || next.Weekday() != time.Monday {
		t.Errorf("expected 2044-02-29 Monday, got %v", next)
	}
}

