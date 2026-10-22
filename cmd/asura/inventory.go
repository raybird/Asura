package main

import (
	"asura/internal/backend"
	"asura/internal/backend/antigravity"
	"asura/internal/backend/codex"
	"asura/internal/backend/omp"
	"asura/internal/backend/opencode"
	"asura/internal/backend/verification"
	"asura/internal/daemon"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
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

// handleRuntimeVerify 2026-10-22：issue-73，對已安裝的 runtime 執行全部 probe，全部通過才寫入驗證紀錄；
// 任一 probe 失敗或無法執行時結束碼非 0，紀錄不變。
func handleRuntimeVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runtime verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || !slices.Contains(localRuntimes, fs.Arg(0)) {
		fmt.Fprintf(stderr, "Usage: asura runtime verify <%s>\n", strings.Join(localRuntimes, "|"))
		return 1
	}
	id := fs.Arg(0)
	records := verificationPath()
	if records == "" {
		fmt.Fprintln(stderr, "error: cannot resolve the verification records path")
		return 1
	}
	config, cleanup, err := opencode.CreateIsolatedReadOnlyConfigDir()
	if err != nil {
		fmt.Fprintln(stderr, "error: read-only runtime configuration unavailable")
		return 1
	}
	defer cleanup()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "Verifying %s: each probe runs the model once\n", id)
	spec, results, err := verification.Verify(ctx, func(path string, onStarted func(context.Context, int)) backend.Backend {
		return localBackend(id, path, config, onStarted)
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: verify %s: %v\n", id, err)
		return 1
	}
	rec := spec.Record
	if spec.Builtin {
		fmt.Fprintf(stdout, "%s %s is a built-in verified identity; nothing to verify\n", id, rec.Version)
		return 0
	}
	summary, ok := verification.Summary(results)
	fmt.Fprint(stdout, summary)
	if !ok {
		fmt.Fprintf(stderr, "error: %s %s (sha256 %s) failed verification; no record written\n", id, rec.Version, rec.Digest)
		return 1
	}
	rec.VerifiedAt, rec.AsuraVersion = time.Now().UTC(), version
	if err := verification.Add(records, rec); err != nil {
		fmt.Fprintf(stderr, "error: write verification record: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "verified %s %s (sha256 %s); recorded in %s\n", id, rec.Version, rec.Digest, records)
	return 0
}
