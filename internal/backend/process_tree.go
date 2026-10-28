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
// 先以 SIGSTOP 凍結並重新掃描到集合不再變大，凍結的行程不能 fork 也不會結束，PID 不會被重用；之後才送 SIGKILL。
// 呼叫端負責先確認 pgid 屬於該任務；子孫只從 group 成員往下找。
func KillProcessTree(pgid int) error {
	if pgid <= 0 {
		return errors.New("invalid pgid")
	}
	stopped := map[int]bool{}
	for {
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

// processTree 回傳 group pgid 的成員與其所有子孫。
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
	for i := 0; i < len(tree); i++ {
		if seen[tree[i]] {
			continue
		}
		seen[tree[i]] = true
		tree = append(tree, children[tree[i]]...)
	}
	out := make([]int, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	return out
}
