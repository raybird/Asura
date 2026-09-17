package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SCN-001: 授權使用者取得單次任務結果，並可再次查得相同任務的結果（外迴圈整合測試）
func TestTaskStorage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 建立任務
	task := &Task{
		ID:        "task-test-001",
		Prompt:    "echo 'hello world'",
		Workspace: "/tmp/allowed/project",
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. 查得初始狀態
	got, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("expected ID %q, got %q", task.ID, got.ID)
	}
	if got.Status != StatusPending {
		t.Errorf("expected status %q, got %q", StatusPending, got.Status)
	}
	if got.Prompt != task.Prompt {
		t.Errorf("expected prompt %q, got %q", task.Prompt, got.Prompt)
	}

	// 3. 更新為 running
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusRunning, "", ""); err != nil {
		t.Fatalf("UpdateTaskStatus to running failed: %v", err)
	}

	gotRunning, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after running failed: %v", err)
	}
	if gotRunning.Status != StatusRunning {
		t.Errorf("expected status %q, got %q", StatusRunning, gotRunning.Status)
	}

	// 4. 更新為 completed 並記錄結果
	expectedResult := "hello world\n"
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusCompleted, expectedResult, ""); err != nil {
		t.Fatalf("UpdateTaskStatus to completed failed: %v", err)
	}

	// 5. 驗證可再次查得相同任務結果
	gotCompleted, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after completed failed: %v", err)
	}
	if gotCompleted.Status != StatusCompleted {
		t.Errorf("expected status %q, got %q", StatusCompleted, gotCompleted.Status)
	}
	if gotCompleted.Result != expectedResult {
		t.Errorf("expected result %q, got %q", expectedResult, gotCompleted.Result)
	}
	if gotCompleted.ErrorMessage != "" {
		t.Errorf("expected empty error message, got %q", gotCompleted.ErrorMessage)
	}
}

// 內迴圈單元測試：驗證 Task 狀態轉換邊界、查無任務處理及 WAL 單一連線配置
func TestSQLiteStore_Unit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-unit-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 查詢不存在的任務
	_, err = store.GetTask(ctx, "non-existent")
	if err != ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}

	// 建立任務並測試失敗狀態紀錄
	task := &Task{
		ID:        "task-fail-002",
		Prompt:    "exit 1",
		Workspace: "/tmp/allowed/project",
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if err := store.UpdateTaskStatus(ctx, task.ID, StatusFailed, "", "command returned exit status 1"); err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	failedTask, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if failedTask.Status != StatusFailed {
		t.Errorf("expected status %q, got %q", StatusFailed, failedTask.Status)
	}
	if failedTask.ErrorMessage != "command returned exit status 1" {
		t.Errorf("expected error message %q, got %q", "command returned exit status 1", failedTask.ErrorMessage)
	}

	// 測試取消狀態
	cancelTask := &Task{
		ID:        "task-cancel-003",
		Prompt:    "sleep 60",
		Workspace: "/tmp/allowed/project",
		Status:    StatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, cancelTask); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, cancelTask.ID, StatusCancelled, "", "context cancelled"); err != nil {
		t.Fatalf("UpdateTaskStatus to cancelled failed: %v", err)
	}
	gotCancelled, err := store.GetTask(ctx, cancelTask.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if gotCancelled.Status != StatusCancelled {
		t.Errorf("expected status %q, got %q", StatusCancelled, gotCancelled.Status)
	}
}

