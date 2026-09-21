package codex

// CodexContextCapabilityMatrix 記錄 Codex CLI 在指定版本下的 Context 與 Token 觀測/控制能力
type CodexContextCapabilityMatrix struct {
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
	TokenReportingGranularity   string            `json:"token_reporting_granularity"` // per_turn
	UsagePreservedOnToolFailure bool              `json:"usage_preserved_on_tool_failure"`
	PreflightModelIdentifiable  bool              `json:"preflight_model_identifiable"` // 自 stdout JSONL 能否識別模型
	UnsupportedCapabilities     map[string]string `json:"unsupported_capabilities"`
}

// GetCodex01551Matrix 回傳針對 Codex CLI 0.155.1 之能力矩陣
func GetCodex01551Matrix() CodexContextCapabilityMatrix {
	return CodexContextCapabilityMatrix{
		BackendID:                 "codex",
		TargetVersion:             "0.155.1",
		ProbeDate:                 "2026-09-21",
		Status:                    "verified_installed",
		HasTokenUsage:             true,
		TokenFields:               []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens"},
		MissingTokenFields:        []string{"total_tokens"}, // Codex CLI 0.155.1 明确不提供 total_tokens
		HasCachedTokens:           true,
		HasReasoningTokens:        true,
		TokenReportingGranularity: "per_turn",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "Codex CLI exec 模式沙箱由內部 bwrap/環境控制，無法在送入模型前攔截過濾工具輸出",
			"history_dynamic_rewrite":  "單次 CLI invocation 無法動態替換歷史上下文",
			"context_limit_query":      "CLI 未提供查詢目前模型 Context Window 上限之指令",
			"stdout_model_identity":    "stdout JSONL 事件流未包含使用的具體 model 名稱",
		},
	}
}

// GetCodex01550Matrix 回傳針對 Codex CLI 0.155.0 之能力矩陣（具有真實 probe artifact 證實）
func GetCodex01550Matrix() CodexContextCapabilityMatrix {
	return CodexContextCapabilityMatrix{
		BackendID:                   "codex",
		TargetVersion:               "0.155.0",
		ProbeDate:                   "2026-09-21",
		Status:                      "verified_probe_artifact",
		HasTokenUsage:               true,
		TokenFields:                 []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens"},
		MissingTokenFields:          []string{"total_tokens"},
		HasCachedTokens:             true,
		HasReasoningTokens:          true,
		TokenReportingGranularity:   "per_turn",
		UsagePreservedOnToolFailure: true,
		PreflightModelIdentifiable:  false,
		UnsupportedCapabilities: map[string]string{
			"tool_output_interception": "Codex 0.155.0 無法在送入模型前攔截過濾工具輸出",
			"history_dynamic_rewrite":  "0.155.0 無動態歷史替換 API",
			"context_limit_query":      "0.155.0 CLI 未提供查詢 Context Window 上限指令",
			"stdout_model_identity":    "0.155.0 JSONL 事件未包含 model 欄位",
		},
	}
}
