package main

import (
	"asura/internal/daemon"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"
)

func handleMemoryUtility(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory utility", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", DefaultSocketPath(), "daemon socket")
	workspace := fs.String("workspace", "", "workspace (required)")
	limit := fs.Int("limit", 20, "recent observations (1..100)")
	cursor := fs.String("cursor", "", "continuation cursor")
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 1
	}
	if *workspace == "" || fs.NArg() != 1 || *limit < 1 || *limit > 100 || len(*cursor) > 512 {
		fmt.Fprintln(stderr, "error: workspace, one memory ID, limit 1..100 and bounded cursor are required")
		return 1
	}
	scope, err := filepath.Abs(*workspace)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	client, err := daemon.NewClient(*socket)
	if err != nil {
		fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
		return 1
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	page, err := client.MemoryUtility(ctx, scope, fs.Arg(0), *limit, *cursor)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if err = json.NewEncoder(stdout).Encode(page); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
