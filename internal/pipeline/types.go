package pipeline

import (
	"asura/internal/backend"
	"asura/internal/contextwin"
)

// ExecutionMode 定義任務執行管線的運作模式
type ExecutionMode string

const (
	// ModeDirect 直通模式：僅執行單一 Striker / Runner，完全向後相容
	ModeDirect ExecutionMode = "direct"
	// ModeThreeFace 固定管線模式：Oracle → Striker → Critic
	ModeThreeFace ExecutionMode = "three-face"
)

// IsValidExecutionMode 檢驗執行模式是否合法
func IsValidExecutionMode(mode ExecutionMode) bool {
	return mode == ModeDirect || mode == ModeThreeFace
}

// OraclePlan 代表 Oracle 分析後產出的結構化執行計畫
type OraclePlan struct {
	Goal            string            `json:"goal"`
	Constraints     []string          `json:"constraints"`
	Steps           []string          `json:"steps"`
	ExpectedOutcome string            `json:"expected_outcome"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// ReviewVerdict 代表 Critic 對 Striker 產出之審查結論
type ReviewVerdict string

const (
	VerdictPass           ReviewVerdict = "pass"
	VerdictNeedsAttention ReviewVerdict = "needs_attention"
	VerdictFailed         ReviewVerdict = "failed"
)

// CriticReview 代表 Critic 審查輸出實體
type CriticReview struct {
	MemoryUsage []MemoryUsage     `json:"memory_usage,omitempty"`
	Verdict     ReviewVerdict     `json:"verdict"`
	Findings    []string          `json:"findings"`
	Summary     string            `json:"summary"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// StageStatus 代表單一管線 Stage 的執行狀態
type StageStatus string

const (
	StagePending   StageStatus = "pending"
	StageRunning   StageStatus = "running"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
	StageSkipped   StageStatus = "skipped"
)

// StageTrace 代表單一 Stage 的執行歷程與耗時
type StageTrace struct {
	ExecutionGuard *backend.CapabilityMatch   `json:"execution_guard,omitempty"`
	GuardError     string                     `json:"guard_error,omitempty"`
	Name           string                     `json:"name"`
	Status         StageStatus                `json:"status"`
	DurationMs     int64                      `json:"duration_ms"`
	BackendID      string                     `json:"backend_id,omitempty"`
	BackendVersion string                     `json:"backend_version,omitempty"`
	Admission      *contextwin.StageAdmission `json:"admission,omitempty"`
	Usage          *backend.TokenUsage        `json:"usage,omitempty"`
	UsageSource    backend.DataProvenance     `json:"usage_source,omitempty"`
	Error          string                     `json:"error,omitempty"`
	Output         string                     `json:"output,omitempty"`
}

// PipelineTrace 代表完整任務執行管線的審計歷程
type PipelineTrace struct {
	ExecutionContract *backend.ExecutionContract `json:"execution_contract,omitempty"`
	ExecutionGuard    *backend.CapabilityMatch   `json:"execution_guard,omitempty"`
	GuardError        string                     `json:"guard_error,omitempty"`
	Mode              ExecutionMode              `json:"mode"`
	BackendID         string                     `json:"backend_id,omitempty"`
	OraclePlan        *OraclePlan                `json:"oracle_plan,omitempty"`
	CriticReview      *CriticReview              `json:"critic_review,omitempty"`
	Stages            []StageTrace               `json:"stages"`
	ModelCalls        int                        `json:"model_calls"`
	DurationMs        int64                      `json:"duration_ms"`
	FinalResult       string                     `json:"final_result,omitempty"`
}
