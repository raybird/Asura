package daemon

import (
	"sync"
)

// WorkspaceQueue 管理同專案目錄（Workspace）的互斥執行與序列排隊
// 遵循 CRON-001：同專案嚴格互斥依序執行，避免多任務並行寫入造成 git 或 workspace 衝突
type WorkspaceQueue struct {
	mu           sync.Mutex
	runningTasks map[string]string   // workspace -> running taskID
	queues       map[string][]string // workspace -> queued taskIDs
}

// NewWorkspaceQueue 建立新的 WorkspaceQueue 實例
func NewWorkspaceQueue() *WorkspaceQueue {
	return &WorkspaceQueue{
		runningTasks: make(map[string]string),
		queues:       make(map[string][]string),
	}
}

// TryAcquire 嘗試為指定任務獲取 workspace 的執行權
// 若當前無任務執行，回傳 (true, 0)
// 若已有任務正在執行，將此任務加入該 workspace 佇列，回傳 (false, queuePos)，queuePos >= 1
func (q *WorkspaceQueue) TryAcquire(workspace string, taskID string) (bool, int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	current, hasRunning := q.runningTasks[workspace]
	if !hasRunning || current == "" {
		q.runningTasks[workspace] = taskID
		return true, 0
	}

	// 已經有任務在執行，排入專案等待佇列
	q.queues[workspace] = append(q.queues[workspace], taskID)
	return false, len(q.queues[workspace])
}

// Release 釋放 workspace 的執行權，並檢查是否有等待中的下一個任務
// 若有等待中的任務，依 FIFO 彈出下一個任務並設定為 running，回傳 (nextTaskID, true)
// 若無等待中的任務，清空 runningTask，回傳 ("", false)
func (q *WorkspaceQueue) Release(workspace string, completedTaskID string) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	current := q.runningTasks[workspace]
	if current != completedTaskID && current != "" {
		return "", false
	}

	queue := q.queues[workspace]
	if len(queue) == 0 {
		delete(q.runningTasks, workspace)
		delete(q.queues, workspace)
		return "", false
	}

	nextTaskID := queue[0]
	if len(queue) == 1 {
		delete(q.queues, workspace)
	} else {
		q.queues[workspace] = queue[1:]
	}
	q.runningTasks[workspace] = nextTaskID
	return nextTaskID, true
}

// Remove 若任務在排隊期間被取消，將其自佇列中移除
func (q *WorkspaceQueue) Remove(workspace string, taskID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	queue, exists := q.queues[workspace]
	if !exists {
		return
	}

	filtered := make([]string, 0, len(queue))
	for _, id := range queue {
		if id != taskID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		delete(q.queues, workspace)
	} else {
		q.queues[workspace] = filtered
	}
}
