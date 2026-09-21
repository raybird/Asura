package opencode

// ContextCapabilityMatrix 記錄後端在指定版本下的 Context 與 Token 觀測/控制能力
type ContextCapabilityMatrix struct {
	BackendID                   string            `json:"backend_id"`
	TargetVersion               string            `json:"target_version"`
	InstalledVersion            string            `json:"installed_version,omitempty"`
	ProbeDate                   string            `json:"probe_date"`
	Status                      string            `json:"status"` // verified_installed, verified_probe_artifact, unverified_not_installed
	HasTokenUsage               bool              `json:"has_token_usage"`
	TokenFields                 []string          `json:"token_fields"`
	MissingTokenFields          []string          `json:"missing_token_fields"`
	HasCachedTokens             bool              `json:"has_cached_tokens"`
	HasReasoningTokens          bool              `json:"has_reasoning_tokens"`
	TokenReportingGranularity   string            `json:"token_reporting_granularity"` // per_step, per_turn
	UsagePreservedOnToolFailure bool              `json:"usage_preserved_on_tool_failure"`
	PreflightModelIdentifiable  bool              `json:"preflight_model_identifiable"` // 自 stdout JSONL 能否識別模型
	UnsupportedCapabilities     map[string]string `json:"unsupported_capabilities"`
}

// GetOpenCode11831Matrix 回傳針對 OpenCode 1.18.31 之能力矩陣
func GetOpenCode11831Matrix() ContextCapabilityMatrix {
	return ContextCapabilityMatrix{
		BackendID:                 "opencode",
		TargetVersion:             "1.18.31",
		ProbeDate:                 "2026-09-21",
		Status:                    "verified_installed",
		HasTokenUsage:             true,
		TokenFields:               []string{"input", "output", "total", "reasoning", "cache.read", "cache.write"},
		MissingTokenFields:        []string{},
		HasCachedTokens:           true,
		HasReasoningTokens:        true,
		TokenReportingGranularity: "per_step",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "OpenCode CLI 非互動模式無外部攔截 hook，無法在送入模型前修改或過濾工具輸出",
			"history_dynamic_rewrite":  "非互動式單次運行無會話歷史熱插拔 API",
			"context_limit_query":      "CLI 未提供動態查詢目前模型 Context Window 上限之指令",
			"stdout_model_identity":    "stdout JSONL 事件流未包含使用的具體 model 名稱",
		},
	}
}

// GetOpenCode11823Matrix 回傳針對 OpenCode 1.18.23 之能力矩陣（具有真實 probe artifact 證實）
func GetOpenCode11823Matrix() ContextCapabilityMatrix {
	return ContextCapabilityMatrix{
		BackendID:                   "opencode",
		TargetVersion:               "1.18.23",
		ProbeDate:                   "2026-09-21",
		Status:                      "verified_probe_artifact",
		HasTokenUsage:               true,
		TokenFields:                 []string{"input", "output", "total", "reasoning", "cache.read", "cache.write"},
		MissingTokenFields:          []string{},
		HasCachedTokens:             true,
		HasReasoningTokens:          true,
		TokenReportingGranularity:   "per_step",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "OpenCode 1.18.23 非互動模式無外部攔截 hook",
			"history_dynamic_rewrite":  "1.18.23 無會話歷史熱插拔 API",
			"context_limit_query":      "1.18.23 CLI 未提供動態查詢 Context Window 指令",
			"stdout_model_identity":    "1.18.23 JSONL 未包含 model 欄位",
		},
	}
}
