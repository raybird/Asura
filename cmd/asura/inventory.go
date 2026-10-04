package main

import (
	"asura/internal/backend"
	"asura/internal/backend/antigravity"
	"asura/internal/backend/codex"
	"asura/internal/backend/omp"
	"asura/internal/backend/opencode"
	"asura/internal/daemon"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

func printInventory(w io.Writer, s *backend.InventorySnapshot) {
	fmt.Fprintf(w, "Runtime Inventory: %s (policy %s; observed %s)\n", s.ContractVersion, s.PolicyVersion, s.ObservedAt.Format("2006-01-02T15:04:05Z07:00"))
	for _, e := range s.Entries {
		version := ""
		if e.Descriptor != nil {
			version = e.Descriptor.Version
		}
		fmt.Fprintf(w, "%s registration=%s availability=%s version=%q reason=%s\n", e.RuntimeID, e.RegistrationState, e.AvailabilityState, version, e.ReasonCode)
	}
}
func handleRuntime(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "inventory" {
		fmt.Fprintln(stderr, "Usage: asura runtime inventory [--socket path]")
		return 1
	}
	fs := flag.NewFlagSet("runtime inventory", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", os.Getenv("ASURA_SOCKET"), "daemon unix socket")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return 1
	}
	ctx := context.Background()
	var snapshot *backend.InventorySnapshot
	if *socket != "" {
		client, err := daemon.NewClient(*socket)
		if err == nil {
			defer client.Close()
			snapshot, err = client.Inventory(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "error: inventory: %v\n", err)
				return 1
			}
		}
	}
	if snapshot == nil {
		config, cleanup, err := opencode.CreateIsolatedReadOnlyConfigDir()
		if err != nil {
			fmt.Fprintln(stderr, "error: read-only runtime configuration unavailable")
			return 1
		}
		defer cleanup()
		reg := backend.NewRegistry()
		read := opencode.NewAdapter(opencode.AdapterConfig{ConfigDir: config, Isolated: true, Pure: true, DisableAuto: true, DisableProjectConfig: true, Role: opencode.RoleOracle, Agent: opencode.AsuraReadOnlyAgentName, Permission: daemon.DefaultReadOnlyPermissionPolicy})
		write := opencode.NewAdapter(opencode.AdapterConfig{Role: opencode.RoleStriker, Permission: daemon.DefaultApprovedPermissionPolicy})
		reg.Register(opencode.NewRoleRoutingBackend(read, write))
		reg.Register(codex.NewAdapter(codex.AdapterConfig{}))
		reg.Register(omp.NewAdapter(omp.AdapterConfig{}))
		reg.Register(antigravity.NewAdapter(antigravity.AdapterConfig{}))
		snapshot, err = backend.CaptureInventory(ctx, reg.RegisteredSnapshot())
		if err != nil {
			fmt.Fprintf(stderr, "error: inventory: %v\n", err)
			return 1
		}
	}
	printInventory(stdout, snapshot)
	return 0
}
