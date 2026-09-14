package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"asura/internal/auth"
)

// SCN-002: 拒絕超出授權範圍的執行
func TestAuthorizeWorkspace_Denied(t *testing.T) {
	policy := auth.NewPolicy([]string{
		"/home/kevin/Documents/RCodes",
	})

	tests := []struct {
		name        string
		targetPath  string
		expectError error
	}{
		{
			name:        "sensitive root directory",
			targetPath:  "/etc",
			expectError: auth.ErrDisallowedWorkspace,
		},
		{
			name:        "outside allowed roots",
			targetPath:  "/var/log",
			expectError: auth.ErrDisallowedWorkspace,
		},
		{
			name:        "parent traversal attempt",
			targetPath:  "/home/kevin/Documents/RCodes/../..",
			expectError: auth.ErrDisallowedWorkspace,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.AuthorizeWorkspace(tc.targetPath)
			if !errors.Is(err, tc.expectError) {
				t.Fatalf("expected error %v, got %v", tc.expectError, err)
			}
		})
	}
}

func TestAuthorizeWorkspace_Allowed(t *testing.T) {
	policy := auth.NewPolicy([]string{
		"/home/kevin/Documents/RCodes",
	})

	tests := []struct {
		name       string
		targetPath string
	}{
		{
			name:       "exact allowed root",
			targetPath: "/home/kevin/Documents/RCodes",
		},
		{
			name:       "subproject directory",
			targetPath: "/home/kevin/Documents/RCodes/Asura",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.AuthorizeWorkspace(tc.targetPath)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.targetPath, err)
			}
		})
	}
}

// TestAuthorizeCanonicalWorkspace: 同次驗證授權並解析出標準工作區路徑 (MEM-003, MEM-004, P1-T2.c)
func TestAuthorizeCanonicalWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	projA := filepath.Join(tmpDir, "projA")
	if err := os.MkdirAll(projA, 0755); err != nil {
		t.Fatal(err)
	}

	subC := filepath.Join(projA, "subC")
	if err := os.MkdirAll(subC, 0755); err != nil {
		t.Fatal(err)
	}

	aliasA := filepath.Join(tmpDir, "aliasA")
	if err := os.Symlink(projA, aliasA); err != nil {
		t.Fatal(err)
	}

	escapedLink := filepath.Join(tmpDir, "escaped")
	if err := os.Symlink(outsideDir, escapedLink); err != nil {
		t.Fatal(err)
	}

	policy := auth.NewPolicy([]string{tmpDir})

	t.Run("正向授權取得 canonical scope", func(t *testing.T) {
		canonical, err := policy.AuthorizeCanonicalWorkspace(projA)
		if err != nil {
			t.Fatalf("單元行為紅燈 (尚未實作)：AuthorizeCanonicalWorkspace 失敗：%v (待 P2-T2 實作)", err)
		}
		expectedClean := filepath.Clean(projA)
		if canonical != expectedClean {
			t.Fatalf("expected canonical %q, got %q", expectedClean, canonical)
		}
	})

	t.Run("有效別名解析至同一專案 canonical scope", func(t *testing.T) {
		canonical, err := policy.AuthorizeCanonicalWorkspace(aliasA)
		if err != nil {
			t.Fatalf("單元行為紅燈 (尚未實作)：AuthorizeCanonicalWorkspace 失敗：%v (待 P2-T2 實作)", err)
		}
		expectedClean := filepath.Clean(projA)
		if canonical != expectedClean {
			t.Fatalf("別名應解析至目標專案之 canonical scope，expected %q, got %q", expectedClean, canonical)
		}
	})

	t.Run("子目錄 C 獨立且不等於專案 A 之 canonical scope", func(t *testing.T) {
		canonicalC, err := policy.AuthorizeCanonicalWorkspace(subC)
		if err != nil {
			t.Fatalf("單元行為紅燈 (尚未實作)：AuthorizeCanonicalWorkspace 失敗：%v (待 P2-T2 實作)", err)
		}
		canonicalA, _ := policy.AuthorizeCanonicalWorkspace(projA)
		if canonicalC == canonicalA {
			t.Fatalf("隔離漏洞：子目錄 C 的 canonical scope 不得等於專案 A 的 scope")
		}
	})

	t.Run("越界符號連結拒絕", func(t *testing.T) {
		_, err := policy.AuthorizeCanonicalWorkspace(escapedLink)
		if err == nil {
			t.Fatalf("安全漏洞：越界符號連結未被拒絕")
		}
		if !errors.Is(err, auth.ErrDisallowedWorkspace) {
			t.Fatalf("單元行為紅燈 (尚未實作)：expected ErrDisallowedWorkspace, got %v", err)
		}
	})

	t.Run("未授權外部目錄拒絕", func(t *testing.T) {
		_, err := policy.AuthorizeCanonicalWorkspace(outsideDir)
		if err == nil {
			t.Fatalf("安全漏洞：外部目錄未被拒絕")
		}
		if !errors.Is(err, auth.ErrDisallowedWorkspace) {
			t.Fatalf("單元行為紅燈 (尚未實作)：expected ErrDisallowedWorkspace, got %v", err)
		}
	})
}
