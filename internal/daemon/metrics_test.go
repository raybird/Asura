package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"asura/internal/auth"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

func TestPipelineMetrics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "asura-metrics-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "asura.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	policy := auth.NewPolicy([]string{tmpDir})
	sockPath := filepath.Join(tmpDir, "daemon.sock")

	var modelCallCount int32
	runner := func(ctx context.Context, ws, prompt string) (string, error) {
		atomic.AddInt32(&modelCallCount, 1)
		time.Sleep(10 * time.Millisecond) // 確保 duration > 0
		return "{\"goal\":\"test\",\"steps\":[\"step 1\"],\"expected_outcome\":\"done\",\"verdict\":\"pass\",\"findings\":[]}", nil
	}

	oracleStage := pipeline.NewOracleStage(runner)
	strikerStage := pipeline.NewStrikerStage(runner)
	criticStage := pipeline.NewCriticStage(runner)
	orc := pipeline.NewOrchestrator(oracleStage, strikerStage, criticStage)

	eng := engine.NewEngine(store, policy,
		engine.WithRunner(runner),
		engine.WithPipelineRunner(orc),
	)

	d := NewDaemon(Config{
		SocketPath: sockPath,
		MaxWorkers: 2,
	}, store, policy, WithEngine(eng))

	if err := d.Start(); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	defer d.Close()

	client, err := NewClient(sockPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// 1. 執行 Direct 任務
	directTask, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "direct prompt",
		Mode:      "direct",
	})
	if err != nil {
		t.Fatalf("submit direct task failed: %v", err)
	}

	// 等候 Direct 完成
	var completedDirect *storage.Task
	for i := 0; i < 20; i++ {
		tQuery, err := store.GetTask(context.Background(), directTask.ID)
		if err == nil && (tQuery.Status == storage.StatusCompleted || tQuery.Status == storage.StatusFailed) {
			completedDirect = tQuery
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if completedDirect == nil {
		t.Fatalf("direct task timed out waiting for completion")
	}

	// 2. 執行 Three-Face 任務
	tfTask, err := client.Submit(context.Background(), engine.SubmitRequest{
		Workspace: tmpDir,
		Prompt:    "three face prompt",
		Mode:      "three-face",
	})
	if err != nil {
		t.Fatalf("submit three-face task failed: %v", err)
	}

	// 等候 Three-Face 完成
	var completedTF *storage.Task
	for i := 0; i < 20; i++ {
		tQuery, err := store.GetTask(context.Background(), tfTask.ID)
		if err == nil && (tQuery.Status == storage.StatusCompleted || tQuery.Status == storage.StatusFailed) {
			completedTF = tQuery
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if completedTF == nil {
		t.Fatalf("three-face task timed out waiting for completion")
	}

	// 3. 驗證 Direct 任務的 Metrics
	if completedDirect.PipelineTrace == "" {
		t.Fatalf("direct task pipeline trace is empty")
	}
	var directTrace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completedDirect.PipelineTrace), &directTrace); err != nil {
		t.Fatalf("unmarshal direct trace failed: %v", err)
	}

	if directTrace.Mode != pipeline.ModeDirect {
		t.Errorf("expected direct mode, got %s", directTrace.Mode)
	}
	if directTrace.ModelCalls != 1 {
		t.Errorf("direct task expected 1 model call, got %d", directTrace.ModelCalls)
	}
	if len(directTrace.Stages) != 1 || directTrace.Stages[0].Name != "striker" {
		t.Errorf("direct task expected 1 striker stage, got %v", directTrace.Stages)
	}

	// 4. 驗證 Three-Face 任務的 Metrics
	if completedTF.PipelineTrace == "" {
		t.Fatalf("three-face task pipeline trace is empty")
	}
	var tfTrace pipeline.PipelineTrace
	if err := json.Unmarshal([]byte(completedTF.PipelineTrace), &tfTrace); err != nil {
		t.Fatalf("unmarshal three-face trace failed: %v", err)
	}

	if tfTrace.Mode != pipeline.ModeThreeFace {
		t.Errorf("expected three-face mode, got %s", tfTrace.Mode)
	}
	if tfTrace.ModelCalls != 3 {
		t.Errorf("three-face task expected 3 model calls, got %d", tfTrace.ModelCalls)
	}
	if len(tfTrace.Stages) != 3 {
		t.Fatalf("three-face task expected 3 stages, got %d", len(tfTrace.Stages))
	}
	stageNames := []string{tfTrace.Stages[0].Name, tfTrace.Stages[1].Name, tfTrace.Stages[2].Name}
	if stageNames[0] != "oracle" || stageNames[1] != "striker" || stageNames[2] != "critic" {
		t.Errorf("expected stages [oracle, striker, critic], got %v", stageNames)
	}
	if tfTrace.DurationMs <= 0 {
		t.Errorf("expected duration_ms > 0, got %d", tfTrace.DurationMs)
	}
}
