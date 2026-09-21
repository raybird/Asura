package contextwin

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// EnvContextLimit 為環境變數名稱，設定全域預算上限（正整數 Token 數）
	EnvContextLimit = "ASURA_CONTEXT_LIMIT"
	// EnvContextPolicy 為環境變數名稱，設定准入超量政策（observe 或 reject）
	EnvContextPolicy = "ASURA_CONTEXT_POLICY"
	// EnvContextReserveRatio 為環境變數名稱，設定輸出保留比例（0.0 ~ 1.0）
	EnvContextReserveRatio = "ASURA_CONTEXT_RESERVE_RATIO"
)

// Config 定義 Context Window 管理之不可變設定
type Config struct {
	ContextLimit int64           `json:"context_limit"`
	Policy       AdmissionPolicy `json:"policy"`
	ReserveRatio float64         `json:"reserve_ratio"`
}

// LoadConfig 自作業系統環境變數讀取並驗證設定
func LoadConfig() (Config, error) {
	return LoadConfigWithLookup(os.LookupEnv)
}

// LoadConfigWithLookup 自自訂 lookup 函式讀取並嚴格校驗設定
func LoadConfigWithLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		ContextLimit: 0,
		Policy:       PolicyObserve, // 使用者核准決策：預設為 observe
		ReserveRatio: DefaultReserveRatio,
	}

	// 1. 讀取 ASURA_CONTEXT_LIMIT
	if val, ok := lookup(EnvContextLimit); ok && strings.TrimSpace(val) != "" {
		trimmed := strings.TrimSpace(val)
		limit, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || limit <= 0 {
			return Config{}, fmt.Errorf("invalid %s=%q: must be a positive integer", EnvContextLimit, val)
		}
		cfg.ContextLimit = limit
	}

	// 2. 讀取 ASURA_CONTEXT_POLICY
	if val, ok := lookup(EnvContextPolicy); ok && strings.TrimSpace(val) != "" {
		trimmed := strings.ToLower(strings.TrimSpace(val))
		switch AdmissionPolicy(trimmed) {
		case PolicyObserve:
			cfg.Policy = PolicyObserve
		case PolicyReject:
			cfg.Policy = PolicyReject
		default:
			return Config{}, fmt.Errorf("invalid %s=%q: must be 'observe' or 'reject'", EnvContextPolicy, val)
		}
	}

	// 3. 讀取 ASURA_CONTEXT_RESERVE_RATIO（可選）
	if val, ok := lookup(EnvContextReserveRatio); ok && strings.TrimSpace(val) != "" {
		trimmed := strings.TrimSpace(val)
		ratio, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || ratio <= 0 || ratio >= 1.0 {
			return Config{}, fmt.Errorf("invalid %s=%q: must be a float between 0.0 and 1.0", EnvContextReserveRatio, val)
		}
		cfg.ReserveRatio = ratio
	}

	return cfg, nil
}
