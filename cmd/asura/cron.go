package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"
	"time"

	"asura/internal/cron"
	"asura/internal/daemon"
)

func handleCron(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "error: cron subcommands: add, list, pause, resume, remove, runs")
		return 1
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "add":
		fs := flag.NewFlagSet("cron add", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
		name := fs.String("name", "", "schedule name (required)")
		workspace := fs.String("workspace", "", "target workspace directory (required)")
		schedule := fs.String("schedule", "", "cron expression (5 fields, required)")
		prompt := fs.String("prompt", "", "task prompt (required)")

		if err := fs.Parse(subargs); err != nil {
			return 1
		}

		if *name == "" || *workspace == "" || *schedule == "" || *prompt == "" {
			fmt.Fprintln(stderr, "error: --name, --workspace, --schedule, and --prompt are all required")
			return 1
		}

		parsed, err := cron.Parse(*schedule)
		if err != nil {
			fmt.Fprintf(stderr, "error: invalid cron expression %q: %v\n", *schedule, err)
			return 1
		}
		if !parsed.IsReachable(time.Now()) {
			fmt.Fprintf(stderr, "error: cron expression %q is unreachable (no upcoming trigger dates)\n", *schedule)
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

		sched, err := client.AddCronSchedule(context.Background(), daemon.CronAddRequest{
			Name:      *name,
			Workspace: absWs,
			Schedule:  *schedule,
			Prompt:    *prompt,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "Created cron schedule %s (%s)\n", sched.ID, sched.Name)
		return 0

	case "list":
		fs := flag.NewFlagSet("cron list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")

		if err := fs.Parse(subargs); err != nil {
			return 1
		}

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		schedules, err := client.ListCronSchedules(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		if len(schedules) == 0 {
			fmt.Fprintln(stdout, "No scheduled tasks found.")
			return 0
		}

		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSCHEDULE\tSTATUS\tNEXT RUN\tWORKSPACE")
		for _, s := range schedules {
			nextRunStr := "-"
			if s.NextRunAt != nil {
				nextRunStr = s.NextRunAt.Local().Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, s.Schedule, s.Status, nextRunStr, s.Workspace)
		}
		_ = w.Flush()
		return 0

	case "pause":
		fs := flag.NewFlagSet("cron pause", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")

		if err := fs.Parse(subargs); err != nil {
			return 1
		}

		rem := fs.Args()
		if len(rem) == 0 {
			fmt.Fprintln(stderr, "error: schedule id is required (Usage: asura cron pause <schedule-id>)")
			return 1
		}
		cronID := rem[0]

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		sched, err := client.PauseCronSchedule(context.Background(), cronID)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "Paused cron schedule %s (%s)\n", sched.ID, sched.Name)
		return 0

	case "resume":
		fs := flag.NewFlagSet("cron resume", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")

		if err := fs.Parse(subargs); err != nil {
			return 1
		}

		rem := fs.Args()
		if len(rem) == 0 {
			fmt.Fprintln(stderr, "error: schedule id is required (Usage: asura cron resume <schedule-id>)")
			return 1
		}
		cronID := rem[0]

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		sched, err := client.ResumeCronSchedule(context.Background(), cronID)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "Resumed cron schedule %s (%s)\n", sched.ID, sched.Name)
		return 0

	case "remove":
		fs := flag.NewFlagSet("cron remove", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")

		if err := fs.Parse(subargs); err != nil {
			return 1
		}

		rem := fs.Args()
		if len(rem) == 0 {
			fmt.Fprintln(stderr, "error: schedule id is required (Usage: asura cron remove <schedule-id>)")
			return 1
		}
		cronID := rem[0]

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		if err := client.RemoveCronSchedule(context.Background(), cronID); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		fmt.Fprintf(stdout, "Removed cron schedule %s\n", cronID)
		return 0

	case "runs":
		fs := flag.NewFlagSet("cron runs", flag.ContinueOnError)
		fs.SetOutput(stderr)
		sockPath := fs.String("socket", DefaultSocketPath(), "path to daemon unix socket")
		limit := fs.Int("limit", 20, "max number of runs to display")

		if err := fs.Parse(subargs); err != nil {
			return 1
		}

		rem := fs.Args()
		if len(rem) == 0 {
			fmt.Fprintln(stderr, "error: schedule id is required (Usage: asura cron runs <schedule-id>)")
			return 1
		}
		cronID := rem[0]

		client, err := daemon.NewClient(*sockPath)
		if err != nil {
			fmt.Fprintf(stderr, "error: daemon unavailable: %v\n", err)
			return 1
		}
		defer client.Close()

		runs, err := client.ListCronRuns(context.Background(), cronID, *limit)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}

		if len(runs) == 0 {
			fmt.Fprintln(stdout, "No execution runs recorded.")
			return 0
		}

		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RUN ID\tSCHEDULED\tTRIGGERED\tSTATUS\tTASK ID")
		for _, r := range runs {
			taskID := r.TaskID
			if taskID == "" {
				taskID = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				r.ID,
				r.ScheduledTime.Local().Format("2006-01-02 15:04:05"),
				r.TriggeredTime.Local().Format("2006-01-02 15:04:05"),
				r.Status,
				taskID,
			)
		}
		_ = w.Flush()
		return 0

	default:
		fmt.Fprintf(stderr, "error: unknown cron action %q (supported: add, list, pause, resume, remove, runs)\n", subcmd)
		return 1
	}
}
