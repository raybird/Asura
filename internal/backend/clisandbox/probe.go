package clisandbox

import (
	"asura/internal/backend"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// 2026-10-04：必要檔案的 context-aware 雜湊，不複製或寫入檔案。
// 2026-10-21：issue-73 起只回傳雜湊，由 adapter 對照內建或已驗證的身分。
func ProbeFileDigest(ctx context.Context, path string, missing backend.ProbeReason) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", &backend.ProbeError{Reason: missing}
	}
	if !info.Mode().IsRegular() {
		return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", &backend.ProbeError{Reason: backend.ConfigurationInvalid}
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), ctx.Err()
}
func ProbeSandboxDependency(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return &backend.ProbeError{Reason: backend.DependencyNotFound}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return &backend.ProbeError{Reason: backend.DependencyNotFound}
	}
	return nil
}