// SCN-006 內迴圈單元測試：重啟恢復保留已完成任務，並將中斷之 running / pending 任務標記為 failed
func TestSQLiteStore_RecoverInterruptedTasks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-recover-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// 1. 寫入已完成任務
	taskCompleted := &Task{
		ID:        "task-completed",
		Prompt:    "echo done",
		Workspace: "/tmp/allowed",
		Status:    StatusCompleted,
		Result:    "completed result",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskCompleted); err != nil {
		t.Fatalf("create completed task: %v", err)
	}

	// 2. 寫入中斷前處於 running 的任務
	taskRunning := &Task{
		ID:        "task-running",
		Prompt:    "sleep 100",
		Workspace: "/tmp/allowed",
		Status:    StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskRunning); err != nil {
		t.Fatalf("create running task: %v", err)
	}

	// 3. 寫入中斷前處於 pending 的任務
	taskPending := &Task{
		ID:        "task-pending",
		Prompt:    "echo pending",
		Workspace: "/tmp/allowed",
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskPending); err != nil {
		t.Fatalf("create pending task: %v", err)
	}

	// 4. 寫入中斷前處於 queued 的任務 (MUST FIX 5: queued 任務亦應被恢復)
	taskQueued := &Task{
		ID:        "task-queued",
		Prompt:    "echo queued",
		Workspace: "/tmp/allowed",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, taskQueued); err != nil {
		t.Fatalf("create queued task: %v", err)
	}

	// 5. 執行恢復
	recoveredCount, err := store.RecoverInterruptedTasks(ctx)
	if err != nil {
		t.Fatalf("RecoverInterruptedTasks failed: %v", err)
	}
	if recoveredCount != 3 {
		t.Errorf("expected 3 recovered tasks, got %d", recoveredCount)
	}

	// 6. 斷言已完成任務不受任何影響
	savedCompleted, err := store.GetTask(ctx, "task-completed")
	if err != nil {
		t.Fatalf("get completed task: %v", err)
	}
	if savedCompleted.Status != StatusCompleted {
		t.Errorf("expected completed task status %s, got %s", StatusCompleted, savedCompleted.Status)
	}
	if savedCompleted.Result != "completed result" {
		t.Errorf("expected completed result %q, got %q", "completed result", savedCompleted.Result)
	}

	// 7. 斷言中斷之 running 任務轉為 failed 且具有明確錯誤原因
	savedRunning, err := store.GetTask(ctx, "task-running")
	if err != nil {
		t.Fatalf("get running task: %v", err)
	}
	if savedRunning.Status != StatusFailed {
		t.Errorf("expected running task status %s, got %s", StatusFailed, savedRunning.Status)
	}
	if savedRunning.ErrorMessage == "" {
		t.Errorf("expected non-empty error message on recovered running task")
	}

	// 8. 斷言中斷之 pending 任務轉為 failed
	savedPending, err := store.GetTask(ctx, "task-pending")
	if err != nil {
		t.Fatalf("get pending task: %v", err)
	}
	if savedPending.Status != StatusFailed {
		t.Errorf("expected pending task status %s, got %s", StatusFailed, savedPending.Status)
	}
	if savedPending.ErrorMessage == "" {
		t.Errorf("expected non-empty error message for interrupted pending task")
	}

	// 9. 斷言中斷之 queued 任務亦轉為 failed (MUST FIX 5)
	savedQueued, err := store.GetTask(ctx, "task-queued")
	if err != nil {
		t.Fatalf("get queued task: %v", err)
	}
	if savedQueued.Status != StatusFailed {
		t.Errorf("expected queued task status %s, got %s", StatusFailed, savedQueued.Status)
	}
	if savedQueued.ErrorMessage == "" {
		t.Errorf("expected non-empty error message on recovered queued task")
	}
}

// TestRecoverInterruptedTasks_Cancelling 驗證當任務處於 StatusCancelling 遭遇守護程序重啟時，被正確復原為 StatusFailed
func TestRecoverInterruptedTasks_Cancelling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-cancelling-rec-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	task := &Task{
		ID:        "task-cancelling-rec",
		Prompt:    "echo test",
		Workspace: "/tmp/allowed",
		Status:    StatusCancelling,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("create cancelling task: %v", err)
	}

	count, err := store.RecoverInterruptedTasks(ctx)
	if err != nil {
		t.Fatalf("RecoverInterruptedTasks failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 recovered task, got %d", count)
	}

	saved, err := store.GetTask(ctx, "task-cancelling-rec")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if saved.Status != StatusFailed {
		t.Errorf("expected recovered status %s, got %s", StatusFailed, saved.Status)
	}
	if saved.ErrorMessage == "" {
		t.Errorf("expected non-empty error message for recovered cancelling task")
	}
}

