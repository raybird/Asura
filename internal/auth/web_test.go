package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	"asura/internal/auth"
)

// 2026-09-13 WEB-002：清單內連結不能把授權擴大到清單外。
func TestWEB002Unit(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	policy := auth.NewPolicy([]string{root})
	if err := policy.AuthorizeWorkspace(root); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(root, "escape"), filepath.Join(root, "escape", "new-project"), root + "/escape/..", root + "/escape/../new-project"} {
		if err := policy.AuthorizeWorkspace(target); err == nil {
			t.Errorf("outside symlink authorized: %s", target)
		}
	}
}
