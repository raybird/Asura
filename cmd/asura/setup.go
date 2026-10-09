package main

// 2026-10-09：issue-45，asura setup：建立 operator config 與資料目錄，可選擇安裝 user service。
// 重跑保留既有設定與資料；不安裝或登入 runtime、不修改 shell rc、不啟動 Web、不搬移舊 DB。

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"asura/internal/config"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"
)

func handleSetup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootsFlag := fs.String("allowed-roots", "", "comma-separated allowed root directories (required when not interactive)")
	withService := fs.Bool("service", false, "install, enable and start the systemd user service")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: asura setup [--allowed-roots DIR[,DIR...]] [--service]")
		return 2
	}
	incomplete := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "error: "+format+"\n", a...)
		fmt.Fprintln(stderr, "Setup incomplete; fix the problem above and rerun `asura setup`.")
		return 1
	}
	paths, err := config.DefaultPaths(os.Getenv)
	if err != nil {
		return incomplete("%v", err)
	}
	_, termErr := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	interactive := termErr == nil
	in := bufio.NewReader(os.Stdin)
	ask := func(question string) string {
		fmt.Fprint(stdout, question)
		answer, _ := in.ReadString('\n')
		return strings.TrimSpace(answer)
	}

	var roots []string
	if *rootsFlag != "" {
		if roots, err = parseRoots(*rootsFlag); err != nil {
			return incomplete("%v", err)
		}
	}
	if activeConfig == nil {
		if roots == nil {
			if !interactive {
				return incomplete("--allowed-roots is required when not running interactively")
			}
			cwd, _ := os.Getwd()
			answer := ask(fmt.Sprintf("Allowed root directories (comma-separated) [%s]: ", cwd))
			if answer == "" {
				answer = cwd
			}
			if roots, err = parseRoots(answer); err != nil {
				return incomplete("%v", err)
			}
		}
		if err := writeNewConfig(paths.ConfigFile, roots); err != nil {
			return incomplete("cannot create %s: %v", paths.ConfigFile, err)
		}
		fmt.Fprintf(stdout, "Created %s\n", paths.ConfigFile)
		if activeConfig, err = loadCLIConfig(); err != nil || activeConfig == nil {
			return incomplete("cannot load %s", paths.ConfigFile)
		}
	} else {
		fmt.Fprintf(stdout, "Using existing config %s\n", paths.ConfigFile)
		if roots != nil && !sameRoots(roots, activeConfig.cfg.AllowedRoots) {
			return incomplete("--allowed-roots conflicts with existing config %s; edit that file manually to change allowed_roots", paths.ConfigFile)
		}
	}

	db := activeConfig.dbPath()
	if err := activeConfig.prepareDirs(db); err != nil {
		return incomplete("%v", err)
	}
	if legacy, err := filepath.Abs("asura.db"); err == nil && legacy != db {
		if _, err := os.Stat(legacy); err == nil {
			fmt.Fprintf(stdout, "Found existing database %s; it was not moved. To keep using it, set db = %q in %s.\n", legacy, legacy, paths.ConfigFile)
		}
	}

	install := *withService
	serviceChosen := false
	fs.Visit(func(f *flag.Flag) { serviceChosen = serviceChosen || f.Name == "service" })
	if !serviceChosen && interactive {
		answer := strings.ToLower(ask("Install and start the user service? [y/N]: "))
		install = answer == "y" || answer == "yes"
	}
	if install {
		for _, verb := range []string{"install", "start"} {
			if code := handleService([]string{verb}, stdout, stderr); code != 0 {
				return incomplete("user service %s failed", verb)
			}
		}
	} else {
		fmt.Fprintln(stdout, "User service not enabled; run `asura daemon` to start the daemon, or rerun `asura setup --service`.")
	}

	fmt.Fprintln(stdout)
	handleDoctor(nil, stdout, stderr)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Setup complete.")
	fmt.Fprintf(stdout, "Next: asura ask --workspace %s \"<task>\"\n", activeConfig.cfg.AllowedRoots[0])
	return 0
}

// parseRoots 將逗號分隔的目錄轉為絕對路徑，每個都必須是既有目錄。
func parseRoots(value string) ([]string, error) {
	var roots []string
	for _, r := range strings.Split(value, ",") {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed root %s", r)
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("allowed root %s is not a directory", abs)
		}
		roots = append(roots, abs)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one allowed root is required")
	}
	return roots, nil
}

func sameRoots(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// writeNewConfig 以 0700 目錄、0600 檔案建立 config；以 link 發佈，既有檔案不會被覆寫。
func writeNewConfig(path string, roots []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	fmt.Fprintf(tmp, "# Asura operator config, created by asura setup on %s.\n", time.Now().Format("2006-01-02"))
	body := struct {
		SchemaVersion int      `toml:"schema_version"`
		AllowedRoots  []string `toml:"allowed_roots"`
	}{config.SchemaVersion, roots}
	if err := toml.NewEncoder(tmp).Encode(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), path)
}
