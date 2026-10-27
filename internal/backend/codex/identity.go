package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"asura/internal/backend"
	"asura/internal/backend/verification"
)

// Builtin 是免驗證即可使用的 Codex 身分（只比版本，2026-10-22 issue-73）。
var Builtin = verification.Identity{Runtime: "codex", Version: BuiltinVersion}

// admit 2026-10-21：issue-73，內建 ExpectedVersion 只比版本；其他版本需要本機驗證紀錄。
func (a *Adapter) admit(rawVersion, bin string) error {
	builtin := []verification.Identity{{Runtime: "codex", Version: NormalizeCodexVersion(a.cfg.ExpectedVersion)}}
	_, err := verification.Admit("codex", NormalizeCodexVersion(rawVersion), func() (string, error) { return codexDigest(bin) },
		builtin, a.cfg.VerificationPath)
	return err
}

// codexDigest 計算 Asura 實際會執行的 codex 身分。npm 安裝時 codex 是 Node 包裝腳本，
// 原生檔與它附帶的 bwrap、rg 在平台套件裡，因此涵蓋整個 @openai/codex 套件樹與平台套件。
func codexDigest(bin string) (string, error) {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(filepath.Dir(real))
	if !isCodexPackage(root) {
		return verification.FileDigest(real)
	}
	rootDigest, err := verification.TreeDigest(root)
	if err != nil {
		return "", err
	}
	platform, err := codexPlatformPackage(root)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(platform, root+string(os.PathSeparator)) {
		return rootDigest, nil
	}
	platformDigest, err := verification.TreeDigest(platform)
	if err != nil {
		return "", err
	}
	return verification.CombineDigests(rootDigest, platformDigest), nil
}

func isCodexPackage(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct{ Name string }
	return json.Unmarshal(data, &pkg) == nil && pkg.Name == "@openai/codex"
}

// codexPlatformPackage 依 codex.js 的 require.resolve 規則，從 bin/ 往上找平台套件；找不到時使用套件內的 vendor/。
func codexPlatformPackage(root string) (string, error) {
	name := map[string]string{"amd64": "codex-linux-x64", "arm64": "codex-linux-arm64"}[runtime.GOARCH]
	if name == "" {
		return "", fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
	for dir := filepath.Join(root, "bin"); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) != "node_modules" {
			candidate := filepath.Join(dir, "node_modules", "@openai", name)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate, nil
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	vendor := filepath.Join(root, "vendor")
	if info, err := os.Stat(vendor); err == nil && info.IsDir() {
		return vendor, nil
	}
	return "", fmt.Errorf("codex platform package @openai/%s not found", name)
}

// VerificationSpec 2026-10-22：issue-73，回報 admission 會檢查的同一個身分；Codex 跑唯讀角色 probe。
func (a *Adapter) VerificationSpec(ctx context.Context) (verification.Spec, error) {
	bin := a.ProbeExecutable()
	if bin == "" {
		return verification.Spec{}, ErrNoBinary
	}
	raw, err := backend.ProbeVersionCommand(ctx, bin)
	if err != nil {
		return verification.Spec{}, err
	}
	spec := verification.Spec{Options: verification.Options{ReadOnlyRoles: true, ReadOnlySandbox: sandboxRunner(bin)}}
	spec.Record.Runtime, spec.Record.Version = "codex", NormalizeCodexVersion(raw)
	if spec.Record.Version == NormalizeCodexVersion(a.cfg.ExpectedVersion) {
		spec.Builtin = true
		return spec, nil
	}
	spec.Record.Digest, err = codexDigest(bin)
	return spec, err
}

// sandboxRunner 2026-10-27：PR #77 審查 Q17，以 codex sandbox 在 Codex 自己的 Linux 沙箱內直接執行命令，不經模型。
// :read-only 與 :workspace 是 Codex 內建的權限設定；命令與 exec 的 shell 工具一樣經 bwrap 與 codex-linux-sandbox 執行（0.163.0 以 ps 確認）。
func sandboxRunner(bin string) func(context.Context, string, bool, ...string) (string, error) {
	return func(ctx context.Context, workspace string, readOnly bool, command ...string) (string, error) {
		profile := ":workspace"
		if readOnly {
			profile = ":read-only"
		}
		out, err := exec.CommandContext(ctx, bin, append([]string{"sandbox", "-P", profile, "-C", workspace, "--"}, command...)...).CombinedOutput()
		return string(out), err
	}
}
