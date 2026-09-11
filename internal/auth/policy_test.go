package auth_test

import (
	"errors"
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
