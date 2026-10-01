package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"asura/internal/daemon"
	"asura/internal/storage"
)

// reorderArgs 將以 "-" 開頭之旗標及其取值提前，non-flag 位置參數置後，避免 flag.FlagSet 遇位置參數提早中止解析
func reorderArgs(args []string) []string {
	var flags []string
	var nonFlags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if arg != "--durable" && arg != "-durable" && !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			nonFlags = append(nonFlags, arg)
		}
	}
	return append(flags, nonFlags...)
}

func handleMemory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "error: memory subcommands: add, list, get, remove, durable, detail, capture, candidates, candidate, approve, discard, clear-candidates, utility")
		return 1
	}

	subcmd := args[0]
	subargs := args[1:]

	if subcmd == "utility" {
		return handleMemoryUtility(subargs, stdout, stderr)
	}
	if subcmd == "durable" || subcmd == "detail" {
		return handleMemoryLifecycle(subcmd, subargs, stdout, stderr)
	}
	switch subcmd {
	case "add":
		fs := flag.NewFlagSet("memory add", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
		workspace := fs.String("workspace", "", "target workspace directory (required)")
		content := fs.String("content", "", "memory content (required)")
		supersedes := fs.String("supersedes", "", "predecessor memory id")
		operation := fs.String("operation-id", "", "stable retry operation id")
		durable := fs.Bool("durable", false, "protect from future automatic retention")
		reason := fs.String("reason", "", "lifecycle reason")

		if err := fs.Parse(reorderArgs(subargs)); err != nil {
			return 1
		}

		if *workspace == "" || *content == "" {
			fmt.Fprintln(stderr, "error: workspace and content are required")
			return 1
		}

		absWs, err := filepath.Abs(*workspace)
		if err != nil {
			fmt.Fprintf(stderr, "error: invalid workspace: %v\n", err)
			return 1
		}

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		mem, err := client.AddMemoryWithOptions(context.Background(), absWs, *content, storage.CreateMemoryOptions{Supersedes: *supersedes, OperationID: *operation, Durable: *durable, Reason: *reason})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "ID:        %s\n", mem.ID)
		fmt.Fprintf(stdout, "Workspace: %s\n", mem.Workspace)
		fmt.Fprintf(stdout, "Content:   %s\n", mem.Content)
		fmt.Fprintf(stdout, "Source:    %s\n", mem.Source)
		fmt.Fprintf(stdout, "CreatedAt: %s\n", mem.CreatedAt.Format(time.RFC3339))
		return 0

	case "list":
		fs := flag.NewFlagSet("memory list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
		workspace := fs.String("workspace", "", "target workspace directory (required)")

		if err := fs.Parse(reorderArgs(subargs)); err != nil {
			return 1
		}

		if *workspace == "" {
			fmt.Fprintln(stderr, "error: --workspace is required")
			return 1
		}

		absWs, err := filepath.Abs(*workspace)
		if err != nil {
			fmt.Fprintf(stderr, "error: invalid workspace: %v\n", err)
			return 1
		}

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		list, err := client.ListMemories(context.Background(), absWs)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		if len(list) == 0 {
			return 0
		}

		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tWORKSPACE\tSOURCE\tCREATED AT\tCONTENT")
		for _, m := range list {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.ID, m.Workspace, m.Source, m.CreatedAt.Format(time.RFC3339), m.Content)
		}
		_ = w.Flush()
		return 0

	case "get":
		fs := flag.NewFlagSet("memory get", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
		workspace := fs.String("workspace", "", "target workspace directory (required)")
		idFlag := fs.String("id", "", "memory ID")

		if err := fs.Parse(reorderArgs(subargs)); err != nil {
			return 1
		}

		memID := *idFlag
		if memID == "" && len(fs.Args()) > 0 {
			memID = fs.Args()[0]
		}

		if *workspace == "" || memID == "" {
			fmt.Fprintln(stderr, "error: --workspace and memory_id are required")
			return 1
		}

		absWs, err := filepath.Abs(*workspace)
		if err != nil {
			fmt.Fprintf(stderr, "error: invalid workspace: %v\n", err)
			return 1
		}

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		mem, err := client.GetMemory(context.Background(), absWs, memID)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "ID:        %s\n", mem.ID)
		fmt.Fprintf(stdout, "Workspace: %s\n", mem.Workspace)
		fmt.Fprintf(stdout, "Content:   %s\n", mem.Content)
		fmt.Fprintf(stdout, "Source:    %s\n", mem.Source)
		fmt.Fprintf(stdout, "CreatedAt: %s\n", mem.CreatedAt.Format(time.RFC3339))
		return 0

	case "remove":
		fs := flag.NewFlagSet("memory remove", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
		workspace := fs.String("workspace", "", "target workspace directory (required)")
		idFlag := fs.String("id", "", "memory ID")

		if err := fs.Parse(reorderArgs(subargs)); err != nil {
			return 1
		}

		memID := *idFlag
		if memID == "" && len(fs.Args()) > 0 {
			memID = fs.Args()[0]
		}

		if *workspace == "" || memID == "" {
			fmt.Fprintln(stderr, "error: --workspace and memory_id are required")
			return 1
		}

		absWs, err := filepath.Abs(*workspace)
		if err != nil {
			fmt.Fprintf(stderr, "error: invalid workspace: %v\n", err)
			return 1
		}

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		err = client.DeleteMemory(context.Background(), absWs, memID)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "Removed memory %s\n", memID)
		return 0

	case "capture":
		return handleMemoryCapture(subargs, stdout, stderr)

	case "candidates":
		return handleMemoryCandidates(subargs, stdout, stderr)

	case "approve":
		return handleMemoryApprove(subargs, stdout, stderr)

	case "discard":
		return handleMemoryDiscard(subargs, stdout, stderr)

	case "candidate":
		return handleMemoryCandidate(subargs, stdout, stderr)

	case "clear-candidates":
		return handleMemoryClearCandidates(subargs, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "error: unknown memory subcommand %q\n", subcmd)
		return 1
	}
}
