package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// 2026-10-09：issue-45 Task 1.1，version 介面（DIST-001／002／004／016 的版本來源）。

func TestVersionUnstampedBuild(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := RunCLI([]string{"version"}, &out, &stderr); code != 0 {
		t.Fatalf("code=%d %s", code, stderr.String())
	}
	want := "asura dev (commit unknown, " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
	out.Reset()
	if code := RunCLI([]string{"version", "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("json code=%d %s", code, stderr.String())
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not json: %q", out.String())
	}
	if len(got) != 4 || got["version"] != "dev" || got["commit"] != "unknown" || got["os"] != runtime.GOOS || got["arch"] != runtime.GOARCH {
		t.Fatalf("json=%v", got)
	}
	if code := RunCLI([]string{"version", "extra"}, &out, &stderr); code != 2 {
		t.Fatalf("usage error code=%d", code)
	}
}

func TestVersionStampedBuild(t *testing.T) {
	ldflags := "-X main.version=v9.8.7 -X main.commit=0123abcd"
	bin := filepath.Join(t.TempDir(), "asura")
	if out, err := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	out, err := exec.Command(bin, "version", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil || got["version"] != "v9.8.7" || got["commit"] != "0123abcd" || got["os"] != runtime.GOOS || got["arch"] != runtime.GOARCH {
		t.Fatalf("stamped json=%q err=%v", out, err)
	}
	human, err := exec.Command(bin, "version").Output()
	if err != nil || string(human) != "asura v9.8.7 (commit 0123abcd, "+runtime.GOOS+"/"+runtime.GOARCH+")\n" {
		t.Fatalf("stamped human=%q err=%v", human, err)
	}
}
