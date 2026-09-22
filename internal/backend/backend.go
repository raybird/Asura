package backend

import (
	"context"
	"time"
)

// Role 定義管線階段在後端執行器層級之角色
type Role string

const (
	RoleOracle  Role = "oracle"
	RoleStriker Role = "striker"
	RoleCritic  Role = "critic"
)

// Capability 定義後端執行器具備之安全與功能能力
type Capability string

const (
	// CapReadOnlySandbox 代表具備唯讀沙箱邊界，禁止檔案寫入副作用 (Oracle, Critic 必要)
	CapReadOnlySandbox Capability = "read_only_sandbox"
	// CapWorkspaceWrite 代表具備工作區寫入能力 (Striker 必要)
	CapWorkspaceWrite Capability = "workspace_write"
	// CapStructuredEvents 代表具備結構化事件串流輸出
	CapStructuredEvents Capability = "structured_events"
	// CapProcessCancellation 代表具備進程組回收與快速取消能力
	CapProcessCancellation Capability = "process_cancellation"
)

// FailureClassification 定義標準化錯誤分類
type FailureClassification string

const (
	FailureNone            FailureClassification = ""
	FailureUnavailable     FailureClassification = "backend_unavailable"
	FailureUnsupported     FailureClassification = "unsupported_capability"
	FailureAuth            FailureClassification = "authentication_failed"
	FailureRateLimit       FailureClassification = "rate_limited"
	FailureExecution       FailureClassification = "execution_failed"
	FailureCancelled       FailureClassification = "cancelled"
	FailureEmptyResult     FailureClassification = "empty_result"
)

// ExecutionRequest 代表提交給後端的標準執行請求
type ExecutionRequest struct {
	Role      Role              `json:"role"`
	Workspace string            `json:"workspace"`
	Prompt    string            `json:"prompt"`
	Model     string            `json:"model,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ExecutionResult 代表後端執行完畢產出之標準結果
type ExecutionResult struct {
	BackendID      string                `json:"backend_id"`
	BackendVersion string                `json:"backend_version"`
	Output         string                `json:"output"`
	PartialOutput  string                `json:"partial_output,omitempty"`
	Events         []Event               `json:"events,omitempty"`
	FailureClass   FailureClassification `json:"failure_class,omitempty"`
	Usage          *TokenUsage           `json:"usage,omitempty"`
	UsageSource    DataProvenance        `json:"usage_source,omitempty"`
	StartedAt      time.Time             `json:"started_at"`
	CompletedAt    time.Time             `json:"completed_at"`
}

// Backend 定義統一後端執行器介面
type Backend interface {
	// ID 回傳後端唯一標識，例如 "opencode" 或 "codex"
	ID() string
	// Version 回傳後端目前解析到之版本字串
	Version(ctx context.Context) (string, error)
	// Capabilities 回傳後端支援的能力清單
	Capabilities() []Capability
	// HasCapability 檢查後端是否具備指定能力
	HasCapability(cap Capability) bool
	// Run 執行請求並回傳標準結果
	Run(ctx context.Context, req *ExecutionRequest) (*ExecutionResult, error)
}
