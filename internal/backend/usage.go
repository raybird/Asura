package backend

// DataProvenance 標示資料來源與可信度
type DataProvenance string

const (
	// ProvenanceExact 代表由後端進程實測精確回報之數據
	ProvenanceExact DataProvenance = "exact"
	// ProvenanceEstimated 代表由啟發式安全換算預估之數據
	ProvenanceEstimated DataProvenance = "estimated"
	// ProvenanceUnknown 代表數值缺失或未提供
	ProvenanceUnknown DataProvenance = "unknown"
)

// TokenUsage 代表標準化之 Token 用量觀測
// 使用 nullable 指標以嚴格區分「欄位未提供／未知 (nil)」與「明確回報為 0 (*v == 0)」
type TokenUsage struct {
	InputTokens     *int64 `json:"input_tokens,omitempty"`
	OutputTokens    *int64 `json:"output_tokens,omitempty"`
	TotalTokens     *int64 `json:"total_tokens,omitempty"`
	CachedTokens    *int64 `json:"cached_tokens,omitempty"`
	ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
}

// Int64Ptr 建立整數指標輔助函式
func Int64Ptr(v int64) *int64 {
	return &v
}
