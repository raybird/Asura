package main

// 2026-10-09：issue-45，asura doctor：唯讀 readiness 診斷。
// 不開 DB、不寫使用者狀態、不提交 Task 或呼叫模型；runtime 判定沿用本機 inventory。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"asura/internal/backend"
	"asura/internal/config"

	"golang.org/x/sys/unix"
)

type checkLevel int

const (
	levelOK checkLevel = iota
	levelInfo
	levelWarn
	levelFail
)

var levelLabel = map[checkLevel]string{levelOK: "[ok]  ", levelInfo: "[info]", levelWarn: "[warn]", levelFail: "[fail]"}

type doctorCheck struct {
	section, name, detail string
	level                 checkLevel
}

const (
	doctorDeadline      = 10 * time.Second
	doctorMaxDetail     = 512
	doctorProbeTimeout  = 2 * time.Second
	doctorDisclaimer    = "Doctor does not verify runtime login, quota or model responses."
	doctorPermissionTag = "(permission check only; nothing was written)"
)

// doctorStatus：有必要條件失敗為 NOT_READY（exit 1）；只有警告為 DEGRADED，其餘 READY（exit 0）。
func doctorStatus(checks []doctorCheck) (string, int) {
	status := "READY"
	for _, c := range checks {
		switch c.level {
		case levelFail:
			return "NOT_READY", 1
		case levelWarn:
			status = "DEGRADED"
		}
	}
	return status, 0
}

// availableModes 以控制平面的模式需求比對可用 runtime 的能力，不另建判定。
func availableModes(entries []backend.RuntimeInventoryEntry) []string {
	var modes []string
	for _, mode := range []string{"direct", "three-face"} {
		reqs, _ := backend.RequirementsForMode(mode)
		ok := true
		for _, req := range reqs {
			if !anyRuntimeHas(entries, req.RequiredCapabilities) {
				ok = false
				break
			}
		}
		if ok {
			modes = append(modes, mode)
		}
	}
	return modes
}

func anyRuntimeHas(entries []backend.RuntimeInventoryEntry, required []backend.Capability) bool {
	for _, e := range entries {
		if e.AvailabilityState != backend.Available || e.Descriptor == nil {
			continue
		}
		have := map[backend.Capability]bool{}
		for _, c := range e.Descriptor.Capabilities {
			have[c] = true
		}
		missing := false
		for _, c := range required {
			if !have[c] {
				missing = true
			}
		}
		if !missing {
			return true
		}
	}
	return false
}

func handleDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: asura doctor")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), doctorDeadline)
	defer cancel()
	var checks []doctorCheck
	add := func(section string, level checkLevel, name, format string, a ...any) {
		detail := fmt.Sprintf(format, a...)
		if len(detail) > doctorMaxDetail {
			cut := doctorMaxDetail
			for cut > 0 && !utf8.RuneStart(detail[cut]) {
				cut--
			}
			detail = detail[:cut] + "...(truncated)"
		}
		checks = append(checks, doctorCheck{section: section, name: name, level: level, detail: detail})
	}

	if runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") {
		add("System", levelOK, "platform", "%s/%s", runtime.GOOS, runtime.GOARCH)
	} else {
		add("System", levelFail, "platform", "%s/%s is not supported (Linux amd64/arm64 only)", runtime.GOOS, runtime.GOARCH)
	}
	if _, err := os.Stat("/proc/self/status"); err == nil {
		add("System", levelOK, "/proc", "available")
	} else {
		add("System", levelFail, "/proc", "unavailable; process supervision requires /proc")
	}
	if path, err := exec.LookPath("bwrap"); err == nil {
		add("System", levelOK, "bwrap", "%s", path)
	} else {
		add("System", levelWarn, "bwrap", "not found; sandboxed runtimes (omp, antigravity) are unavailable")
	}

	cc, cfgErr := loadCLIConfig()
	switch {
	case cfgErr != nil:
		add("Config", levelFail, "config", "%v", cfgErr)
	case cc != nil:
		add("Config", levelOK, "config", "%s (schema_version %d)", cc.paths.ConfigFile, config.SchemaVersion)
	default:
		if paths, err := config.DefaultPaths(os.Getenv); err == nil {
			add("Config", levelInfo, "config", "none at %s; using v1.0 defaults", paths.ConfigFile)
		} else {
			add("Config", levelInfo, "config", "cannot locate (HOME is not absolute); using v1.0 defaults")
		}
	}
	socket := cc.socketPath()
	if cfgErr == nil {
		db, _ := filepath.Abs(cc.dbPath())
		level, detail := assessDir(filepath.Dir(db))
		add("Config", level, "database directory", "%s", detail)
		if len(socket) > config.MaxSocketPathBytes {
			add("Config", levelFail, "socket", "%s is %d bytes, exceeding the %d-byte Unix socket limit", socket, len(socket), config.MaxSocketPathBytes)
		} else {
			level, detail := assessDir(filepath.Dir(socket))
			add("Config", level, "socket directory", "%s", detail)
		}
		if cc == nil || cc.cfg.AllowedRoots == nil {
			add("Config", levelInfo, "allowed roots", "not configured; daemon defaults to its working directory")
		}
		if cc != nil {
			for _, root := range cc.cfg.AllowedRoots {
				if info, err := os.Stat(root); err == nil && info.IsDir() {
					add("Config", levelOK, "allowed root", "%s", root)
				} else {
					add("Config", levelFail, "allowed root", "%s is not a directory", root)
				}
			}
		}
	}

	entries := doctorRuntimes(ctx, add)

	if _, err := exec.LookPath("systemctl"); err != nil {
		add("Service", levelWarn, "user service", "systemctl not found; run the daemon standalone")
	} else {
		state := userServiceState(ctx)
		switch state {
		case "active":
			add("Service", levelOK, "user service", "%s active", serviceUnit)
		case "not-installed":
			add("Service", levelWarn, "user service", "%s not installed", serviceUnit)
		default:
			add("Service", levelWarn, "user service", "%s %s", serviceUnit, state)
		}
	}
	dialer := net.Dialer{Timeout: doctorProbeTimeout}
	if conn, err := dialer.DialContext(ctx, "unix", socket); err == nil {
		conn.Close()
		add("Service", levelInfo, "daemon socket", "accepting connections at %s (daemon identity and health not verified)", socket)
	} else {
		add("Service", levelInfo, "daemon socket", "no listener at %s", socket)
	}

	status, code := doctorStatus(checks)
	fmt.Fprintf(stdout, "Asura doctor: %s\n", status)
	section := ""
	for _, c := range checks {
		if c.section != section {
			section = c.section
			fmt.Fprintf(stdout, "%s:\n", section)
		}
		fmt.Fprintf(stdout, "  %s %s: %s\n", levelLabel[c.level], c.name, c.detail)
	}
	modes := availableModes(entries)
	if len(modes) == 0 {
		modes = []string{"none"}
	}
	fmt.Fprintf(stdout, "Modes: %s\n", strings.Join(modes, ", "))
	fmt.Fprintln(stdout, doctorDisclaimer)
	return code
}

