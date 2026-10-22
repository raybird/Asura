package verification

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"asura/internal/backend"
)

// Spec 2026-10-22：issue-73，一次驗證的對象與 probe 設定，由各 runtime 依實際會執行的安裝計算。
type Spec struct {
	// Record 是全部 probe 通過後要寫入的身分；VerifiedAt、AsuraVersion 由呼叫端填入。
	Record Record
	// Builtin 為 true 時目前安裝已是內建已驗證身分，不需要驗證。
	Builtin bool
	// Options.Started 由建立 adapter 的呼叫端填入。
	Options Options
	// Protected 是全部 probe 前後必須逐 byte 相同的登入憑證檔。
	Protected []string
}

// Subject 是可以被驗證的 runtime。
type Subject interface {
	VerificationSpec(ctx context.Context) (Spec, error)
}

// Prepare 以 newBackend("") 計算目前安裝的驗證身分；不是內建身分時在 dir 寫一份只含該身分的暫時紀錄，
// 再以 newBackend(暫時紀錄) 建立執行 probe 的 backend，讓 probe 走與正式執行相同的放行路徑。
func Prepare(ctx context.Context, dir string, newBackend func(records string) backend.Backend) (backend.Backend, Spec, error) {
	subject, ok := newBackend("").(Subject)
	if !ok {
		return nil, Spec{}, errors.New("runtime does not support verification")
	}
	spec, err := subject.VerificationSpec(ctx)
	if err != nil {
		return nil, Spec{}, err
	}
	if spec.Builtin {
		return newBackend(""), spec, nil
	}
	records, err := TempRecords(dir, spec.Record)
	if err != nil {
		return nil, Spec{}, err
	}
	return newBackend(records), spec, nil
}

// TempRecords 在 dir 下建立只含 rec 的驗證紀錄並回傳路徑，讓 probe 能對尚未驗證的已安裝版本執行。
func TempRecords(dir string, rec Record) (string, error) {
	data := filepath.Join(dir, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(data, "runtime-verification.json")
	return path, Add(path, rec)
}

// Verify 2026-10-22：issue-73，以 Prepare 建立 backend，依 spec 執行該 runtime 的 probe；不寫入正式紀錄。
// newBackend 必須把 onStarted 接到 adapter 的 OnProcessStarted，取消 probe 依此確認行程樹已回收。
// only 非空時只執行列出的 probe（開發用的失效對照組）。
func Verify(ctx context.Context, newBackend func(records string, onStarted func(context.Context, int)) backend.Backend, only ...string) (Spec, []Result, error) {
	dir, err := os.MkdirTemp("", "asura-verify-records-")
	if err != nil {
		return Spec{}, nil, err
	}
	defer os.RemoveAll(dir)
	started := make(chan int, 4)
	onStarted := func(_ context.Context, pgid int) {
		select {
		case started <- pgid:
		default:
		}
	}
	b, spec, err := Prepare(ctx, dir, func(records string) backend.Backend { return newBackend(records, onStarted) })
	if err != nil {
		return Spec{}, nil, err
	}
	opt := spec.Options
	opt.Started = started
	probes := Probes(opt)
	if len(only) > 0 {
		var picked []Probe
		for _, p := range probes {
			if slices.Contains(only, p.Name) {
				picked = append(picked, p)
			}
		}
		probes = picked
	}
	base, err := NewBase()
	if err != nil {
		return Spec{}, nil, err
	}
	defer os.RemoveAll(base)
	return spec, Run(ctx, b, base, probes, spec.Protected), nil
}

// NewBase 在使用者快取目錄下建立 probe 根目錄；刻意避開 /tmp，
// 因為 Codex workspace-write 預設可寫 /tmp、bwrap 會把 /tmp 換成 tmpfs，兩者都會讓 workspace 外的寫入檢查失準。
func NewBase() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(cache, "asura-verify"), 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(filepath.Join(cache, "asura-verify"), "run-")
}
