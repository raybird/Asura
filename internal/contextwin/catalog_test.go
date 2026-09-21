package contextwin_test

import (
	"testing"

	"asura/internal/contextwin"
)

func TestStaticCatalog_Lookup(t *testing.T) {
	cat := contextwin.NewStaticCatalog()

	tests := []struct {
		model       string
		wantFound   bool
		wantTokens  int64
		wantSource  contextwin.LimitSource
	}{
		{
			model:      "gpt-4o",
			wantFound:  true,
			wantTokens: 128000,
			wantSource: contextwin.LimitSourceCatalog,
		},
		{
			model:      "claude-3-5-sonnet-20241022",
			wantFound:  true,
			wantTokens: 200000,
			wantSource: contextwin.LimitSourceCatalog,
		},
		{
			model:      "unknown-custom-model",
			wantFound:  false,
			wantTokens: 0,
			wantSource: contextwin.LimitSourceUnknown,
		},
		{
			model:      "",
			wantFound:  false,
			wantTokens: 0,
			wantSource: contextwin.LimitSourceUnknown,
		},
	}

	for _, tt := range tests {
		info := cat.GetModelWindow(tt.model)
		if tt.wantFound && info.LimitSource != contextwin.LimitSourceCatalog {
			t.Errorf("expected catalog source for %s, got %s", tt.model, info.LimitSource)
		}
		if !tt.wantFound && info.LimitSource != contextwin.LimitSourceUnknown {
			t.Errorf("expected unknown source for %s, got %s", tt.model, info.LimitSource)
		}
		if info.MaxTokens != tt.wantTokens {
			t.Errorf("tokens for %s: got %d, want %d", tt.model, info.MaxTokens, tt.wantTokens)
		}
	}
}

func TestCatalog_ResolveWindow_ExplicitPriority(t *testing.T) {
	cat := contextwin.NewStaticCatalog()

	// 顯式上限優先於已知模型
	info := cat.ResolveWindow("gpt-4o", 50000)
	if info.LimitSource != contextwin.LimitSourceConfig {
		t.Errorf("expected config limit source, got %s", info.LimitSource)
	}
	if info.MaxTokens != 50000 {
		t.Errorf("expected max tokens 50000, got %d", info.MaxTokens)
	}

	// 顯式上限對未知模型依然生效
	infoUnknown := cat.ResolveWindow("custom-v1", 32000)
	if infoUnknown.LimitSource != contextwin.LimitSourceConfig {
		t.Errorf("expected config limit source, got %s", infoUnknown.LimitSource)
	}
	if infoUnknown.MaxTokens != 32000 {
		t.Errorf("expected max tokens 32000, got %d", infoUnknown.MaxTokens)
	}

	// 無顯式上限（<= 0）降級至目錄
	infoCatalog := cat.ResolveWindow("gpt-4o", 0)
	if infoCatalog.LimitSource != contextwin.LimitSourceCatalog {
		t.Errorf("expected catalog limit source, got %s", infoCatalog.LimitSource)
	}
	if infoCatalog.MaxTokens != 128000 {
		t.Errorf("expected max tokens 128000, got %d", infoCatalog.MaxTokens)
	}

	// 無顯式上限且目錄未命中時為 unknown
	infoBypassed := cat.ResolveWindow("custom-v1", 0)
	if infoBypassed.LimitSource != contextwin.LimitSourceUnknown {
		t.Errorf("expected unknown limit source, got %s", infoBypassed.LimitSource)
	}
	if infoBypassed.MaxTokens != 0 {
		t.Errorf("expected max tokens 0, got %d", infoBypassed.MaxTokens)
	}
}
