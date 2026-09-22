package contextwin

import "strings"

// Catalog 提供模型 Context 限制查詢介面
type Catalog interface {
	GetModelWindow(model string) ModelWindowInfo
	ResolveWindow(model string, explicitLimit int64) ModelWindowInfo
}

// StaticCatalog 為內建的靜態模型容量目錄
type StaticCatalog struct {
	windows map[string]int64
}

// NewStaticCatalog 建立包含已驗證主流模型之靜態目錄
func NewStaticCatalog() *StaticCatalog {
	return &StaticCatalog{
		windows: map[string]int64{
			// OpenAI
			"gpt-4o":                      128000,
			"gpt-4o-mini":                 128000,
			"gpt-4-turbo":                 128000,
			"gpt-4":                       8192,
			"gpt-3.5-turbo":               16385,
			"o1":                          200000,
			"o1-preview":                  128000,
			"o1-mini":                     128000,
			"o3-mini":                     200000,

			// Anthropic Claude
			"claude-3-5-sonnet-20241022": 200000,
			"claude-3-5-sonnet-20240620": 200000,
			"claude-3-5-haiku-20241022":  200000,
			"claude-3-opus-20240229":     200000,
			"claude-3-sonnet-20240229":   200000,
			"claude-3-haiku-20240307":    200000,

			// Google Gemini
			"gemini-1.5-pro":             1000000,
			"gemini-1.5-flash":           1000000,
			"gemini-2.0-flash":           1000000,
		},
	}
}

// GetModelWindow 自靜態目錄查詢模型窗口限制
func (c *StaticCatalog) GetModelWindow(model string) ModelWindowInfo {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ModelWindowInfo{
			Model:       model,
			MaxTokens:   0,
			LimitSource: LimitSourceUnknown,
		}
	}

	if limit, ok := c.windows[trimmed]; ok {
		return ModelWindowInfo{
			Model:       model,
			MaxTokens:   limit,
			LimitSource: LimitSourceCatalog,
		}
	}

	return ModelWindowInfo{
		Model:       model,
		MaxTokens:   0,
		LimitSource: LimitSourceUnknown,
	}
}

// ResolveWindow 依顯式配置優先原則解析有效模型窗口
// 1. 若 explicitLimit > 0，顯式上限優先，來源為 config
// 2. 否則若命中靜態目錄，來源為 catalog
// 3. 否則來源為 unknown，上限為 0
func (c *StaticCatalog) ResolveWindow(model string, explicitLimit int64) ModelWindowInfo {
	if explicitLimit > 0 {
		return ModelWindowInfo{
			Model:       model,
			MaxTokens:   explicitLimit,
			LimitSource: LimitSourceConfig,
		}
	}

	return c.GetModelWindow(model)
}
