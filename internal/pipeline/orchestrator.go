package pipeline

import (
	"context"
	"encoding/json"
	"time"

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
	oracle  OracleStage
	striker StrikerStage
	critic  CriticStage
}

// NewOrchestrator 建立新的 PipelineOrchestrator 實例
func NewOrchestrator(oracle OracleStage, striker StrikerStage, critic CriticStage) *PipelineOrchestrator {
	return &PipelineOrchestrator{
		oracle:  oracle,
		striker: striker,
		critic:  critic,
	}
}

// Execute 依指定模式執行管線
func (o *PipelineOrchestrator) Execute(ctx context.Context, mode ExecutionMode, execCtx *engine.ExecutionContext) (string, *PipelineTrace, error) {
	if mode == "" {
		mode = ModeDirect
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
			Mode: ModeDirect,
			Stages: []StageTrace{
				{
					Name:       "striker",
					Status:     status,
					DurationMs: duration,
					Error:      errStr,
					Output:     out,
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
		Mode:   ModeThreeFace,
		Stages: make([]StageTrace, 0, 3),
	}

	// 1. Oracle Stage
	s1 := time.Now()
	plan, err := o.oracle.Execute(ctx, execCtx)
	d1 := time.Since(s1).Milliseconds()
	trace.ModelCalls++

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:       "oracle",
			Status:     StageFailed,
			DurationMs: d1,
			Error:      err.Error(),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return "", trace, err
	}

	trace.OraclePlan = plan
	trace.Stages = append(trace.Stages, StageTrace{
		Name:       "oracle",
		Status:     StageCompleted,
		DurationMs: d1,
		Output:     "plan generated",
	})

	// 2. Striker Stage
	s2 := time.Now()
	strikerOut, err := o.striker.Execute(ctx, execCtx, plan)
	d2 := time.Since(s2).Milliseconds()
	trace.ModelCalls++

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:       "striker",
			Status:     StageFailed,
			DurationMs: d2,
			Error:      err.Error(),
			Output:     strikerOut,
		})
		trace.FinalResult = strikerOut
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return strikerOut, trace, err
	}

	trace.FinalResult = strikerOut
	trace.Stages = append(trace.Stages, StageTrace{
		Name:       "striker",
		Status:     StageCompleted,
		DurationMs: d2,
		Output:     strikerOut,
	})

	// 3. Critic Stage
	s3 := time.Now()
	review, err := o.critic.Execute(ctx, execCtx, plan, strikerOut)
	d3 := time.Since(s3).Milliseconds()
	trace.ModelCalls++

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:       "critic",
			Status:     StageFailed,
			DurationMs: d3,
			Error:      err.Error(),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return strikerOut, trace, err
	}

	trace.CriticReview = review
	trace.Stages = append(trace.Stages, StageTrace{
		Name:       "critic",
		Status:     StageCompleted,
		DurationMs: d3,
		Output:     string(review.Verdict),
	})
	trace.DurationMs = time.Since(pipelineStart).Milliseconds()

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
