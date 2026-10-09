package main

// 2026-10-09：issue-45 Task 4.3，doctor 的 secret 安全、唯讀邊界與有界執行（DIST-008）。

import (
	"bytes"
	"crypto/sha256"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const doctorSecret = "SECRET-SENTINEL-d0c7"

// tree 回傳目錄下所有路徑與內容雜湊，用於比對前後狀態。
func tree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	m := map[string][32]byte{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		var sum [32]byte
		if info.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			sum = sha256.Sum256(b)
		}
		m[p] = sum
		return nil
	})
	return m
}

func diffTree(before, after map[string][32]byte) []string {
	var d []string
	for p, h := range after {
		if old, ok := before[p]; !ok || old != h {
			d = append(d, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			d = append(d, p+" (removed)")
		}
	}
	sort.Strings(d)
	return d
}

func TestDoctorSecretSafetyAndReadOnly(t *testing.T) {
	e := newXDGEnv(t)
	e.setenv(t)
	socket := filepath.Join(e.runtimeDir, "asura", "asura.sock")
	db := filepath.Join(e.dataHome, "asura", "asura.db")
	os.MkdirAll(filepath.Dir(socket), 0700)
	os.MkdirAll(filepath.Dir(db), 0700)
	os.WriteFile(db, []byte("sqlite-fixture"), 0600)
	e.writeConfig(t, "schema_version = 1\n")
	os.MkdirAll(filepath.Join(e.home, ".codex"), 0700)
	os.WriteFile(filepath.Join(e.home, ".codex", "auth.json"), []byte(`{"token":"`+doctorSecret+`"}`), 0600)

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("OPENAI_API_KEY", doctorSecret)
	t.Setenv("ASURA_WEB_KEY", doctorSecret)
	bin := t.TempDir()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		// 失敗的 probe 把 secret 印到 stderr 與 stdout。
		"codex": "#!/bin/sh\necho 'codex-cli 0.155.0 " + doctorSecret + "'\necho " + doctorSecret + " >&2\nexit 3\n",
		// 掛住的 probe：子行程持有輸出管線。
		"opencode": "#!/bin/sh\necho " + doctorSecret + " >&2\n" + sleep + " 30\necho 1.18.23\n",
		// 掛住的 systemctl：子行程持有輸出管線。
		"systemctl": "#!/bin/sh\necho " + doctorSecret + " >&2\n" + sleep + " 30\necho active\n",
	}
	for name, body := range stubs {
		os.WriteFile(filepath.Join(bin, name), []byte(body), 0700)
	}
	t.Setenv("PATH", bin)
	cwd := t.TempDir()
	t.Chdir(cwd)

	// 掛住的 daemon peer：接受連線但永不回應。
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	before := tree(t, e.root)
	cwdBefore := tree(t, cwd)
	start := time.Now()
	var out, stderr bytes.Buffer
	code := RunCLI([]string{"doctor"}, &out, &stderr)
	elapsed := time.Since(start)
	all := out.String() + stderr.String()

	if elapsed > 11*time.Second {
		t.Fatalf("doctor exceeded its deadline: %v", elapsed)
	}
	if strings.Contains(all, doctorSecret) {
		t.Fatalf("secret leaked:\n%s", all)
	}
	if code != 1 || !strings.Contains(all, "NOT_READY") {
		t.Fatalf("code=%d (all runtimes fail):\n%s", code, all)
	}
	// 2026-10-09：外部審查 P2，只做連線時不宣稱 daemon 身分或健康狀態。
	if !strings.Contains(all, "daemon socket: accepting connections at "+socket+" (daemon identity and health not verified)") {
		t.Fatalf("socket wording:\n%s", all)
	}
	if !strings.Contains(all, "user service: asura.service status unavailable (timeout)") {
		t.Fatalf("hung systemctl not reported:\n%s", all)
	}
	if d := diffTree(before, tree(t, e.root)); len(d) > 0 {
		t.Fatalf("doctor changed user state: %v", d)
	}
	if d := diffTree(cwdBefore, tree(t, cwd)); len(d) > 0 {
		t.Fatalf("doctor wrote to cwd: %v", d)
	}
	if left, _ := os.ReadDir(tmp); len(left) > 0 {
		t.Fatalf("private temp residue: %v", left)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 600 {
			t.Fatalf("unbounded line: %d bytes", len(line))
		}
	}
}

func TestDoctorBoundsLongDetails(t *testing.T) {
	doctorEnv(t, nil)
	long := "/" + strings.Repeat("x", 2000)
	t.Setenv("ASURA_SOCKET", long)
	var out, stderr bytes.Buffer
	RunCLI([]string{"doctor"}, &out, &stderr)
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > 600 {
			t.Fatalf("unbounded line: %d bytes", len(line))
		}
	}
	if !strings.Contains(out.String(), "(truncated)") {
		t.Fatalf("long detail not marked truncated:\n%s", out.String())
	}
	// 2026-10-09：review N2，多位元組路徑截斷後仍是合法 UTF-8。
	t.Setenv("ASURA_SOCKET", "/"+strings.Repeat("界", 400))
	out.Reset()
	RunCLI([]string{"doctor"}, &out, &stderr)
	if !utf8.ValidString(out.String()) || !strings.Contains(out.String(), "(truncated)") {
		t.Fatal("truncation produced invalid UTF-8")
	}
}
