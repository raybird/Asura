package main

import (
	"asura/internal/audit"
	"asura/internal/daemon"
	"asura/internal/storage"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func handleAudit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", "asura.db", "path to SQLite database")
	socket := fs.String("socket", os.Getenv("ASURA_SOCKET"), "path to daemon unix socket")
	id := fs.String("id", "", "task ID")
	jsonOutput := fs.Bool("json", false, "print machine-readable audit")
	if fs.Parse(args) != nil {
		return 1
	}
	if *id == "" || len(*id) > 256 {
		fmt.Fprintln(stderr, "error: valid task ID required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var record *audit.Record
	var err error
	if *socket != "" {
		client, connectErr := daemon.NewClient(*socket)
		if connectErr == nil {
			defer client.Close()
			record, err = client.GetTaskAudit(ctx, *id)
			if err != nil {
				fmt.Fprintln(stderr, "error: audit unavailable")
				return 1
			}
		}
	}
	if record == nil {
		store, openErr := storage.NewSQLiteStore(*db)
		if openErr != nil {
			fmt.Fprintln(stderr, "error: audit storage unavailable")
			return 1
		}
		defer store.Close()
		record, err = store.GetTaskAudit(ctx, *id)
	}
	if err != nil || record.Validate() != nil {
		fmt.Fprintln(stderr, "error: audit invalid/unavailable")
		return 1
	}
	raw, _ := json.Marshal(record)
	if len(raw)+1 > audit.MaxResponseBytes {
		fmt.Fprintln(stderr, "error: audit too large")
		return 1
	}
	if *jsonOutput {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "Task: %s\nAudit: %s (%s)\nIntent: %s / %s\nAdmission: %s\n", record.Header.TaskID, record.Completeness, record.Integrity, record.Header.Mode, record.Header.RequestedBackend, record.Header.Admission.State)
	for _, binding := range record.Header.Admission.Bindings {
		fmt.Fprintf(stdout, "Decision %s: policy=%s/%s priority=%v selected=%s\n", binding.Stage, binding.Policy, binding.PolicyVersion, binding.Priority, binding.Selected)
		for _, candidate := range binding.Candidates {
			verdict := "pass"
			if len(candidate.Missing) > 0 {
				verdict = "reject"
			}
			fmt.Fprintf(stdout, "Capability %s: %s missing=%v\n", candidate.RuntimeID, verdict, candidate.Missing)
		}
	}
	for _, runtime := range record.Header.Admission.Runtimes {
		fmt.Fprintf(stdout, "Inventory %s: %s (%s)\n", runtime.ID, runtime.Availability, runtime.Reason)
	}
	for _, stage := range record.Stages {
		fmt.Fprintf(stdout, "Stage %s: sealed=%s %s guard=%s (%s) observed=%s %s actual=%s %s outcome=%s (%s)\n", stage.Name, stage.SealedRuntime, stage.SealedVersion, stage.Guard.State, stage.Guard.Reason, stage.ObservedRuntime, stage.ObservedVersion, stage.ActualRuntime, stage.ActualVersion, stage.Status, stage.Reason)
	}
	if t := record.Terminal; t != nil {
		fmt.Fprintf(stdout, "Terminal: %s reason=%s layer=%s stage=%s completed=%d intents=%d returns=%d evidence=%s model_calls=%s cancel_phase=%s\n", t.State, t.Reason, t.FailureLayer, t.FailingStage, t.CompletedStages, t.RunIntents, t.ConfirmedReturns, t.InvocationEvidence, t.ModelCalls, t.CancelPhase)
	}
	return 0
}
