package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"asura/internal/auth"
	"asura/internal/engine"
	"asura/internal/storage"
)

// RunCLI 為 CLI 總進入點函式
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 1
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "run":
		return handleRun(subargs, stdout, stderr)
	case "status":
		return handleStatus(subargs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "error: unknown command %q\n\n", subcmd)
		printUsage(stderr)
		return 1
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: asura <command> [options] [arguments]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  run     Submit and execute a task in a workspace")
	fmt.Fprintln(w, "  status  Query the execution status and result of a task")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run 'asura <command> -h' for details on each command.")
}

func handleRun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dbPath := fs.String("db", "asura.db", "path to SQLite database")
	workspace := fs.String("workspace", "", "target workspace directory")
	allowedRoots := fs.String("allowed-roots", "", "comma-separated list of allowed root directories")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(stderr, "error: prompt is required")
		return 1
	}
	prompt := strings.Join(remaining, " ")

	ws := *workspace
	if ws == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "error: failed to get working directory: %v\n", err)
			return 1
		}
		ws = cwd
	}

	var roots []string
	if *allowedRoots != "" {
		for _, r := range strings.Split(*allowedRoots, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, r)
			}
		}
	} else {
		// 預設允許當前指定的 workspace
		roots = []string{ws}
	}

	policy := auth.NewPolicy(roots)

	store, err := storage.NewSQLiteStore(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to initialize storage: %v\n", err)
		return 1
	}
	defer store.Close()

	runner := func(ctx context.Context, dir string, p string) (string, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", p)
		cmd.Dir = dir
		var buf bytes.Buffer
		cmd.Stdout = io.MultiWriter(stdout, &buf)
		cmd.Stderr = io.MultiWriter(stderr, &buf)
		err := cmd.Run()
		return buf.String(), err
	}

	eng := engine.NewEngine(store, policy, engine.WithRunner(runner))

	ctx := context.Background()
	task, err := eng.SubmitTask(ctx, engine.SubmitRequest{
		Workspace: ws,
		Prompt:    prompt,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Task ID: %s\n", task.ID)
	fmt.Fprintf(stdout, "Status:  %s\n", task.Status)

	if task.Status == storage.StatusFailed {
		return 1
	}
	return 0
}