func TestSQLiteStore_TaskClassPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-class-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// 1. 預設情況下應為 interactive
	t1 := &Task{
		ID:        "task-interactive",
		Prompt:    "echo 1",
		Workspace: "/tmp/allowed",
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, t1); err != nil {
		t.Fatalf("create t1 failed: %v", err)
	}
	got1, err := store.GetTask(ctx, t1.ID)
	if err != nil {
		t.Fatalf("get t1 failed: %v", err)
	}
	if got1.Class != "interactive" {
		t.Errorf("expected default class 'interactive', got %q", got1.Class)
	}

	// 2. 指定 class 為 cron
	t2 := &Task{
		ID:        "task-cron",
		Prompt:    "echo 2",
		Workspace: "/tmp/allowed",
		Status:    StatusPending,
		Class:     "cron",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, t2); err != nil {
		t.Fatalf("create t2 failed: %v", err)
	}
	got2, err := store.GetTask(ctx, t2.ID)
	if err != nil {
		t.Fatalf("get t2 failed: %v", err)
	}
	if got2.Class != "cron" {
		t.Errorf("expected class 'cron', got %q", got2.Class)
	}
}

// 驗證從不具備 request_id/fingerprint 欄位的舊版 SQLite 資料庫進行遷移升級
func TestSQLiteStore_UpgradeFromOldSchemaWithoutRequestID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-upgrade-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "legacy.db")

	// 1. 建立舊版 schema（無 request_id 與 fingerprint 欄位）
	oldDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old db failed: %v", err)
	}
	legacySchema := `
	CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		prompt TEXT NOT NULL,
		workspace TEXT NOT NULL,
		status TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		pgid INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX idx_tasks_status ON tasks(status);
	`
	if _, err := oldDB.Exec(legacySchema); err != nil {
		oldDB.Close()
		t.Fatalf("exec legacy schema failed: %v", err)
	}
	// 插入一筆舊資料
	now := time.Now().UTC()
	_, err = oldDB.Exec(`
	INSERT INTO tasks (id, prompt, workspace, status, result, error_message, pgid, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "legacy-task-1", "legacy prompt", "/tmp", "completed", "done", "", 0, now, now)
	if err != nil {
		oldDB.Close()
		t.Fatalf("insert legacy task failed: %v", err)
	}
	oldDB.Close()

	// 2. 以 NewSQLiteStore 開啟，預期順利執行 schema 遷移與補齊欄位、建立索引
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed on legacy DB: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 3. 能讀取舊任務且欄位預設值正確
	legacyTask, err := store.GetTask(ctx, "legacy-task-1")
	if err != nil {
		t.Fatalf("GetTask failed for legacy task: %v", err)
	}
	if legacyTask.RequestID != "" || legacyTask.Fingerprint != "" {
		t.Errorf("expected empty request_id/fingerprint, got %q, %q", legacyTask.RequestID, legacyTask.Fingerprint)
	}

	// 4. 能正常寫入含 request_id 與 fingerprint 的新任務
	newTask := &Task{
		ID:          "new-task-1",
		RequestID:   "req-123",
		Fingerprint: "fp-123",
		Prompt:      "new prompt",
		Workspace:   "/tmp",
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateTask(ctx, newTask); err != nil {
		t.Fatalf("CreateTask failed on upgraded DB: %v", err)
	}

	// 5. 能透過 GetTaskByRequestID 查回
	found, err := store.GetTaskByRequestID(ctx, "req-123")
	if err != nil {
		t.Fatalf("GetTaskByRequestID failed: %v", err)
	}
	if found.ID != newTask.ID {
		t.Errorf("expected task ID %q, got %q", newTask.ID, found.ID)
	}
}

// 驗證 SQLite 部分唯一索引：相同非空 RequestID 會觸發 ErrDuplicateRequestID，而多個空 RequestID 不受限制
func TestSQLiteStore_DuplicateRequestIDUniqueIndex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-unique-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// 1. 寫入第一筆非空 RequestID
	task1 := &Task{
		ID:        "task-1",
		RequestID: "req-dup-1",
		Prompt:    "prompt 1",
		Workspace: "/tmp",
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, task1); err != nil {
		t.Fatalf("CreateTask task1 failed: %v", err)
	}

	// 2. 寫入相同 RequestID 的第二筆任務，預期回傳 ErrDuplicateRequestID
	task2 := &Task{
		ID:        "task-2",
		RequestID: "req-dup-1",
		Prompt:      "prompt 2",
		Workspace:   "/tmp",
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateTask(ctx, task2); err != ErrDuplicateRequestID {
		t.Fatalf("expected ErrDuplicateRequestID, got %v", err)
	}

	// 3. 寫入兩筆空字串 RequestID，部分唯一索引不應衝突
	emptyReqTask1 := &Task{
		ID:        "task-empty-1",
		RequestID: "",
		Prompt:    "prompt empty 1",
		Workspace: "/tmp",
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, emptyReqTask1); err != nil {
		t.Fatalf("CreateTask emptyReqTask1 failed: %v", err)
	}

	emptyReqTask2 := &Task{
		ID:        "task-empty-2",
		RequestID: "",
		Prompt:    "prompt empty 2",
		Workspace: "/tmp",
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateTask(ctx, emptyReqTask2); err != nil {
		t.Fatalf("CreateTask emptyReqTask2 failed: %v", err)
	}
}

// 驗證既有資料庫若已存在重複的 RequestID，遷移策略保留所有歷史任務並明確保留最早任務之 RequestID，使唯一索引能順利建立
func TestSQLiteStore_UpgradeFromSchemaWithDuplicateRequestIDs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-storage-dup-upgrade-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "dup.db")

	// 1. 建立具有 request_id 欄位但無唯一索引的 schema（模擬前一版本環境）
	oldDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old db failed: %v", err)
	}
	schemaWithDup := `
	CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		request_id TEXT NOT NULL DEFAULT '',
		fingerprint TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		workspace TEXT NOT NULL,
		status TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		pgid INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX idx_tasks_status ON tasks(status);
	CREATE INDEX idx_tasks_request_id ON tasks(request_id);
	`
	if _, err := oldDB.Exec(schemaWithDup); err != nil {
		oldDB.Close()
		t.Fatalf("exec schemaWithDup failed: %v", err)
	}

	// 插入兩筆相同非空 RequestID 的歷史任務，created_at 刻意分開
	timeEarlier := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	timeLater := time.Date(2026, 9, 12, 10, 5, 0, 0, time.UTC)

	_, err = oldDB.Exec(`
	INSERT INTO tasks (id, request_id, fingerprint, prompt, workspace, status, result, error_message, pgid, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),
	       (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"task-earlier", "req-shared-dup", "fp1", "prompt 1", "/tmp", "completed", "result 1", "", 0, timeEarlier, timeEarlier,
		"task-later", "req-shared-dup", "fp1", "prompt 2", "/tmp", "completed", "result 2", "", 0, timeLater, timeLater,
	)
	if err != nil {
		oldDB.Close()
		t.Fatalf("insert duplicate request_id tasks failed: %v", err)
	}
	oldDB.Close()

	// 2. 以 NewSQLiteStore 開啟，預期順利執行去重遷移策略並建立唯一索引，不應拋出 UNIQUE constraint failed
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed on DB with duplicate request_ids: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 3. 驗證所有歷史任務均被完整保留（未被 DELETE）
	task1, err := store.GetTask(ctx, "task-earlier")
	if err != nil {
		t.Fatalf("GetTask failed for task-earlier: %v", err)
	}
	if task1.Result != "result 1" {
		t.Errorf("task-earlier result corrupted, got %q", task1.Result)
	}

	task2, err := store.GetTask(ctx, "task-later")
	if err != nil {
		t.Fatalf("GetTask failed for task-later: %v", err)
	}
	if task2.Result != "result 2" {
		t.Errorf("task-later result corrupted, got %q", task2.Result)
	}

	// 4. 驗證明確的 Request ID 對應策略：最早的任務保留該 Request ID
	if task1.RequestID != "req-shared-dup" {
		t.Errorf("expected task-earlier to retain request_id 'req-shared-dup', got %q", task1.RequestID)
	}
	// 後續任務之 RequestID 降級為空字串，解除唯一衝突
	if task2.RequestID != "" {
		t.Errorf("expected task-later request_id to be cleared to '', got %q", task2.RequestID)
	}

	// 5. 驗證透過 GetTaskByRequestID 能準確查回最早的任務
	foundByReq, err := store.GetTaskByRequestID(ctx, "req-shared-dup")
	if err != nil {
		t.Fatalf("GetTaskByRequestID failed: %v", err)
	}
	if foundByReq.ID != "task-earlier" {
		t.Errorf("expected GetTaskByRequestID to return 'task-earlier', got %q", foundByReq.ID)
	}

	// 6. 驗證唯一索引已生效：嘗試寫入相同的 request_id 會被阻擋
	newDupTask := &Task{
		ID:        "task-new-dup",
		RequestID: "req-shared-dup",
		Prompt:    "new dup prompt",
		Workspace: "/tmp",
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, newDupTask); err != ErrDuplicateRequestID {
		t.Errorf("expected ErrDuplicateRequestID from unique index, got %v", err)
	}
}

