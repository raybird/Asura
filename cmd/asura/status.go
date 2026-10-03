package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"asura/internal/daemon"
	"asura/internal/engine"
	"asura/internal/pipeline"
	"asura/internal/storage"
)

func handleStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dbPath := fs.String("db", "asura.db", "path to SQLite database")
	sockPath := fs.String("socket", os.Getenv("ASURA_SOCKET"), "path to daemon unix socket")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(stderr, "error: task_id is required")
		return 1
	}
	taskID := remaining[0]

	var task *storage.Task
	if *sockPath != "" {
		client, err := daemon.NewClient(*sockPath)
		if err == nil {
			defer client.Close()
			task, err = client.GetTask(context.Background(), taskID)
			if err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return 1
			}
		}
	}

	if task == nil {
		store, err := storage.NewSQLiteStore(*dbPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: failed to open storage: %v\n", err)
			return 1
		}
		defer store.Close()

		task, err = store.GetTask(context.Background(), taskID)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		task.UtilityStatus, task.UtilityError, _ = store.TaskUtilityStatus(context.Background(), task.ID)
	}

	fmt.Fprintf(stdout, "Task ID:   %s\n", task.ID)
	fmt.Fprintf(stdout, "Status:    %s\n", task.Status)
	backendID := task.Backend
	if backendID == "" {
		backendID = "opencode"
	}
	fmt.Fprintf(stdout, "Backend:   %s\n", backendID)
	if task.ExecutionMode != "" {
		fmt.Fprintf(stdout, "Mode:      %s\n", task.ExecutionMode)
	}
	fmt.Fprintf(stdout, "Workspace: %s\n", task.Workspace)
	fmt.Fprintf(stdout, "Prompt:    %s\n", task.Prompt)
	// 2026-10-03：只讀取接受時封存值，查詢不探測目前 runtime。
	var capabilityContext engine.ExecutionContext
	if task.ResolvedContext != "" && json.Unmarshal([]byte(task.ResolvedContext), &capabilityContext) != nil {
		fmt.Fprintln(stdout, "Capability contract: unsupported/unavailable")
	} else if contract := capabilityContext.ExecutionContract; contract != nil {
		if contract.Validate(task.ExecutionMode, backendID) != nil {
			fmt.Fprintln(stdout, "Capability contract: unsupported/unavailable")
		} else {
			fmt.Fprintf(stdout, "Capability contract: %s (policy %s)\n", contract.ContractVersion, contract.PolicyVersion)
			fmt.Fprintf(stdout, "Requested backend: %q\n", contract.RequestedBackend)
			fmt.Fprintf(stdout, "Runtime snapshot: %s %s (%s)\n", contract.Runtime.ID, contract.Runtime.Version, contract.Runtime.CapturedAt.Format("2006-01-02T15:04:05Z07:00"))
			fmt.Fprintf(stdout, "Requirements: %v\n", contract.Match.Required)
			fmt.Fprintf(stdout, "Capabilities: %v\n", contract.Match.Available)
			fmt.Fprintln(stdout, "Capability match: compatible")
		}
	} else {
		fmt.Fprintln(stdout, "Capability contract: legacy/unavailable")
	}
	if task.ResolvedContext != "" {
		var sealed engine.ExecutionContext
		if err := json.Unmarshal([]byte(task.ResolvedContext), &sealed); err == nil && sealed.Recall != nil {
			encoded, _ := json.Marshal(sealed.Recall)
			fmt.Fprintf(stdout, "Recall:    %s\n", encoded)
			status := task.UtilityStatus
			if status == "" || sealed.Recall.RecallID == "" {
				status = "unavailable"
			}
			fmt.Fprintf(stdout, "Utility:   %s %s\n", status, task.UtilityError)
		}
	}
	if task.PipelineTrace != "" {
		var trace pipeline.PipelineTrace
		if json.Unmarshal([]byte(task.PipelineTrace), &trace) == nil && trace.GuardError != "" {
			fmt.Fprintf(stdout, "Execution guard: rejected (%s)\n", trace.GuardError)
		}
		if err := json.Unmarshal([]byte(task.PipelineTrace), &trace); err == nil && len(trace.Stages) > 0 {
			var stageParts []string
			for _, s := range trace.Stages {
				if s.BackendVersion != "" {
					fmt.Fprintf(stdout, "Backend version (%s): %s\n", s.Name, s.BackendVersion)
				}
				stageParts = append(stageParts, fmt.Sprintf("%s (%s, %dms)", s.Name, s.Status, s.DurationMs))
			}
			fmt.Fprintf(stdout, "Pipeline:  %s\n", strings.Join(stageParts, " -> "))
			if trace.CriticReview != nil {
				fmt.Fprintf(stdout, "Verdict:   %s\n", trace.CriticReview.Verdict)
			}
		}
	}
	if task.Result != "" {
		fmt.Fprintf(stdout, "Result:    %s\n", task.Result)
	}
	if task.ErrorMessage != "" {
		fmt.Fprintf(stdout, "Error:     %s\n", task.ErrorMessage)
	}
	fmt.Fprintf(stdout, "Created:   %s\n", task.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(stdout, "Updated:   %s\n", task.UpdatedAt.Format("2006-01-02 15:04:05 UTC"))

	return 0
}
