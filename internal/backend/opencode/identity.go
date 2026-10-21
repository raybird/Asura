package opencode

import (
	"path/filepath"

	"asura/internal/backend/verification"
)

// admitOpenCode 2026-10-21：issue-73，內建 expected 版本精確比對；其他版本需要本機驗證紀錄。
func admitOpenCode(version, bin, expected, recordsPath string) error {
	return verification.Admit("opencode", version, func() (string, error) {
		real, err := filepath.EvalSymlinks(bin)
		if err != nil {
			return "", err
		}
		return verification.FileDigest(real)
	}, []verification.Identity{{Runtime: "opencode", Version: expected}}, recordsPath)
}
