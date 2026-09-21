package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"asura/internal/backend"
	"asura/internal/engine"
)

// OracleStage 定義 Oracle 階段介面
type OracleStage interface {
	Execute(ctx context.Context, execCtx *engine.ExecutionContext) (*OraclePlan, error)
}

// StrikerStage 定義 Striker 階段介面
type StrikerStage interface {
	Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan) (string, error)
}

// CriticStage 定義 Critic 階段介面
type CriticStage interface {
	Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*CriticReview, error)
}

// PipelineOrchestrator 負責任務管線的 Stage 調度、順序控制與 Fail-Closed 防禦
type PipelineOrchestrator struct {
	registry backend.Registry
	oracle   OracleStage
	striker  StrikerStage
	critic   CriticStage
}

// NewOrchestrator 建立新的 PipelineOrchestrator 實例
func NewOrchestrator(oracle OracleStage, striker StrikerStage, critic CriticStage) *PipelineOrchestrator {
	return &PipelineOrchestrator{
		oracle:  oracle,
		striker: striker,
		critic:  critic,
	}
}

// WithRegistry 為 Orchestrator 配置後端註冊表，並向下傳播至各個 Stage
func (o *PipelineOrchestrator) WithRegistry(reg backend.Registry) *PipelineOrchestrator {
	o.registry = reg
	if s, ok := o.oracle.(*DefaultOracleStage); ok && s != nil {
		s.WithRegistry(reg)
	}
	if s, ok := o.striker.(*DefaultStrikerStage); ok && s != nil {
		s.WithRegistry(reg)
	}
	if s, ok := o.critic.(*DefaultCriticStage); ok && s != nil {
		s.WithRegistry(reg)
	}
	return o
}

// Execute 依指定模式執行管線
func (o *PipelineOrchestrator) Execute(ctx context.Context, mode ExecutionMode, execCtx *engine.ExecutionContext) (string, *PipelineTrace, error) {
	if mode == "" {
		mode = ModeDirect
	}

	backendID := "opencode"
	if execCtx != nil && execCtx.Backend != "" {
		backendID = execCtx.Backend
	}

	var activeBackend backend.Backend
	var backendVer string
	if o.registry != nil {
		b, err := o.registry.Get(backendID)
		if err != nil {
			// 未知後端在任何模型呼叫前 fail-closed 拒絕 (MBACK-003)
			return "", nil, fmt.Errorf("backend preflight failed: %w", err)
		}
		activeBackend = b
		backendVer, _ = b.Version(ctx)
	}

	// 能力預檢 (Capability Preflight, MBACK-008)
	if activeBackend != nil {
		// 任何執行模式皆需要工作區寫入能力以供 Striker 執行
		if !activeBackend.HasCapability(backend.CapWorkspaceWrite) {
			return "", nil, fmt.Errorf("%w: backend %q does not support workspace write required for execution", backend.ErrUnsupportedCapability, activeBackend.ID())
		}
		// Three-Face 模式額外要求 Oracle 與 Critic 之唯讀沙箱安全能力
		if mode == ModeThreeFace && !activeBackend.HasCapability(backend.CapReadOnlySandbox) {
			return "", nil, fmt.Errorf("%w: backend %q does not support read-only sandbox required for Three-Face", backend.ErrUnsupportedCapability, activeBackend.ID())
		}
	}

	if mode == ModeDirect {
		start := time.Now()
		out, err := o.striker.Execute(ctx, execCtx, nil)
		duration := time.Since(start).Milliseconds()

		status := StageCompleted
		var errStr string
		if err != nil {
			status = StageFailed
			errStr = err.Error()
		}

		trace := &PipelineTrace{
			Mode:      ModeDirect,
			BackendID: backendID,
			Stages: []StageTrace{
				{
					Name:           "striker",
					Status:         status,
					DurationMs:     duration,
					BackendID:      backendID,
					BackendVersion: backendVer,
					Error:          errStr,
					Output:         out,
				},
			},
			ModelCalls:  1,
			DurationMs:  duration,
			FinalResult: out,
		}
		return out, trace, err
	}

	// ModeThreeFace 固定依序執行：Oracle → Striker → Critic
	pipelineStart := time.Now()
	trace := &PipelineTrace{
		Mode:      ModeThreeFace,
		BackendID: backendID,
		Stages:    make([]StageTrace, 0, 3),
	}

	// 1. Oracle Stage
	s1 := time.Now()
	plan, err := o.oracle.Execute(ctx, execCtx)
	d1 := time.Since(s1).Milliseconds()
	trace.ModelCalls++

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "oracle",
			Status:         StageFailed,
			DurationMs:     d1,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Error:          err.Error(),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return "", trace, err
	}

	trace.OraclePlan = plan
	trace.Stages = append(trace.Stages, StageTrace{
		Name:           "oracle",
		Status:         StageCompleted,
		DurationMs:     d1,
		BackendID:      backendID,
		BackendVersion: backendVer,
		Output:         "plan generated",
	})

	// 2. Striker Stage (MBACK-010: 部分執行失敗保存 partial output，絕無第二次容錯嘗試)
	s2 := time.Now()
	strikerOut, err := o.striker.Execute(ctx, execCtx, plan)
	d2 := time.Since(s2).Milliseconds()
	trace.ModelCalls++

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "striker",
			Status:         StageFailed,
			DurationMs:     d2,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Error:          err.Error(),
			Output:         strikerOut,
		})
		trace.FinalResult = strikerOut
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return strikerOut, trace, err
	}

	trace.FinalResult = strikerOut
	trace.Stages = append(trace.Stages, StageTrace{
		Name:           "striker",
		Status:         StageCompleted,
		DurationMs:     d2,
		BackendID:      backendID,
		BackendVersion: backendVer,
		Output:         strikerOut,
	})

	// 3. Critic Stage
	s3 := time.Now()
	review, err := o.critic.Execute(ctx, execCtx, plan, strikerOut)
	d3 := time.Since(s3).Milliseconds()
	trace.ModelCalls++

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "critic",
			Status:         StageFailed,
			DurationMs:     d3,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Error:          err.Error(),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return strikerOut, trace, err
	}

	trace.CriticReview = review
	trace.Stages = append(trace.Stages, StageTrace{
		Name:           "critic",
		Status:         StageCompleted,
		DurationMs:     d3,
		BackendID:      backendID,
		BackendVersion: backendVer,
		Output:         string(review.Verdict),
	})
	trace.DurationMs = time.Since(pipelineStart).Milliseconds()

	if review.Verdict == VerdictFailed {
		errMsg := fmt.Sprintf("critic review verdict failed: %s", review.Summary)
		if review.Summary == "" {
			errMsg = "critic review verdict failed"
		}
		return strikerOut, trace, errors.New(errMsg)
	}

	return strikerOut, trace, nil
}

// ExecutePipeline 提供與 engine.PipelineRunner 介面相容之呼叫入口
func (o *PipelineOrchestrator) ExecutePipeline(ctx context.Context, mode string, execCtx *engine.ExecutionContext) (string, string, error) {
	out, trace, err := o.Execute(ctx, ExecutionMode(mode), execCtx)
	traceJSON := ""
	if trace != nil {
		b, _ := json.Marshal(trace)
		traceJSON = string(b)
	}
	return out, traceJSON, err
}
