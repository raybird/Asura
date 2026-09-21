package contextwin

import (
	"math"

	"asura/internal/backend"
)

// DefaultReserveRatio 預設預估輸出空間保留比例（20%）
const DefaultReserveRatio = 0.20

// EstimationMethodHeuristicV1 定義啟發式字元換算版本名稱
const EstimationMethodHeuristicV1 = "heuristic-char-ratio-v1"

// Estimator 定義 Context Token 估算器介面
type Estimator interface {
	Estimate(stage string, prompt string) StageEstimate
	EstimateWithReserve(stage string, prompt string, reserveRatio float64) StageEstimate
}

// HeuristicEstimator 提供輕量啟發式 Token 估算實作
//
// 限制說明：
// 本估算器採用字元與 Rune 換算（ASCII: ~3.5 字元/token，CJK/非 ASCII: ~2 token/字元），
// 不依賴外部 CGO 或特定模型之 Tokenizer。本計算純為執行前預算防禦與可觀測性之保守估算，
// 無法保證 CLI 內部或特定模型之 Context 絕對不溢出。計算結果一律標記為 ProvenanceEstimated。
type HeuristicEstimator struct {
	defaultReserveRatio float64
}

// NewHeuristicEstimator 建立具有指定預設保留比例之估算器
func NewHeuristicEstimator(defaultReserveRatio float64) *HeuristicEstimator {
	if defaultReserveRatio <= 0 {
		defaultReserveRatio = DefaultReserveRatio
	}
	return &HeuristicEstimator{
		defaultReserveRatio: defaultReserveRatio,
	}
}

// Estimate 使用預設輸出空間保留比例估算 Stage Context
func (e *HeuristicEstimator) Estimate(stage string, prompt string) StageEstimate {
	return e.EstimateWithReserve(stage, prompt, e.defaultReserveRatio)
}

// EstimateWithReserve 使用自訂保留比例估算 Stage Context
func (e *HeuristicEstimator) EstimateWithReserve(stage string, prompt string, reserveRatio float64) StageEstimate {
	if len(prompt) == 0 {
		return StageEstimate{
			Stage:                 stage,
			EstimatedPromptTokens: 0,
			ReservedOutputTokens:  0,
			TotalEstimatedTokens:  0,
			Provenance:            backend.ProvenanceEstimated,
			EstimationMethod:      EstimationMethodHeuristicV1,
		}
	}

	if reserveRatio <= 0 {
		reserveRatio = e.defaultReserveRatio
	}

	var asciiCount int64
	var nonAsciiCount int64

	for _, r := range prompt {
		if r <= 127 {
			asciiCount++
		} else {
			nonAsciiCount++
		}
	}

	// 啟發式公式：
	// ASCII 字元：1 token 約 3.5 字元（保守取 ceil）
	// 非 ASCII / CJK 字元：1 字元約 2 tokens
	promptTokens := int64(math.Ceil(float64(asciiCount)/3.5)) + (nonAsciiCount * 2)
	if promptTokens == 0 && len(prompt) > 0 {
		promptTokens = 1
	}

	// 保留輸出空間（保守取 ceil）
	reservedTokens := int64(math.Ceil(float64(promptTokens) * reserveRatio))
	totalTokens := promptTokens + reservedTokens

	return StageEstimate{
		Stage:                 stage,
		EstimatedPromptTokens: promptTokens,
		ReservedOutputTokens:  reservedTokens,
		TotalEstimatedTokens:  totalTokens,
		Provenance:            backend.ProvenanceEstimated,
		EstimationMethod:      EstimationMethodHeuristicV1,
	}
}
