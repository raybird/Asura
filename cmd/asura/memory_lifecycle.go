package main

import (
	"asura/internal/daemon"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
)

func handleMemoryLifecycle(action string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	sock := fs.String("socket", DefaultSocketPath(), "daemon socket")
	ws := fs.String("workspace", "", "workspace (required)")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 1
	}
	if *ws == "" || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "error: workspace and one memory id required")
		return 1
	}
	scope, err := filepath.Abs(*ws)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	c, err := daemon.NewClient(*sock)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer c.Close()
	var value any
	if action == "durable" {
		value, err = c.MarkMemoryDurable(context.Background(), scope, fs.Arg(0))
	} else {
		value, err = c.MemoryDetail(context.Background(), scope, fs.Arg(0))
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err = json.NewEncoder(stdout).Encode(value); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
