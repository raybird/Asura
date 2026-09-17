package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"asura/internal/storage"
	_ "modernc.org/sqlite"
)

// P2-T1 固定多語文字真相來源 (Single Source of Truth)
const (
	testMemContentA1 = "專案A記憶1：核心設定方針 (Alpha Policy 1 - 2026-09-14)"
	testMemContentA2 = "專案A記憶2：模組重構要點 (Alpha Refactoring 2 - 2026-09-14)"
	testMemContentB1 = "專案B記憶1：機密金鑰格式 (Beta Secret 1 - 2026-09-14)"
)

// TestSQLiteStore_MemoryCRUD 驗證專案記憶的加法式寫入、scope 限定讀取、指定刪除與重啟持久化 (MEM-001, MEM-002, MEM-005, P2-T1.a)
func TestSQLiteStore_MemoryCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura_memory.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	projA := "/workspace/projectA"
	projB := "/workspace/projectB"

	now := time.Now().UTC().Truncate(time.Millisecond)

	memA1 := storage.Memory{
		ID:        "mem-a-1",
		Workspace: projA,
		Content:   testMemContentA1,
		Source:    "manual",
		CreatedAt: now,
	}

	memA2 := storage.Memory{
		ID:        "mem-a-2",
		Workspace: projA,
		Content:   testMemContentA2,
		Source:    "manual",
		CreatedAt: now.Add(time.Second),
	}

	memB1 := storage.Memory{
		ID:        "mem-b-1",
		Workspace: projB,
		Content:   testMemContentB1,
		Source:    "manual",
		CreatedAt: now.Add(2 * time.Second),
	}

	// 1. 寫入 A1, A2 與 B1
	if err := store.CreateMemory(ctx, memA1); err != nil {
		t.Fatalf("內迴圈單元紅燈：CreateMemory(A1) 失敗：%v (待 P2-T1 實作)", err)
	}
	if err := store.CreateMemory(ctx, memA2); err != nil {
		t.Fatalf("CreateMemory(A2) 失敗：%v", err)
	}
	if err := store.CreateMemory(ctx, memB1); err != nil {
		t.Fatalf("CreateMemory(B1) 失敗：%v", err)
	}

	// 2. 正向查詢：A 依 ID 取得 A1，驗證欄位完整性 (ID, Workspace, Content, Source, CreatedAt)
	gotA1, err := store.GetMemory(ctx, projA, memA1.ID)
	if err != nil {
		t.Fatalf("GetMemory(projA, A1) 失敗：%v", err)
	}
	if gotA1.ID != memA1.ID || gotA1.Workspace != projA || gotA1.Content != testMemContentA1 || gotA1.Source != "manual" {
		t.Fatalf("A1 欄位內容不符：got %+v, expected %+v", gotA1, memA1)
	}

	// 3. 跨專案查詢隔離 (MEM-002)：在 A scope 查詢 B1 應失敗回傳 ErrMemoryNotFound
	if _, err := store.GetMemory(ctx, projA, memB1.ID); !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Fatalf("跨專案隔離漏洞：在 projA 查詢 B1 應回傳 ErrMemoryNotFound，實際得到: %v", err)
	}

	// 4. 清單隔離 (MEM-002)：projA 清單應恰為 2 筆，且不包含 B1
	listA, err := store.ListMemories(ctx, projA)
	if err != nil {
		t.Fatalf("ListMemories(projA) 失敗：%v", err)
	}
	if len(listA) != 2 {
		t.Fatalf("projA 筆數不符：expected 2, got %d", len(listA))
	}
	for _, m := range listA {
		if m.ID == memB1.ID || m.Content == testMemContentB1 {
			t.Fatalf("跨專案清單洩漏：projA 清單包含 B1 資料")
		}
	}

	// projB 清單應恰為 1 筆
	listB, err := store.ListMemories(ctx, projB)
	if err != nil {
		t.Fatalf("ListMemories(projB) 失敗：%v", err)
	}
	if len(listB) != 1 || listB[0].ID != memB1.ID {
		t.Fatalf("projB 清單不符：got %+v", listB)
	}

	// 5. 跨專案刪除拒絕 (MEM-002)：嘗試在 projB 刪除 A1 應失敗，且 A1 依然存在
	if err := store.DeleteMemory(ctx, projB, memA1.ID); !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Fatalf("跨專案刪除漏洞：在 projB 刪除 A1 應回傳 ErrMemoryNotFound，實際得到: %v", err)
	}
	if _, err := store.GetMemory(ctx, projA, memA1.ID); err != nil {
		t.Fatalf("A1 遭跨專案刪除誤傷：%v", err)
	}

	// 6. 指定刪除與保持不變 (MEM-005)：在 projA 刪除 A1
	if err := store.DeleteMemory(ctx, projA, memA1.ID); err != nil {
		t.Fatalf("DeleteMemory(projA, A1) 失敗：%v", err)
	}
	if _, err := store.GetMemory(ctx, projA, memA1.ID); !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Fatalf("A1 刪除後仍能查得：%v", err)
	}
	// 另一筆 A2 內容與時間保持不變
	gotA2, err := store.GetMemory(ctx, projA, memA2.ID)
	if err != nil {
		t.Fatalf("A2 遭誤傷：%v", err)
	}
	if gotA2.Content != testMemContentA2 {
		t.Fatalf("A2 內容改變：expected %q, got %q", testMemContentA2, gotA2.Content)
	}

	// 7. 重啟持久化驗證 (MEM-001, MEM-005)：關閉 store 並重新開庫
	if err := store.Close(); err != nil {
		t.Fatalf("close store failed: %v", err)
	}

	reopenedStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store failed: %v", err)
	}
	defer reopenedStore.Close()

	// 已刪除的 A1 不應恢復
	if _, err := reopenedStore.GetMemory(ctx, projA, memA1.ID); !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Fatalf("重啟後已刪除記憶復活：%v", err)
	}
	// 未刪除的 A2 與 B1 完整保留
	reA2, err := reopenedStore.GetMemory(ctx, projA, memA2.ID)
	if err != nil || reA2.Content != testMemContentA2 {
		t.Fatalf("重啟後 A2 遺失或內容改變：%v, %+v", err, reA2)
	}
	reB1, err := reopenedStore.GetMemory(ctx, projB, memB1.ID)
	if err != nil || reB1.Content != testMemContentB1 {
		t.Fatalf("重啟後 B1 遺失或內容改變：%v, %+v", err, reB1)
	}
}

