package main

// 2026-10-09：issue-45 Task 4.2，asura doctor readiness（DIST-006／007）。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asura/internal/backend"
)

func TestDoctorStatusFromChecks(t *testing.T) {
	cases := []struct {
		levels []checkLevel
		status string
		code   int
	}{
		{[]checkLevel{levelOK, levelInfo}, "READY", 0},
		{[]checkLevel{levelOK, levelWarn, levelInfo}, "DEGRADED", 0},
		{[]checkLevel{levelOK, levelWarn, levelFail}, "NOT_READY", 1},
	}
	for _, c := range cases {
		var checks []doctorCheck
		for _, l := range c.levels {
			checks = append(checks, doctorCheck{level: l})
		}
		if status, code := doctorStatus(checks); status != c.status || code != c.code {
			t.Fatalf("%v → %s/%d want %s/%d", c.levels, status, code, c.status, c.code)
		}
	}
}

func TestDoctorModesFromDescriptors(t *testing.T) {
	entry := func(id string, caps ...backend.Capability) backend.RuntimeInventoryEntry {
		return backend.RuntimeInventoryEntry{RuntimeID: id, AvailabilityState: backend.Available, Descriptor: &backend.RuntimeDescriptor{ID: id, Capabilities: caps}}
	}
	writeOnly := []backend.RuntimeInventoryEntry{entry("codex", backend.CapWorkspaceWrite)}
	if got := availableModes(writeOnly); strings.Join(got, ",") != "direct" {
		t.Fatalf("write-only modes=%v", got)
	}
	both := append(writeOnly, entry("opencode", backend.CapReadOnlySandbox))
	if got := availableModes(both); strings.Join(got, ",") != "direct,three-face" {
		t.Fatalf("modes=%v", got)
	}
	if got := availableModes([]backend.RuntimeInventoryEntry{{RuntimeID: "codex", AvailabilityState: backend.Unavailable}}); len(got) != 0 {
		t.Fatalf("unavailable modes=%v", got)
	}
}

// doctorEnv 建立隔離的 HOME／XDG 與只含指定 stub 的 PATH；stub 把收到的參數記到 calls.log。
func doctorEnv(t *testing.T, stubs map[string]string) (xdgEnv, string) {
	t.Helper()
	e := newXDGEnv(t)
	e.setenv(t)
	bin := t.TempDir()
	log := filepath.Join(e.root, "calls.log")
	for name, version := range stubs {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + log + "\n[ \"$1\" = --version ] || exit 9\necho '" + version + "'\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Chdir(t.TempDir())
	return e, log
}

func runDoctor(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := RunCLI(append([]string{"doctor"}, args...), &out, &stderr)
	return code, out.String() + stderr.String()
}

func TestDoctorDegradedWithOneRuntime(t *testing.T) {
	e, log := doctorEnv(t, map[string]string{"codex": "codex-cli 0.155.0", "opencode": "0.0.1"})
	code, out := runDoctor(t)
	if code != 0 || !strings.Contains(out, "Asura doctor: DEGRADED\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	for _, want := range []string{
		"[ok]   codex: available version=codex-cli 0.155.0 executable=" + filepath.Join(os.Getenv("PATH"), "codex"),
		"[warn] opencode: unavailable reason=version_unverified executable=" + filepath.Join(os.Getenv("PATH"), "opencode"),
		"[warn] omp: unavailable reason=",
		"[warn] user service: systemctl not found",
		"[info] daemon socket: no listener at",
		"[info] config: none at " + filepath.Join(e.configHome, "asura", "config.toml") + "; using v1.0 defaults",
		"Modes: direct",
		"permission check only; nothing was written",
		"Doctor does not verify runtime login, quota or model responses.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	calls, _ := os.ReadFile(log)
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if !strings.HasSuffix(line, " --version") {
			t.Fatalf("runtime invoked beyond version probe: %q", line)
		}
	}
}

func TestDoctorNotReady(t *testing.T) {
	t.Run("no-runtime", func(t *testing.T) {
		doctorEnv(t, nil)
		code, out := runDoctor(t)
		if code != 1 || !strings.Contains(out, "Asura doctor: NOT_READY\n") || !strings.Contains(out, "[fail] runtimes: no runtime available") {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})
	t.Run("invalid-config", func(t *testing.T) {
		e, _ := doctorEnv(t, map[string]string{"codex": "codex-cli 0.155.0"})
		e.writeConfig(t, "schema_version = 1\ntoken = \"SECRET-SENTINEL-d0c\"\n")
		code, out := runDoctor(t)
		if code != 1 || !strings.Contains(out, "NOT_READY") || !strings.Contains(out, "[fail] config: ") || strings.Contains(out, "SECRET-SENTINEL") {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})
	t.Run("data-dir-not-writable", func(t *testing.T) {
		e, _ := doctorEnv(t, map[string]string{"codex": "codex-cli 0.155.0"})
		locked := filepath.Join(e.root, "locked")
		os.Mkdir(locked, 0500)
		t.Cleanup(func() { os.Chmod(locked, 0700) })
		e.writeConfig(t, "schema_version = 1\ndb = \""+filepath.Join(locked, "data", "asura.db")+"\"\n")
		code, out := runDoctor(t)
		if code != 1 || !strings.Contains(out, "[fail] database directory: "+filepath.Join(locked, "data")+" is not writable") {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})
	t.Run("missing-allowed-root", func(t *testing.T) {
		e, _ := doctorEnv(t, map[string]string{"codex": "codex-cli 0.155.0"})
		e.writeConfig(t, "schema_version = 1\nallowed_roots = [\""+filepath.Join(e.root, "gone")+"\"]\n")
		code, out := runDoctor(t)
		if code != 1 || !strings.Contains(out, "[fail] allowed root: "+filepath.Join(e.root, "gone")+" is not a directory") {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})
}

func TestDoctorUsageError(t *testing.T) {
	doctorEnv(t, nil)
	if code, _ := runDoctor(t, "extra"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
