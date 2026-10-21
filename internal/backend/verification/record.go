package verification

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"asura/internal/config"
)

// Identity 是一份 runtime 安裝的身分：回報的版本字串加上 Asura 實際會執行的檔案 SHA-256。
type Identity struct {
	Runtime string `json:"runtime"`
	Version string `json:"version"`
	Digest  string `json:"sha256"`
}

// Record 是 asura runtime verify 全部 probe 通過後寫入的驗證紀錄。
type Record struct {
	Identity
	// Tree 是驗證當時固定的相依樹雜湊（OMP 的 node_modules），執行時以它固定快照內容。
	Tree         string    `json:"tree_sha256,omitempty"`
	VerifiedAt   time.Time `json:"verified_at"`
	AsuraVersion string    `json:"asura_version"`
}

type recordFile struct {
	Records []Record `json:"records"`
}

var (
	ErrBuiltin    = errors.New("built-in verified identities cannot be removed")
	ErrNotFound   = errors.New("no verification record for this identity")
	ErrUnverified = errors.New("runtime version is not verified")
)

// Load 每次判定都重新讀檔；檔案不存在代表沒有紀錄。可被他人寫入的檔案會被拒絕，
// 否則寫得進去的人就能把未驗證的執行檔加進放行名單。
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("verification records %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("verification records %s: %w", path, err)
	}
	if err := config.CheckOwnerAndMode(path, info); err != nil {
		return nil, fmt.Errorf("verification records %s: %w", path, err)
	}
	var file recordFile
	if err := json.NewDecoder(f).Decode(&file); err != nil {
		return nil, fmt.Errorf("verification records %s: invalid JSON: %w", path, err)
	}
	return file.Records, nil
}

// Add 寫入或取代同一身分的紀錄。
func Add(path string, r Record) error {
	return update(path, func(records []Record) ([]Record, error) {
		for i := range records {
			if records[i].Identity == r.Identity {
				records[i] = r
				return records, nil
			}
		}
		return append(records, r), nil
	})
}

func Remove(path string, id Identity, builtin []Identity) error {
	if matchesBuiltin(id, builtin) {
		return ErrBuiltin
	}
	return update(path, func(records []Record) ([]Record, error) {
		for i := range records {
			if records[i].Identity == id {
				return append(records[:i], records[i+1:]...), nil
			}
		}
		return nil, ErrNotFound
	})
}

// Admit 判定已安裝的 runtime 是否可用。內建身分沒有 Digest 時只比版本（OpenCode、Codex 沿用變更前的行為），
// 此時不計算雜湊；其餘身分需要版本與雜湊都相符的內建身分或本機驗證紀錄。
// 放行時回傳相符的紀錄；內建身分回傳只含 Identity 的紀錄。
func Admit(runtime, version string, digest func() (string, error), builtin []Identity, recordsPath string) (Record, error) {
	for _, b := range builtin {
		if b.Digest == "" && b.Runtime == runtime && b.Version == version {
			return Record{Identity: b}, nil
		}
	}
	sum, err := digest()
	if err != nil {
		return Record{}, err
	}
	id := Identity{Runtime: runtime, Version: version, Digest: sum}
	if matchesBuiltin(id, builtin) {
		return Record{Identity: id}, nil
	}
	if recordsPath != "" {
		records, err := Load(recordsPath)
		if err != nil {
			return Record{}, fmt.Errorf("%w: %v", ErrUnverified, err)
		}
		for _, r := range records {
			if r.Identity == id {
				return r, nil
			}
		}
	}
	return Record{}, fmt.Errorf("%w: %s %s; run asura runtime verify %s", ErrUnverified, runtime, version, runtime)
}

func matchesBuiltin(id Identity, builtin []Identity) bool {
	for _, b := range builtin {
		if b.Runtime == id.Runtime && b.Version == id.Version && (b.Digest == "" || b.Digest == id.Digest) {
			return true
		}
	}
	return false
}

func FileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// TreeDigest 依路徑順序涵蓋目錄樹內每個項目的路徑、型別、執行權限與內容（symlink 取目標字串）。
func TreeDigest(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			fmt.Fprintf(h, "d %q\n", rel)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "l %q %q\n", rel, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			sum, err := FileDigest(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "f %q %o %s\n", rel, info.Mode().Perm()&0o111, sum)
		default:
			return fmt.Errorf("unsupported file type in %s", path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// CombineDigests 把多個雜湊合成一個身分雜湊。
func CombineDigests(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%s\n", p)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// update 以檔案鎖串起讀改寫，兩個 verify 同時執行時不會互相蓋掉紀錄；
// 新內容寫進暫存檔再 rename，讀取端不會讀到寫一半的檔案。
func update(path string, change func([]Record) ([]Record, error)) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	records, err := Load(path)
	if err != nil {
		return err
	}
	if records, err = change(records); err != nil {
		return err
	}
	data, err := json.MarshalIndent(recordFile{Records: records}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".runtime-verification-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
