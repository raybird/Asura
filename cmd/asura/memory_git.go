package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"
	"time"

	"asura/internal/daemon"
)

func handleMemoryCapture(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory capture", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
	workspace := fs.String("workspace", "", "target workspace directory (required)")
	rangeExpr := fs.String("range", "", "git commit range (e.g. HEAD~2..HEAD or <sha>)")
	sinceLast := fs.Bool("since-last", false, "capture incrementally since last recorded checkpoint")
	force := fs.Bool("force", false, "force reopening discarded candidate")
	jsonOutput := fs.Bool("json", false, "output in JSON format")

	if err := fs.Parse(reorderArgs(args)); err != nil {
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

	resp, err := client.CaptureGitMemory(context.Background(), absWs, *rangeExpr, *sinceLast, *force)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
		return 0
	}

	cand := resp.Candidate
	if cand == nil {
		fmt.Fprintln(stderr, "error: no candidate returned from daemon")
		return 1
	}

	fmt.Fprintf(stdout, "Candidate ID: %s\n", cand.ID)
	fmt.Fprintf(stdout, "Status:       %s\n", cand.Status)
	fmt.Fprintf(stdout, "Workspace:    %s\n", cand.Workspace)
	fmt.Fprintf(stdout, "Range:        %s..%s (%d commits)\n", cand.BaseSHA, cand.HeadSHA, cand.CommitCount)
	if resp.GitFacts != nil && resp.GitFacts.Truncated {
		fmt.Fprintf(stdout, "Truncated:    true (%s)\n", resp.GitFacts.TruncatedReason)
	}
	if resp.Message != "" {
		fmt.Fprintf(stdout, "Message:      %s\n", resp.Message)
	}
	fmt.Fprintln(stdout, "\nDraft Content:")
	fmt.Fprintln(stdout, "----------------------------------------")
	fmt.Fprintln(stdout, cand.DraftContent)
	fmt.Fprintln(stdout, "----------------------------------------")
	return 0
}

func handleMemoryCandidates(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory candidates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
	workspace := fs.String("workspace", "", "target workspace directory (required)")
	status := fs.String("status", "", "filter by status: pending, approved, discarded")
	limit := fs.Int("limit", 50, "maximum number of candidates to list")
	jsonOutput := fs.Bool("json", false, "output in JSON format")

	if err := fs.Parse(reorderArgs(args)); err != nil {
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

	cands, err := client.ListGitCandidates(context.Background(), absWs, *status, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(cands)
		return 0
	}

	if len(cands) == 0 {
		return 0
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tCOMMITS\tCREATED AT\tAPPROVED MEMORY\tRANGE")
	for _, c := range cands {
		baseShort := c.BaseSHA
		if len(baseShort) > 8 {
			baseShort = baseShort[:8]
		}
		headShort := c.HeadSHA
		if len(headShort) > 8 {
			headShort = headShort[:8]
		}
		rangeStr := fmt.Sprintf("%s..%s", baseShort, headShort)
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			c.ID, c.Status, c.CommitCount,
			c.CreatedAt.Format(time.RFC3339),
			c.ApprovedMemoryID,
			rangeStr,
		)
	}
	_ = w.Flush()
	return 0
}

func handleMemoryApprove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
	workspace := fs.String("workspace", "", "target workspace directory (required)")
	candidateID := fs.String("candidate", "", "candidate ID to approve (required)")
	content := fs.String("content", "", "optional customized memory content (overrides draft)")
	jsonOutput := fs.Bool("json", false, "output in JSON format")

	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 1
	}

	candID := *candidateID
	if candID == "" && len(fs.Args()) > 0 {
		candID = fs.Args()[0]
	}

	if *workspace == "" || candID == "" {
		fmt.Fprintln(stderr, "error: --workspace and --candidate are required")
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

	memID, mem, err := client.ApproveGitCandidate(context.Background(), absWs, candID, *content)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]interface{}{
			"approved_memory_id": memID,
			"memory":             mem,
		})
		return 0
	}

	fmt.Fprintf(stdout, "Approved Candidate: %s\n", candID)
	fmt.Fprintf(stdout, "Formal Memory ID:   %s\n", memID)
	if mem != nil {
		fmt.Fprintf(stdout, "Workspace:          %s\n", mem.Workspace)
		fmt.Fprintf(stdout, "Source:             %s\n", mem.Source)
		fmt.Fprintln(stdout, "\nContent:")
		fmt.Fprintln(stdout, "----------------------------------------")
		fmt.Fprintln(stdout, mem.Content)
		fmt.Fprintln(stdout, "----------------------------------------")
	}
	return 0
}

func handleMemoryDiscard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory discard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
	workspace := fs.String("workspace", "", "target workspace directory (required)")
	candidateID := fs.String("candidate", "", "candidate ID to discard (required)")
	jsonOutput := fs.Bool("json", false, "output in JSON format")

	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 1
	}

	candID := *candidateID
	if candID == "" && len(fs.Args()) > 0 {
		candID = fs.Args()[0]
	}

	if *workspace == "" || candID == "" {
		fmt.Fprintln(stderr, "error: --workspace and --candidate are required")
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

	if err := client.DiscardGitCandidate(context.Background(), absWs, candID); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(map[string]interface{}{
			"candidate_id": candID,
			"status":       "discarded",
		})
		return 0
	}

	fmt.Fprintf(stdout, "candidate discarded: %s\n", candID)
	return 0
}
