package main

import (
	"asura/internal/backend"
	"asura/internal/backend/antigravity"
	"asura/internal/backend/codex"
	"asura/internal/backend/omp"
	"asura/internal/backend/opencode"
	"asura/internal/backend/verification"
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

// builtinIdentities 是內建已驗證身分，順序與 localRuntimes 相同。
var builtinIdentities = []verification.Identity{opencode.Builtin, codex.Builtin, omp.Builtin, antigravity.Builtin}

// handleRuntimeVerify 2026-10-22：issue-73，對已安裝的 runtime 執行全部 probe，全部通過才寫入驗證紀錄；
// 任一 probe 失敗或無法執行時結束碼非 0，紀錄不變。--list 列出內建身分與紀錄，--remove 移除一筆紀錄。
func handleRuntimeVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runtime verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	list := fs.Bool("list", false, "list built-in identities and verification records")
	remove := fs.Bool("remove", false, "remove the verification record of <runtime> <version> [<sha256>]")
	err := fs.Parse(args)
	n := fs.NArg()
	if err != nil || *list && (*remove || n != 0) || *remove && (n < 2 || n > 3) || !*list && !*remove && n != 1 ||
		n > 0 && !slices.Contains(localRuntimes, fs.Arg(0)) {
		fmt.Fprintf(stderr, "Usage: asura runtime verify <%s>\n       asura runtime verify --list\n       asura runtime verify --remove <runtime> <version> [<sha256>]\n", strings.Join(localRuntimes, "|"))
		return 1
	}
	records := verificationPath()
	if records == "" {
		fmt.Fprintln(stderr, "error: cannot resolve the verification records path")
		return 1
	}
	switch {
	case *list:
		return listVerification(records, stdout, stderr)
	case *remove:
		return removeVerification(records, fs.Args(), stdout, stderr)
	}
	id := fs.Arg(0)
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

// listVerification 先列內建身分（只比版本的身分 sha256 顯示 any），再依檔案順序列出紀錄。
func listVerification(path string, stdout, stderr io.Writer) int {
	records, err := verification.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	for _, b := range builtinIdentities {
		sum := b.Digest
		if sum == "" {
			sum = "any"
		}
		fmt.Fprintf(stdout, "%s version=%q sha256=%s built-in\n", b.Runtime, b.Version, sum)
	}
	for _, r := range records {
		fmt.Fprintf(stdout, "%s version=%q sha256=%s verified_at=%s asura_version=%s\n", r.Runtime, r.Version, r.Digest, r.VerifiedAt.UTC().Format(time.RFC3339), r.AsuraVersion)
	}
	return 0
}

// removeVerification 移除 <runtime> <version> [<sha256>] 指定的紀錄；省略 sha256 時必須剛好一筆相符。
// 內建身分不能移除：省略 sha256 時只要版本相同就視為內建身分，避免誤以為移除成功。
func removeVerification(path string, args []string, stdout, stderr io.Writer) int {
	id := verification.Identity{Runtime: args[0], Version: args[1]}
	if len(args) == 3 {
		id.Digest = args[2]
	}
	for _, b := range builtinIdentities {
		if b.Runtime == id.Runtime && b.Version == id.Version && (b.Digest == "" || id.Digest == "" || b.Digest == id.Digest) {
			fmt.Fprintf(stderr, "error: %s %s is a built-in verified identity: %v\n", id.Runtime, id.Version, verification.ErrBuiltin)
			return 1
		}
	}
	if id.Digest == "" {
		records, err := verification.Load(path)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		var matches []verification.Identity
		for _, r := range records {
			if r.Runtime == id.Runtime && r.Version == id.Version {
				matches = append(matches, r.Identity)
			}
		}
		if len(matches) > 1 {
			fmt.Fprintf(stderr, "error: %s %s has %d verification records; specify the sha256\n", id.Runtime, id.Version, len(matches))
			return 1
		}
		if len(matches) == 1 {
			id = matches[0]
		}
	}
	if err := verification.Remove(path, id, builtinIdentities); err != nil {
		fmt.Fprintf(stderr, "error: %s %s: %v\n", id.Runtime, id.Version, err)
		return 1
	}
	fmt.Fprintf(stdout, "removed %s %s (sha256 %s)\n", id.Runtime, id.Version, id.Digest)
	return 0
}
