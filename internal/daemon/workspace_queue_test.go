package daemon

import (
	"sync"
	"testing"
)

func TestWorkspaceQueue_ExclusiveAccess(t *testing.T) {
	t.Run("同專案單一任務直接取得執行權", func(t *testing.T) {
		wq := NewWorkspaceQueue()
		ws := "/path/to/project-a"

		acquired, pos := wq.TryAcquire(ws, "task-1")
		if !acquired {
			t.Fatalf("expected acquired=true for idle workspace, got false")
		}
		if pos != 0 {
			t.Fatalf("expected pos=0, got %d", pos)
		}

		// 任務完成釋放
		next, hasNext := wq.Release(ws, "task-1")
		if hasNext || next != "" {
			t.Fatalf("expected no next task, got %q", next)
		}
	})

	t.Run("同專案第二個任務排入佇列並依序喚醒", func(t *testing.T) {
		wq := NewWorkspaceQueue()
		ws := "/path/to/project-a"

		// 任務 1 取得執行權
		acq1, _ := wq.TryAcquire(ws, "task-1")
		if !acq1 {
			t.Fatalf("task 1 should acquire lock")
		}

		// 任務 2 排隊
		acq2, pos2 := wq.TryAcquire(ws, "task-2")
		if acq2 {
			t.Fatalf("task 2 should not acquire lock while task 1 is running")
		}
		if pos2 != 1 {
			t.Fatalf("expected task 2 queue position 1, got %d", pos2)
		}

		// 任務 3 排隊
		acq3, pos3 := wq.TryAcquire(ws, "task-3")
		if acq3 {
			t.Fatalf("task 3 should not acquire lock")
		}
		if pos3 != 2 {
			t.Fatalf("expected task 3 queue position 2, got %d", pos3)
		}

		// 任務 1 完成釋放，下一個應是任務 2
		next1, has1 := wq.Release(ws, "task-1")
		if !has1 || next1 != "task-2" {
			t.Fatalf("expected next task-2, got %q (has=%v)", next1, has1)
		}

		// 任務 2 完成釋放，下一個應是任務 3
		next2, has2 := wq.Release(ws, "task-2")
		if !has2 || next2 != "task-3" {
			t.Fatalf("expected next task-3, got %q (has=%v)", next2, has2)
		}

		// 任務 3 完成釋放，隊列清空
		next3, has3 := wq.Release(ws, "task-3")
		if has3 || next3 != "" {
			t.Fatalf("expected queue to be empty, got %q", next3)
		}
	})

	t.Run("不同專案各自獨立互不阻塞", func(t *testing.T) {
		wq := NewWorkspaceQueue()
		wsA := "/path/to/project-a"
		wsB := "/path/to/project-b"

		acqA, _ := wq.TryAcquire(wsA, "task-a")
		acqB, _ := wq.TryAcquire(wsB, "task-b")

		if !acqA || !acqB {
			t.Fatalf("tasks in different workspaces should both acquire immediately (acqA=%v, acqB=%v)", acqA, acqB)
		}

		wq.Release(wsA, "task-a")
		wq.Release(wsB, "task-b")
	})

	t.Run("並發競爭安全性 (Race Condition Check)", func(t *testing.T) {
		wq := NewWorkspaceQueue()
		ws := "/path/to/project-race"

		var wg sync.WaitGroup
		acquiredCount := 0
		var mu sync.Mutex

		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				taskID := "task-" + string(rune('a'+id))
				acq, _ := wq.TryAcquire(ws, taskID)
				if acq {
					mu.Lock()
					acquiredCount++
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()

		// 任何瞬間同專案只能有 1 個任務成功 Acquire
		if acquiredCount != 1 {
			t.Fatalf("expected exactly 1 task to acquire initial lock, got %d", acquiredCount)
		}
	})
}
