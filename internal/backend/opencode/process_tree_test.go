package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// markerSleepPIDs 回傳 argv 為 sleep <marker> 的行程，涵蓋其他 session 與 PID namespace 裡的行程。
func markerSleepPIDs(markers ...string) map[string][]int {
	found := map[string][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(args) != 2 || filepath.Base(args[0]) != "sleep" {
			continue
		}
		for _, m := range markers {
			if args[1] == m {
				found[m] = append(found[m], pid)
			}
		}
	}
	return found
}

func waitMarkers(t *testing.T, timeout time.Duration, cond func(map[string][]int) bool, markers ...string) map[string][]int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		found := markerSleepPIDs(markers...)
		if cond(found) || time.Now().After(deadline) {
			return found
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// 2026-10-28 issue-74 SCN-001、SCN-003：取消要回收 group 成員的整棵子孫樹，
// 包含另開 session、另開 process group，以及 Codex 那樣另開 PID namespace 且 init 脫離 group 的命令；
// 不是子孫的無關行程必須保留。
func TestKillProcessGroupReapsDetachedDescendants(t *testing.T) {
	base := 40000 + os.Getpid()%9000
	marker := func(i int) string { return strconv.Itoa(base*10 + i) }
	sessionChild, sessionGrandchild, foreground, unrelated, nsChild := marker(1), marker(2), marker(3), marker(4), marker(5)
	markers := []string{sessionChild, sessionGrandchild, foreground, unrelated}

	script := "setsid sleep " + sessionChild + " & setsid sh -c 'sleep " + sessionGrandchild + " & wait' & sleep " + foreground + " & "
	// Codex 0.163.0 的命令跑在 bwrap 另開的 process group 與 PID namespace 裡，namespace 的 init 另開 session（2026-10-28 實測）。
	if exec.Command("bwrap", "--unshare-pid", "--dev-bind", "/", "/", "--", "true").Run() == nil {
		script += "setsid bwrap --unshare-pid --dev-bind / / -- sh -c 'sleep " + nsChild + " & wait' & "
		markers = append(markers, nsChild)
	} else {
		t.Log("bwrap cannot create a PID namespace here; skipping the PID namespace case")
	}
	script += "wait"

	other := exec.Command("sleep", unrelated)
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", script)
	ConfigureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, pids := range markerSleepPIDs(markers...) {
			for _, pid := range pids {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		_ = cmd.Wait()
		_ = other.Wait()
	})

	all := func(found map[string][]int) bool { return len(found) == len(markers) }
	if found := waitMarkers(t, 5*time.Second, all, markers...); !all(found) {
		t.Fatalf("not all commands started: %v", found)
	}

	if err := KillProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("KillProcessGroup: %v", err)
	}

	onlyUnrelated := func(found map[string][]int) bool { return len(found) == 1 && len(found[unrelated]) == 1 }
	found := waitMarkers(t, 5*time.Second, onlyUnrelated, markers...)
	delete(found, unrelated)
	if len(found) != 0 {
		t.Errorf("descendants survived cancellation: %v", found)
	}
	if len(markerSleepPIDs(unrelated)[unrelated]) != 1 {
		t.Error("unrelated process outside the task tree was killed")
	}
}

// 2026-10-28 issue-74：取消期間仍不斷 fork 另開 session 的子行程時，凍結後重新掃描，也要全部回收。
func TestKillProcessGroupReapsForkingDescendants(t *testing.T) {
	m := strconv.Itoa((40000+os.Getpid()%9000)*10 + 6)
	// 節流：不節流時幾秒內會累積數千個行程，拖慢同時執行的其他套件（2026-10-28 gate 實測）。
	cmd := exec.Command("sh", "-c", "while :; do setsid sleep "+m+" & sleep 0.01; done")
	ConfigureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		for _, pid := range markerSleepPIDs(m)[m] {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	})
	if found := waitMarkers(t, 5*time.Second, func(f map[string][]int) bool { return len(f[m]) >= 20 }, m); len(found[m]) < 20 {
		t.Fatalf("fork loop did not start: %v", found)
	}

	if err := KillProcessGroup(cmd.Process.Pid); err != nil {
		t.Fatalf("KillProcessGroup: %v", err)
	}
	found := waitMarkers(t, 5*time.Second, func(f map[string][]int) bool { return len(f) == 0 }, m)
	if len(found) != 0 {
		t.Errorf("%d forked descendants survived cancellation", len(found[m]))
	}
}
