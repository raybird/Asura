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

// 2026-10-04：必要檔案的 context-aware 校驗，不複製或寫入檔案。
func VerifyProbeFile(ctx context.Context, path, want string, missing backend.ProbeReason) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return &backend.ProbeError{Reason: missing}
	}
	if !info.Mode().IsRegular() {
		return &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	f, err := os.Open(path)
	if err != nil {
		return &backend.ProbeError{Reason: backend.ConfigurationInvalid}
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return &backend.ProbeError{Reason: backend.ConfigurationInvalid}
		}
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != want {
		return &backend.ProbeError{Reason: backend.VersionFailed}
	}
	return ctx.Err()
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
