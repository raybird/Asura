package contextwin

import "asura/internal/backend"

// LimitSource 標示模型 Context 上限之來源
type LimitSource string

const (
	LimitSourceConfig  LimitSource = "config"  // 顯式配置覆蓋
	LimitSourceCatalog LimitSource = "catalog" // 靜態模型目錄
	LimitSourceUnknown LimitSource = "unknown" // 未知上限
)

// ModelWindowInfo 描述模型窗口與限制
type ModelWindowInfo struct {
	Model       string      `json:"model"`
	MaxTokens   int64       `json:"max_tokens"`
	LimitSource LimitSource `json:"limit_source"`
}

// StageEstimate 描述單一 Stage 在執行前估算之 Context 需求
type StageEstimate struct {
	Stage                 string                 `json:"stage"`
	EstimatedPromptTokens int64                  `json:"estimated_prompt_tokens"`
	ReservedOutputTokens  int64                  `json:"reserved_output_tokens"`
	TotalEstimatedTokens  int64                  `json:"total_estimated_tokens"`
	Provenance            backend.DataProvenance `json:"provenance"`
	EstimationMethod      string                 `json:"estimation_method"`
}

// AdmissionVerdict 准入檢查結論
type AdmissionVerdict string

const (
	VerdictAdmitted AdmissionVerdict = "admitted"
	VerdictRejected AdmissionVerdict = "rejected"
	VerdictWarning  AdmissionVerdict = "warning"
	VerdictBypassed AdmissionVerdict = "bypassed" // 未配置限制或處於未知模式
)

// AdmissionPolicy 准入檢查政策
type AdmissionPolicy string

const (
	PolicyObserve AdmissionPolicy = "observe" // 超量僅警示並記錄
	PolicyReject  AdmissionPolicy = "reject"  // 超量直接拒絕執行
)

// StageAdmission 記錄執行前之准入審計結果
type StageAdmission struct {
	Stage       string           `json:"stage"`
	Verdict     AdmissionVerdict `json:"verdict"`
	Policy      string           `json:"policy"`
	Model       string           `json:"model,omitempty"`
	MaxTokens   int64            `json:"max_tokens,omitempty"`
	LimitSource LimitSource      `json:"limit_source"`
	Estimate    StageEstimate    `json:"estimate"`
	Reason      string           `json:"reason,omitempty"`
}
