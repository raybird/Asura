package contextwin

import (
	"fmt"
)

// AdmissionEngine 負責在 Stage 執行前進行 Context 預算評估與准入判定
type AdmissionEngine struct {
	catalog   Catalog
	estimator Estimator
}

// NewAdmissionEngine 建立准入檢查引擎實例
func NewAdmissionEngine(catalog Catalog, estimator Estimator) *AdmissionEngine {
	if catalog == nil {
		catalog = NewStaticCatalog()
	}
	if estimator == nil {
		estimator = NewHeuristicEstimator(DefaultReserveRatio)
	}
	return &AdmissionEngine{
		catalog:   catalog,
		estimator: estimator,
	}
}

// EvaluateStage 評估單一 Stage 之 Context 需求並產出 StageAdmission
func (e *AdmissionEngine) EvaluateStage(stage, prompt, model string, cfg Config) StageAdmission {
	reserveRatio := cfg.ReserveRatio
	if reserveRatio <= 0 {
		reserveRatio = DefaultReserveRatio
	}

	policy := cfg.Policy
	if policy == "" {
		policy = PolicyObserve
	}

	estimate := e.estimator.EstimateWithReserve(stage, prompt, reserveRatio)
	window := e.catalog.ResolveWindow(model, cfg.ContextLimit)

	adm := StageAdmission{
		Stage:       stage,
		Policy:      string(policy),
		Model:       window.Model,
		MaxTokens:   window.MaxTokens,
		LimitSource: window.LimitSource,
		Estimate:    estimate,
	}

	// 1. 若無有效上限限制（LimitSourceUnknown 或 MaxTokens <= 0）
	if window.LimitSource == LimitSourceUnknown || window.MaxTokens <= 0 {
		adm.Verdict = VerdictBypassed
		adm.Reason = "no context limit configured or model unknown"
		return adm
	}

	// 2. 比較估算 Token 與上限
	if estimate.TotalEstimatedTokens <= window.MaxTokens {
		adm.Verdict = VerdictAdmitted
		return adm
	}

	// 3. 超量情形：依 Policy 決定 VerdictRejected 或 VerdictWarning
	if policy == PolicyReject {
		adm.Verdict = VerdictRejected
		adm.Reason = fmt.Sprintf("estimated tokens %d exceeds limit %d", estimate.TotalEstimatedTokens, window.MaxTokens)
	} else {
		adm.Verdict = VerdictWarning
		adm.Reason = fmt.Sprintf("estimated tokens %d exceeds limit %d (observed)", estimate.TotalEstimatedTokens, window.MaxTokens)
	}

	return adm
}