// TestSQLiteStore_MemorySchemaUpgrade 驗證從無記憶表的舊版資料庫升級時，Task/Cron 歷史無損且記憶功能正常 (P2-T1.c)
func TestSQLiteStore_MemorySchemaUpgrade(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_schema.db")

	// 1. 建立真實舊版 schema（僅含 tasks, cron_schedules, cron_runs，完全無 memories 表）
	oldDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db failed: %v", err)
	}

	legacyDDL := `
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
	CREATE INDEX idx_tasks_status ON tasks(status);

	CREATE TABLE cron_schedules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		workspace TEXT NOT NULL,
		schedule TEXT NOT NULL,
		prompt TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		last_run_at DATETIME,
		next_run_at DATETIME,
		pause_reason TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE cron_runs (
		id TEXT PRIMARY KEY,
		cron_id TEXT NOT NULL,
		scheduled_time DATETIME NOT NULL,
		triggered_time DATETIME NOT NULL,
		task_id TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		FOREIGN KEY (cron_id) REFERENCES cron_schedules(id) ON DELETE CASCADE
	);
	`
	if _, err := oldDB.Exec(legacyDDL); err != nil {
		oldDB.Close()
		t.Fatalf("exec legacy DDL failed: %v", err)
	}

	// 插入既有任務與排程歷史資料
	now := time.Now().UTC()
	_, err = oldDB.Exec(`
		INSERT INTO tasks (id, request_id, fingerprint, prompt, workspace, status, result, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "legacy-t1", "req-legacy-1", "fp-legacy-1", "歷史任務", "/workspace/legacy", "completed", "done", now, now)
	if err != nil {
		oldDB.Close()
		t.Fatalf("insert legacy task failed: %v", err)
	}

	_, err = oldDB.Exec(`
		INSERT INTO cron_schedules (id, name, workspace, schedule, prompt, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "legacy-cron-1", "歷史排程", "/workspace/legacy", "@hourly", "定時任務", "active", now, now)
	if err != nil {
		oldDB.Close()
		t.Fatalf("insert legacy cron failed: %v", err)
	}

	oldDB.Close()

	// 2. 以 NewSQLiteStore 開啟舊版資料庫，觸發加法式 schema 升級
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore 升級失敗：%v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 3. 獨立讀取並驗證既有 Task / Cron 歷史完整無損
	task, err := store.GetTask(ctx, "legacy-t1")
	if err != nil {
		t.Fatalf("升級後讀取歷史 task 失敗：%v", err)
	}
	if task.Prompt != "歷史任務" || task.Result != "done" {
		t.Fatalf("歷史 task 內容毀損：got %+v", task)
	}

	sched, err := store.GetCronSchedule(ctx, "legacy-cron-1")
	if err != nil {
		t.Fatalf("升級後讀取歷史 cron 失敗：%v", err)
	}
	if sched.Name != "歷史排程" || sched.Schedule != "@hourly" {
		t.Fatalf("歷史 cron 內容毀損：got %+v", sched)
	}

	// 4. 驗證升級後資料庫可正常寫入與讀取記憶
	mem := storage.Memory{
		ID:        "migrated-mem-1",
		Workspace: "/workspace/legacy",
		Content:   "升級後新增記憶",
		Source:    "manual",
		CreatedAt: now,
	}
	if err := store.CreateMemory(ctx, mem); err != nil {
		t.Fatalf("升級後 CreateMemory 失敗：%v", err)
	}
	gotMem, err := store.GetMemory(ctx, "/workspace/legacy", mem.ID)
	if err != nil {
		t.Fatalf("升級後 GetMemory 失敗：%v", err)
	}
	if gotMem.Content != "升級後新增記憶" {
		t.Fatalf("升級後記憶內容不符：got %q", gotMem.Content)
	}
}

