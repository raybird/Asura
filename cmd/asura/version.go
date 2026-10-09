package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
)

// Release build 以 -ldflags "-X main.version=<tag> -X main.commit=<sha>" 注入；source build 維持 dev／unknown。
var (
	version = "dev"
	commit  = "unknown"
)

func handleVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable version")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: asura version [--json]")
		return 2
	}
	if *jsonOutput {
		raw, _ := json.Marshal(map[string]string{"version": version, "commit": commit, "os": runtime.GOOS, "arch": runtime.GOARCH})
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	fmt.Fprintf(stdout, "asura %s (commit %s, %s/%s)\n", version, commit, runtime.GOOS, runtime.GOARCH)
	return 0
}
