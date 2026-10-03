package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"asura/internal/backend"
	"asura/internal/contextwin"
	"asura/internal/engine"
)

// OracleStage 定義 Oracle 階段介面
type OracleStage interface {
	Prepare(execCtx *engine.ExecutionContext) (*backend.ExecutionRequest, error)
	ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (*OraclePlan, *backend.ExecutionResult, error)
	Execute(ctx context.Context, execCtx *engine.ExecutionContext) (*OraclePlan, error)
}

// StrikerStage 定義 Striker 階段介面
type StrikerStage interface {
	Prepare(execCtx *engine.ExecutionContext, plan *OraclePlan) (*backend.ExecutionRequest, error)
	ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (string, *backend.ExecutionResult, error)
	Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan) (string, error)
}

// CriticStage 定義 Critic 階段介面
type CriticStage interface {
	Prepare(execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*backend.ExecutionRequest, error)
	ExecutePrepared(ctx context.Context, execCtx *engine.ExecutionContext, req *backend.ExecutionRequest) (*CriticReview, *backend.ExecutionResult, error)
	Execute(ctx context.Context, execCtx *engine.ExecutionContext, plan *OraclePlan, strikerOutput string) (*CriticReview, error)
}

// PipelineOrchestrator 負責任務管線的 Stage 調度、順序控制與 Fail-Closed 防禦
type PipelineOrchestrator struct {
	registry        backend.Registry
	oracle          OracleStage
	striker         StrikerStage
	critic          CriticStage
	contextCfg      contextwin.Config
	admissionEngine *contextwin.AdmissionEngine
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

// WithContextBudget 配置 Context Window 管理設定與准入引擎
func (o *PipelineOrchestrator) WithContextBudget(cfg contextwin.Config) *PipelineOrchestrator {
	o.contextCfg = cfg
	if o.admissionEngine == nil {
		o.admissionEngine = contextwin.NewAdmissionEngine(contextwin.NewStaticCatalog(), contextwin.NewHeuristicEstimator(cfg.ReserveRatio))
	}
	return o
}

// WithAdmissionEngine 注入自訂准入檢查引擎實例
func (o *PipelineOrchestrator) WithAdmissionEngine(engine *contextwin.AdmissionEngine) *PipelineOrchestrator {
	o.admissionEngine = engine
	return o
}

func (o *PipelineOrchestrator) evaluateAdmission(stage, prompt, model string) contextwin.StageAdmission {
	engine := o.admissionEngine
	if engine == nil {
		engine = contextwin.NewAdmissionEngine(contextwin.NewStaticCatalog(), contextwin.NewHeuristicEstimator(o.contextCfg.ReserveRatio))
	}
	return engine.EvaluateStage(stage, prompt, model, o.contextCfg)
}

// 2026-10-03：所有 runtime 接受入口共用同一純 policy 與 matcher。
func (o *PipelineOrchestrator) Preflight(ctx context.Context, mode, requested string) (*backend.ExecutionContract, error) {
	if o.registry == nil {
		return nil, nil
	}
	return backend.AdmitRuntime(ctx, o.registry, mode, requested)
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
			if execCtx != nil && execCtx.ExecutionContract != nil {
				return "", &PipelineTrace{Mode: mode, BackendID: backendID, ExecutionContract: execCtx.ExecutionContract, GuardError: err.Error()}, fmt.Errorf("backend preflight failed: %w", err)
			}
			return "", nil, fmt.Errorf("backend preflight failed: %w", err)
		}
		activeBackend = b
		backendVer, _ = b.Version(ctx)
	}

	// 2026-10-03：封存契約的需求保持不變，執行前只查核目前安全能力。
	var contract *backend.ExecutionContract
	var guard *backend.CapabilityMatch
	if execCtx != nil {
		contract = execCtx.ExecutionContract
	}
	requirements, reqErr := backend.RequirementsForMode(string(mode))
	if contract != nil {
		if err := contract.Validate(string(mode), backendID); err != nil {
			return "", &PipelineTrace{Mode: mode, BackendID: backendID, ExecutionContract: contract, GuardError: err.Error()}, err
		}
		requirements = contract.Requirements
	}
	if reqErr != nil {
		return "", nil, reqErr
	}
	if activeBackend != nil {
		m, err := backend.MatchCapabilities(requirements, backend.RuntimeDescriptor{ID: activeBackend.ID(), Version: backendVer, Capabilities: activeBackend.Capabilities()})
		guard = &m
		if err == nil && !m.Compatible {
			err = &backend.CapabilityAdmissionError{RequestedBackend: backendID, Match: m}
		}
		if err != nil {
			if contract != nil {
				return "", &PipelineTrace{Mode: mode, BackendID: backendID, ExecutionContract: contract, ExecutionGuard: guard, GuardError: err.Error()}, err
			}
			return "", nil, err
		}
	} else if contract != nil {
		err := fmt.Errorf("%w: sealed runtime unavailable", backend.ErrBackendNotFound)
		return "", &PipelineTrace{Mode: mode, BackendID: backendID, ExecutionContract: contract, GuardError: err.Error()}, err
	}
	if execCtx != nil {
		execCtx.BoundBackend = activeBackend
	}

	if mode == ModeDirect {
		start := time.Now()
		req, pErr := o.striker.Prepare(execCtx, nil)
		if pErr != nil {
			return "", nil, fmt.Errorf("striker prepare error: %w", pErr)
		}

		adm := o.evaluateAdmission("striker", req.Prompt, req.Model)
		var admPtr *contextwin.StageAdmission = &adm

		if adm.Verdict == contextwin.VerdictRejected {
			duration := time.Since(start).Milliseconds()
			trace := &PipelineTrace{
				ExecutionContract: contract,
				ExecutionGuard:    guard,
				Mode:              ModeDirect,
				BackendID:         backendID,
				Stages: []StageTrace{
					{
						Name:           "striker",
						Status:         StageFailed,
						DurationMs:     duration,
						BackendID:      backendID,
						BackendVersion: backendVer,
						Admission:      admPtr,
						Error:          fmt.Sprintf("context limit exceeded: %s", adm.Reason),
					},
				},
				ModelCalls:  0,
				DurationMs:  duration,
				FinalResult: "",
			}
			return "", trace, fmt.Errorf("context limit exceeded: %s", adm.Reason)
		}

		out, execRes, err := o.striker.ExecutePrepared(ctx, execCtx, req)
		duration := time.Since(start).Milliseconds()

		status := StageCompleted
		var errStr string
		if err != nil {
			status = StageFailed
			errStr = err.Error()
		}

		var usage *backend.TokenUsage
		usageSource := backend.ProvenanceUnknown
		if execRes != nil && execRes.Usage != nil {
			usage = execRes.Usage
			usageSource = execRes.UsageSource
		}

		trace := &PipelineTrace{
			ExecutionContract: contract,
			ExecutionGuard:    guard,
			Mode:              ModeDirect,
			BackendID:         backendID,
			Stages: []StageTrace{
				{
					Name:           "striker",
					Status:         status,
					DurationMs:     duration,
					BackendID:      backendID,
					BackendVersion: backendVer,
					Admission:      admPtr,
					Usage:          usage,
					UsageSource:    usageSource,
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
		ExecutionContract: contract,
		ExecutionGuard:    guard,
		Mode:              ModeThreeFace,
		BackendID:         backendID,
		Stages:            make([]StageTrace, 0, 3),
	}

	// 1. Oracle Stage
	req1, pErr := o.oracle.Prepare(execCtx)
	if pErr != nil {
		return "", trace, fmt.Errorf("oracle prepare error: %w", pErr)
	}
	adm1 := o.evaluateAdmission("oracle", req1.Prompt, req1.Model)
	if adm1.Verdict == contextwin.VerdictRejected {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "oracle",
			Status:         StageFailed,
			DurationMs:     0,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Admission:      &adm1,
			Error:          fmt.Sprintf("context limit exceeded: %s", adm1.Reason),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return "", trace, fmt.Errorf("context limit exceeded: %s", adm1.Reason)
	}

	s1 := time.Now()
	plan, res1, err := o.oracle.ExecutePrepared(ctx, execCtx, req1)
	d1 := time.Since(s1).Milliseconds()
	trace.ModelCalls++

	var u1 *backend.TokenUsage
	us1 := backend.ProvenanceUnknown
	if res1 != nil && res1.Usage != nil {
		u1 = res1.Usage
		us1 = res1.UsageSource
	}

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "oracle",
			Status:         StageFailed,
			DurationMs:     d1,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Admission:      &adm1,
			Usage:          u1,
			UsageSource:    us1,
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
		Admission:      &adm1,
		Usage:          u1,
		UsageSource:    us1,
		Output:         "plan generated",
	})

	// 2. Striker Stage (MBACK-010: 部分執行失敗保存 partial output，絕無第二次容錯嘗試)
	req2, pErr := o.striker.Prepare(execCtx, plan)
	if pErr != nil {
		return "", trace, fmt.Errorf("striker prepare error: %w", pErr)
	}
	adm2 := o.evaluateAdmission("striker", req2.Prompt, req2.Model)
	if adm2.Verdict == contextwin.VerdictRejected {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "striker",
			Status:         StageFailed,
			DurationMs:     0,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Admission:      &adm2,
			Error:          fmt.Sprintf("context limit exceeded: %s", adm2.Reason),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return "", trace, fmt.Errorf("context limit exceeded: %s", adm2.Reason)
	}

	s2 := time.Now()
	strikerOut, res2, err := o.striker.ExecutePrepared(ctx, execCtx, req2)
	d2 := time.Since(s2).Milliseconds()
	trace.ModelCalls++

	var u2 *backend.TokenUsage
	us2 := backend.ProvenanceUnknown
	if res2 != nil && res2.Usage != nil {
		u2 = res2.Usage
		us2 = res2.UsageSource
	}

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "striker",
			Status:         StageFailed,
			DurationMs:     d2,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Admission:      &adm2,
			Usage:          u2,
			UsageSource:    us2,
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
		Admission:      &adm2,
		Usage:          u2,
		UsageSource:    us2,
		Output:         strikerOut,
	})

	// 3. Critic Stage
	req3, pErr := o.critic.Prepare(execCtx, plan, strikerOut)
	if pErr != nil {
		return strikerOut, trace, fmt.Errorf("critic prepare error: %w", pErr)
	}
	adm3 := o.evaluateAdmission("critic", req3.Prompt, req3.Model)
	if adm3.Verdict == contextwin.VerdictRejected {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "critic",
			Status:         StageFailed,
			DurationMs:     0,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Admission:      &adm3,
			Error:          fmt.Sprintf("context limit exceeded: %s", adm3.Reason),
		})
		trace.DurationMs = time.Since(pipelineStart).Milliseconds()
		return strikerOut, trace, fmt.Errorf("context limit exceeded: %s", adm3.Reason)
	}

	s3 := time.Now()
	review, res3, err := o.critic.ExecutePrepared(ctx, execCtx, req3)
	d3 := time.Since(s3).Milliseconds()
	trace.ModelCalls++

	var u3 *backend.TokenUsage
	us3 := backend.ProvenanceUnknown
	if res3 != nil && res3.Usage != nil {
		u3 = res3.Usage
		us3 = res3.UsageSource
	}

	if err != nil {
		trace.Stages = append(trace.Stages, StageTrace{
			Name:           "critic",
			Status:         StageFailed,
			DurationMs:     d3,
			BackendID:      backendID,
			BackendVersion: backendVer,
			Admission:      &adm3,
			Usage:          u3,
			UsageSource:    us3,
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
		Admission:      &adm3,
		Usage:          u3,
		UsageSource:    us3,
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