// CTXMEM-002, CTXMEM-005: 驗證 ResolveMemoriesForTask 同專案成功解析、跨專案拒絕、不存在與已刪除記憶拒絕
func TestResolveMemoriesForTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "asura_resolve_mem.db")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	projA := "/workspace/projectA"
	projB := "/workspace/projectB"

	now := time.Now().UTC().Truncate(time.Millisecond)

	memA1 := storage.Memory{
		ID:        "mem-a-1",
		Workspace: projA,
		Content:   testMemContentA1,
		Source:    "manual",
		CreatedAt: now,
	}
	memA2 := storage.Memory{
		ID:        "mem-a-2",
		Workspace: projA,
		Content:   testMemContentA2,
		Source:    "manual",
		CreatedAt: now.Add(time.Second),
	}
	memB1 := storage.Memory{
		ID:        "mem-b-1",
		Workspace: projB,
		Content:   testMemContentB1,
		Source:    "manual",
		CreatedAt: now.Add(2 * time.Second),
	}

	if err := store.CreateMemory(ctx, memA1); err != nil {
		t.Fatalf("CreateMemory(memA1) failed: %v", err)
	}
	if err := store.CreateMemory(ctx, memA2); err != nil {
		t.Fatalf("CreateMemory(memA2) failed: %v", err)
	}
	if err := store.CreateMemory(ctx, memB1); err != nil {
		t.Fatalf("CreateMemory(memB1) failed: %v", err)
	}

	// 1. 空清單：回傳空切片且無錯誤
	emptyMems, err := store.ResolveMemoriesForTask(ctx, projA, []string{})
	if err != nil {
		t.Fatalf("ResolveMemoriesForTask(empty) unexpected error: %v", err)
	}
	if len(emptyMems) != 0 {
		t.Errorf("expected 0 memories for empty input, got %d", len(emptyMems))
	}

	// 2. 同專案合法解析（單一與多筆，維持請求順序）
	resolved, err := store.ResolveMemoriesForTask(ctx, projA, []string{"mem-a-2", "mem-a-1"})
	if err != nil {
		t.Fatalf("ResolveMemoriesForTask(A2, A1) failed: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(resolved))
	}
	if resolved[0].ID != "mem-a-2" || resolved[0].Content != testMemContentA2 {
		t.Errorf("expected first memory to be mem-a-2, got %+v", resolved[0])
	}
	if resolved[1].ID != "mem-a-1" || resolved[1].Content != testMemContentA1 {
		t.Errorf("expected second memory to be mem-a-1, got %+v", resolved[1])
	}

	// 3. 跨專案記憶拒絕 (CTXMEM-002)
	_, err = store.ResolveMemoriesForTask(ctx, projA, []string{"mem-b-1"})
	if err == nil {
		t.Fatal("expected error when resolving cross-project memory mem-b-1 from projA, got nil")
	}
	if !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Errorf("expected ErrMemoryNotFound, got %v", err)
	}

	// 4. 不存在記憶拒絕 (CTXMEM-005)
	_, err = store.ResolveMemoriesForTask(ctx, projA, []string{"mem-nonexistent"})
	if err == nil {
		t.Fatal("expected error when resolving nonexistent memory, got nil")
	}
	if !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Errorf("expected ErrMemoryNotFound, got %v", err)
	}

	// 5. 已刪除記憶拒絕 (CTXMEM-005)
	if err := store.DeleteMemory(ctx, projA, "mem-a-1"); err != nil {
		t.Fatalf("DeleteMemory(mem-a-1) failed: %v", err)
	}
	_, err = store.ResolveMemoriesForTask(ctx, projA, []string{"mem-a-1"})
	if err == nil {
		t.Fatal("expected error when resolving deleted memory mem-a-1, got nil")
	}
	if !errors.Is(err, storage.ErrMemoryNotFound) {
		t.Errorf("expected ErrMemoryNotFound, got %v", err)
	}

	// 6. 空 workspace 拒絕
	_, err = store.ResolveMemoriesForTask(ctx, "", []string{"mem-a-2"})
	if err == nil {
		t.Fatal("expected error when resolving with empty workspace, got nil")
	}
}