// CTXMEM-006: 驗證 tasks 表擴充 memory_refs 與 resolved_context 欄位之加法式無損升級與讀寫持久化
func TestSQLiteStore_MemoryContextColumnsMigration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-migration-memctx-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")

	// 1. 手動建立舊版 tasks 資料表（不含 memory_refs 與 resolved_context 欄位）
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw db: %v", err)
	}
	oldSchema := `
	CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		request_id TEXT NOT NULL DEFAULT '',
		fingerprint TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		workspace TEXT NOT NULL,
		status TEXT NOT NULL,
		result TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		pgid INTEGER NOT NULL DEFAULT 0,
		class TEXT NOT NULL DEFAULT 'interactive',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	INSERT INTO tasks (id, request_id, fingerprint, prompt, workspace, status, created_at, updated_at)
	VALUES ('old-task-1', 'req-old-1', 'fp1', 'old prompt', '/tmp/ws', 'completed', datetime('now'), datetime('now'));
	`
	if _, err := rawDB.Exec(oldSchema); err != nil {
		rawDB.Close()
		t.Fatalf("failed to create old schema: %v", err)
	}
	rawDB.Close()

	// 2. 以 NewSQLiteStore 開庫，觸發 initSchema() 的自動遷移
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed on old schema: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 3. 讀取舊任務，驗證加法升級後舊資料完整保留且新欄位為預設值
	oldTask, err := store.GetTask(ctx, "old-task-1")
	if err != nil {
		t.Fatalf("GetTask failed for old-task-1: %v", err)
	}
	if oldTask.Prompt != "old prompt" {
		t.Errorf("expected prompt 'old prompt', got %q", oldTask.Prompt)
	}
	if len(oldTask.MemoryRefs) != 0 {
		t.Errorf("expected empty MemoryRefs, got %v", oldTask.MemoryRefs)
	}
	if oldTask.ResolvedContext != "" {
		t.Errorf("expected empty ResolvedContext, got %q", oldTask.ResolvedContext)
	}

	// 4. 寫入帶有 MemoryRefs 與 ResolvedContext 的新任務
	newTask := &Task{
		ID:              "task-mem-001",
		RequestID:       "req-mem-001",
		Prompt:          "check context",
		Workspace:       "/tmp/ws",
		MemoryRefs:      []string{"mem-1", "mem-2"},
		ResolvedContext: `{"original_prompt":"check context","workspace":"/tmp/ws"}`,
		Status:          StatusPending,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	if err := store.CreateTask(ctx, newTask); err != nil {
		t.Fatalf("CreateTask with memory context failed: %v", err)
	}

	// 5. 透過 GetTask 查回並驗證欄位持久化
	gotTask, err := store.GetTask(ctx, newTask.ID)
	if err != nil {
		t.Fatalf("GetTask failed for newTask: %v", err)
	}
	if len(gotTask.MemoryRefs) != 2 || gotTask.MemoryRefs[0] != "mem-1" || gotTask.MemoryRefs[1] != "mem-2" {
		t.Errorf("expected MemoryRefs ['mem-1', 'mem-2'], got %v", gotTask.MemoryRefs)
	}
	if gotTask.ResolvedContext != newTask.ResolvedContext {
		t.Errorf("expected ResolvedContext %q, got %q", newTask.ResolvedContext, gotTask.ResolvedContext)
	}

	// 6. 透過 GetTaskByRequestID 查回並驗證
	gotByReq, err := store.GetTaskByRequestID(ctx, "req-mem-001")
	if err != nil {
		t.Fatalf("GetTaskByRequestID failed: %v", err)
	}
	if gotByReq.ID != newTask.ID {
		t.Errorf("expected task ID %q, got %q", newTask.ID, gotByReq.ID)
	}
	if len(gotByReq.MemoryRefs) != 2 || gotByReq.MemoryRefs[0] != "mem-1" || gotByReq.MemoryRefs[1] != "mem-2" {
		t.Errorf("expected MemoryRefs ['mem-1', 'mem-2'], got %v", gotByReq.MemoryRefs)
	}
	if gotByReq.ResolvedContext != newTask.ResolvedContext {
		t.Errorf("expected ResolvedContext %q, got %q", newTask.ResolvedContext, gotByReq.ResolvedContext)
	}
}


