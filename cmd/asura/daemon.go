package main

import (
	"asura/internal/backend"
	"asura/internal/config"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"asura/internal/auth"
	"asura/internal/daemon"
	"asura/internal/storage"
)

func handleDaemon(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)

	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
	dbPath := fs.String("db", activeConfig.dbPath(), "path to SQLite database")
	allowedRoots := fs.String("allowed-roots", activeConfig.allowedRoots(), "comma-separated list of allowed root directories")
	workers := fs.Int("workers", activeConfig.workers(), "maximum concurrent workers")
	queue := fs.Int("queue", activeConfig.queue(), "maximum queue capacity")
	permission := fs.String("permission", "", "override OPENCODE_PERMISSION JSON policy")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	contextCfg, err := activeConfig.contextConfig()
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid context configuration: %v\n", err)
		return 1
	}

	runtimePriority, err := backend.ParseRuntimePriority(activeConfig.runtimePriority())
	if err != nil {
		fmt.Fprintf(stderr, "error: runtime priority: %v\n", err)
		return 1
	}

	utilityPolicy := activeConfig.memoryUtilityPolicy()
	if utilityPolicy != "observe" && utilityPolicy != "apply" {
		fmt.Fprintln(stderr, "error: invalid utility policy")
		return 1
	}
	var roots []string
	if *allowedRoots != "" {
		for _, r := range strings.Split(*allowedRoots, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				abs, err := filepath.Abs(r)
				if err == nil {
					roots = append(roots, abs)
				} else {
					roots = append(roots, r)
				}
			}
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "error: failed to get working directory: %v\n", err)
			return 1
		}
		roots = []string{cwd}
	}

	policy := auth.NewPolicy(roots)

	if activeConfig != nil {
		if err := config.CheckSocketPath(*sockPath); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	if err := activeConfig.prepareDirs(*sockPath, *dbPath); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	store, err := storage.NewSQLiteStore(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to initialize storage: %v\n", err)
		return 1
	}
	defer store.Close()

	cfg := daemon.Config{
		RuntimePriority: runtimePriority,
		UtilityPolicy:   utilityPolicy,
		SocketPath:      *sockPath,
		MaxWorkers:      *workers,
		MaxQueue:        *queue,
		ContextConfig:   contextCfg,
	}
	if *permission != "" {
		cfg.OpenCodeConfig.Permission = *permission
	}

	d := daemon.NewDaemon(cfg, store, policy)

	if err := d.Start(); err != nil {
		fmt.Fprintf(stderr, "error: failed to start daemon: %v\n", err)
		return 1
	}
	defer d.Close()

	fmt.Fprintf(stdout, "Daemon started on socket %s (workers: %d, queue: %d)\n", *sockPath, *workers, *queue)

	// 監聽終止信號實現 Graceful Shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	sig := <-sigCh
	fmt.Fprintf(stdout, "\nReceived signal %s, draining workers and shutting down daemon...\n", sig)

	if err := d.Close(); err != nil {
		fmt.Fprintf(stderr, "error during daemon shutdown: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "Daemon stopped successfully.")
	return 0
}
