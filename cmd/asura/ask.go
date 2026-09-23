package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(val string) error {
	parts := strings.Split(val, ",")
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			exists := false
			for _, existing := range *s {
				if existing == trimmed {
					exists = true
					break
				}
			}
			if !exists {
				*s = append(*s, trimmed)
			}
		}
	}
	return nil
}

func handleAsk(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(stderr)

	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
	workspace := fs.String("workspace", "", "target workspace directory (defaults to current directory)")
	customID := fs.String("id", "", "optional custom task/request ID")
	wait := fs.Bool("wait", false, "wait for task to complete")
	mode := fs.String("mode", string(pipeline.ModeDirect), "execution mode (direct or three-face)")
	backendFlag := fs.String("backend", "opencode", "execution backend (opencode or codex)")
	memoryRecall := fs.String("memory-recall", "off", "project memory recall (off or bounded)")
	var memoryIDs stringSliceFlag
	fs.Var(&memoryIDs, "memory-id", "specify project memory ID to include in task context (can be repeated or comma-separated)")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *memoryRecall != "off" && *memoryRecall != "bounded" {
		fmt.Fprintln(stderr, "error: memory-recall must be off or bounded")
		return 1
	}
	recallMode := *memoryRecall
	if recallMode == "off" {
		recallMode = ""
	}

	if !pipeline.IsValidExecutionMode(pipeline.ExecutionMode(*mode)) {
		fmt.Fprintf(stderr, "error: invalid mode %q (must be direct or three-face)\n", *mode)
		return 1
	}

	bID := *backendFlag
	if bID == "" {
		bID = "opencode"
	}
	if bID != "opencode" && bID != "codex" {
		fmt.Fprintf(stderr, "error: unknown backend %q (supported: opencode, codex)\n", bID)
		return 1
	}

	if *sockPath == "" {
		*sockPath = DefaultSocketPath()
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(stderr, "error: prompt is required")
		return 1
	}
	prompt := strings.Join(remaining, " ")

	targetWorkspace := *workspace
	if targetWorkspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "error: failed to get current directory: %v\n", err)
			return 1
		}
		targetWorkspace = cwd
	}

	absWorkspace, err := filepath.Abs(targetWorkspace)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid workspace path: %v\n", err)
		return 1
	}

	// 連線至 Daemon；不可用時直接報錯退出，嚴禁自動啟動或回退為獨立執行器
	client, err := daemon.NewClient(*sockPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
		return 1
	}
	defer client.Close()

	ctx := context.Background()
	task, err := client.Submit(ctx, engine.SubmitRequest{
		ID:           *customID,
		Workspace:    absWorkspace,
		Prompt:       prompt,
		Mode:         *mode,
		Backend:      bID,
		MemoryIDs:    memoryIDs,
		MemoryRecall: recallMode,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Task ID: %s\n", task.ID)

	if *wait {
		for {
			time.Sleep(50 * time.Millisecond)
			latest, err := client.GetTask(ctx, task.ID)
			if err != nil {
				fmt.Fprintf(stderr, "error: failed to query task: %v\n", err)
				return 1
			}
			if latest.Status == storage.StatusCompleted {
				fmt.Fprintf(stdout, "%s\n", latest.Result)
				return 0
			}
			if latest.Status == storage.StatusFailed || latest.Status == storage.StatusCancelled {
				fmt.Fprintf(stderr, "task %s: %s\n", latest.Status, latest.ErrorMessage)
				return 1
			}
		}
	}

	return 0
}