// doctorRuntimes 以與 daemon 相同的本機 registry 探測 runtime，並附上 adapter 回報的 executable。
func doctorRuntimes(ctx context.Context, add func(string, checkLevel, string, string, ...any)) []backend.RuntimeInventoryEntry {
	reg, cleanup, err := newLocalRegistry()
	if err != nil {
		add("Runtimes", levelFail, "runtimes", "read-only runtime configuration unavailable")
		return nil
	}
	defer cleanup()
	registered := reg.RegisteredSnapshot()
	snapshot, err := backend.CaptureInventory(ctx, registered)
	if err != nil {
		add("Runtimes", levelFail, "runtimes", "inventory unavailable")
		return nil
	}
	executables := map[string]string{}
	for _, r := range registered {
		if rep, ok := r.Backend.(backend.ExecutableReporter); ok {
			executables[r.ID] = rep.ProbeExecutable()
		}
	}
	available := 0
	for _, e := range snapshot.Entries {
		executable := executables[e.RuntimeID]
		if executable == "" {
			executable = "unknown"
		}
		if e.AvailabilityState == backend.Available && e.Descriptor != nil {
			available++
			var caps []string
			for _, c := range e.Descriptor.Capabilities {
				caps = append(caps, string(c))
			}
			add("Runtimes", levelOK, e.RuntimeID, "available version=%s executable=%s capabilities=%s", e.Descriptor.Version, executable, strings.Join(caps, ","))
		} else {
			hint := ""
			if e.ReasonCode == backend.VersionUnverified {
				hint = "; " + backend.VerifyHint(e.RuntimeID)
			}
			add("Runtimes", levelWarn, e.RuntimeID, "%s reason=%s executable=%s%s", e.AvailabilityState, e.ReasonCode, executable, hint)
		}
	}
	if available == 0 {
		add("Runtimes", levelFail, "runtimes", "no runtime available")
	}
	return snapshot.Entries
}

// userServiceState 以有界的 systemctl 查詢回傳固定狀態字，不轉述 systemctl 原始輸出。
func userServiceState(ctx context.Context) string {
	state, err := systemctl(ctx, doctorProbeTimeout, "is-active", serviceUnit)
	if errors.Is(err, context.DeadlineExceeded) {
		return "status unavailable (timeout)"
	}
	switch state = firstWord(state, ""); state {
	case "active", "activating", "deactivating", "failed":
		return state
	}
	dir, err := managerUnitDir(ctx)
	if err != nil {
		return "status unavailable (user bus)"
	}
	if _, err := os.Stat(unitPathIn(dir)); err == nil {
		return "inactive (installed)"
	}
	return "not-installed"
}

// assessDir 只以 metadata 與 access(2) 評估目錄可寫性；不存在時評估最近的既有上層目錄。
func assessDir(dir string) (checkLevel, string) {
	target := dir
	for {
		info, err := os.Stat(target)
		if err == nil {
			if !info.IsDir() {
				return levelFail, target + " is not a directory"
			}
			break
		}
		parent := filepath.Dir(target)
		if !errors.Is(err, fs.ErrNotExist) || parent == target {
			return levelWarn, dir + " writability unknown (cannot inspect)"
		}
		target = parent
	}
	var st unix.Statfs_t
	if unix.Statfs(target, &st) == nil && st.Flags&unix.ST_RDONLY != 0 {
		return levelFail, dir + " is on a read-only filesystem"
	}
	if unix.Access(target, unix.W_OK|unix.X_OK) != nil {
		return levelFail, dir + " is not writable"
	}
	if target != dir {
		return levelOK, dir + " can be created under " + target + " " + doctorPermissionTag
	}
	return levelOK, dir + " is writable " + doctorPermissionTag
}
