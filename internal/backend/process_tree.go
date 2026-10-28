package backend

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// KillProcessTree 回收 process group pgid 的所有成員，以及沿 PPID 找到的整棵子孫樹。
// 模型啟動的命令會另開 session 或 process group（OpenCode 的 bash 工具），或跑在 runtime 另開的 PID namespace 裡（Codex 的 bwrap）；
// 只送 kill(-pgid) 時它們會被 PID 1 接手繼續執行（2026-10-28 issue-74）。
// 先以 SIGSTOP 凍結並重新掃描到集合不再變大，之後才送 SIGKILL：凍結後的行程不能 fork，PID 也不會被重用。
// 凍結前仍有一段窗口：尚未凍結的行程可以 fork 出另開 session 的子行程後自己先結束，該子行程會被 PID 1 接手，
// 屬於已脫離行程樹的情況（issue-74 TBD-1）。
// 凍結最多 maxStopRounds 輪，SIGSTOP 失敗（例如不同 uid）又持續 fork 的子孫不會讓取消停不下來（2026-10-28 PR #82 審查）。
// 呼叫端負責先確認 pgid 屬於該任務；子孫只從 group 成員往下找。
func KillProcessTree(pgid int) error {
	if pgid <= 0 {
		return errors.New("invalid pgid")
	}
	stopped := map[int]bool{}
	for round := 0; round < maxStopRounds; round++ {
		grew := false
		for _, pid := range processTree(pgid) {
			if !stopped[pid] {
				_ = syscall.Kill(pid, syscall.SIGSTOP)
				stopped[pid] = true
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	for pid := range stopped {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

const maxStopRounds = 64

// processTree 依 BFS 順序回傳 group pgid 的成員與其所有子孫，父行程在子行程之前。
func processTree(pgid int) []int {
	children := map[int][]int{}
	var tree []int
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		i := strings.LastIndexByte(string(data), ')')
		if i < 0 {
			continue
		}
		// ')' 之後依序為 state、ppid、pgrp
		fields := strings.Fields(string(data[i+1:]))
		if len(fields) < 3 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		pgrp, _ := strconv.Atoi(fields[2])
		children[ppid] = append(children[ppid], pid)
		if pgrp == pgid {
			tree = append(tree, pid)
		}
	}
	seen := map[int]bool{}
	var out []int
	for i := 0; i < len(tree); i++ {
		if seen[tree[i]] {
			continue
		}
		seen[tree[i]] = true
		out = append(out, tree[i])
		tree = append(tree, children[tree[i]]...)
	}
	return out
}
