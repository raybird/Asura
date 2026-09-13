package main

import (
	"os"
	"path/filepath"
)

// DefaultSocketPath 回傳統一預設的 Unix Socket 路徑
// 優先序：環境變數 ASURA_SOCKET -> ~/.asura/asura.sock -> /tmp/asura.sock
func DefaultSocketPath() string {
	if env := os.Getenv("ASURA_SOCKET"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil {
		asuraDir := filepath.Join(home, ".asura")
		_ = os.MkdirAll(asuraDir, 0755)
		return filepath.Join(asuraDir, "asura.sock")
	}
	return filepath.Join(os.TempDir(), "asura.sock")
}
