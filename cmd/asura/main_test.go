package main

import (
	"os"
	"testing"
)

// 2026-10-09：issue-45，測試不得讀到開發者自己的 operator config；需要 config 的測試自行設定 XDG_CONFIG_HOME。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "asura-cli-test-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
