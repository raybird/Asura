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

// localRuntimes 是本機 registry 的 runtime，順序與 daemon 相同。
var localRuntimes = []string{"opencode", "codex", "omp", "antigravity"}

// localBackend 建立與 daemon 相同設定的 runtime adapter；config 是 OpenCode 唯讀角色的隔離設定目錄，
// onStarted 接到 adapter 的 OnProcessStarted（asura runtime verify 的取消 probe 使用）。
func localBackend(id, records, config string, onStarted func(context.Context, int)) backend.Backend {
	switch id {
	case "opencode":
		read := opencode.NewAdapter(opencode.AdapterConfig{VerificationPath: records, ConfigDir: config, Isolated: true, Pure: true, DisableAuto: true, DisableProjectConfig: true, Role: opencode.RoleOracle, Agent: opencode.AsuraReadOnlyAgentName, Permission: daemon.DefaultReadOnlyPermissionPolicy, OnProcessStarted: onStarted})
		write := opencode.NewAdapter(opencode.AdapterConfig{VerificationPath: records, Role: opencode.RoleStriker, Permission: daemon.DefaultApprovedPermissionPolicy, OnProcessStarted: onStarted})
		return opencode.NewRoleRoutingBackend(read, write)
	case "codex":
		return codex.NewAdapter(codex.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted})
	case "omp":
		return omp.NewAdapter(omp.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted})
	case "antigravity":
		return antigravity.NewAdapter(antigravity.AdapterConfig{VerificationPath: records, OnProcessStarted: onStarted})
	}
	return nil
}

// newLocalRegistry 建立與 daemon 相同組成的本機 runtime registry；cleanup 移除隔離的唯讀設定目錄。
func newLocalRegistry() (*backend.InMemoryRegistry, func(), error) {
	config, cleanup, err := opencode.CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		return nil, nil, err
	}
	reg := backend.NewRegistry()
	records := verificationPath()
	for _, id := range localRuntimes {
		reg.Register(localBackend(id, records, config, nil))
	}
	return reg, cleanup, nil
}

func handleRuntime(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "verify" {
		return handleRuntimeVerify(args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "inventory" {
		fmt.Fprintln(stderr, "Usage: asura runtime inventory [--socket path]\n       asura runtime verify <runtime>")
		return 1
	}
	fs := flag.NewFlagSet("runtime inventory", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", localSocketDefault(), "daemon unix socket")
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
		reg, cleanup, err := newLocalRegistry()
		if err != nil {
			fmt.Fprintln(stderr, "error: read-only runtime configuration unavailable")
			return 1
		}
		defer cleanup()
		snapshot, err = backend.CaptureInventory(ctx, reg.RegisteredSnapshot())
		if err != nil {
			fmt.Fprintf(stderr, "error: inventory: %v\n", err)
			return 1
		}
	}
	printInventory(stdout, snapshot)
	return 0
}
