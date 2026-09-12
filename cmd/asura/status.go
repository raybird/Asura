package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"asura/internal/daemon"
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
	}

	fmt.Fprintf(stdout, "Task ID:   %s\n", task.ID)
	fmt.Fprintf(stdout, "Status:    %s\n", task.Status)
	fmt.Fprintf(stdout, "Workspace: %s\n", task.Workspace)
	fmt.Fprintf(stdout, "Prompt:    %s\n", task.Prompt)
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
