package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrDisallowedWorkspace = errors.New("workspace is not in allowed roots")
)

type Policy struct {
	allowedRoots []string
}

func NewPolicy(allowedRoots []string) *Policy {
	cleaned := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		abs, err := resolveWorkspace(root)
		if err == nil {
			cleaned = append(cleaned, filepath.Clean(abs))
		}
	}
	return &Policy{allowedRoots: cleaned}
}

// 2026-09-13：回傳授權根目錄清單副本，供 Web 介面查詢授權專案
func (p *Policy) AllowedRoots() []string {
	if p == nil || len(p.allowedRoots) == 0 {
		return []string{}
	}
	res := make([]string, len(p.allowedRoots))
	copy(res, p.allowedRoots)
	return res
}

func (p *Policy) AuthorizeWorkspace(targetPath string) error {
	absTarget, err := resolveWorkspace(targetPath)
	if err != nil {
		return fmt.Errorf("%w: invalid target path", ErrDisallowedWorkspace)
	}
	cleanedTarget := filepath.Clean(absTarget)

	for _, root := range p.allowedRoots {
		if cleanedTarget == root {
			return nil
		}
		if strings.HasPrefix(cleanedTarget, root+string(filepath.Separator)) {
			return nil
		}
	}

	return ErrDisallowedWorkspace
}

// CanonicalWorkspace 將目標路徑正規化為標準專案鍵值
// 遵循 MUST FIX 4：解析符號連結與消除尾斜線、多餘斜線，保證同一目錄之不同路徑寫法（如尾斜線或符號連結）對齊至相同專案鎖鍵值
func (p *Policy) CanonicalWorkspace(targetPath string) string {
	absTarget, err := resolveWorkspace(targetPath)
	if err != nil {
		absTarget = targetPath
	}
	return filepath.Clean(absTarget)
}

// AuthorizeCanonicalWorkspace 同次驗證授權並解析出標準工作區路徑 (MEM-003, MEM-004)
// 若未授權或解析失敗則回傳錯誤；此處為最小可編譯空殼，由 P2-T2 完成正式實作
func (p *Policy) AuthorizeCanonicalWorkspace(targetPath string) (string, error) {
	return "", errors.New("AuthorizeCanonicalWorkspace: not implemented")
}

// 2026-09-13：解析既有祖先的連結，仍容許尚未建立的專案路徑。
func resolveWorkspace(path string) (string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		abs = cwd + string(filepath.Separator) + path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, statErr := os.Lstat(abs); statErr == nil {
		return "", err
	}
	abs = strings.TrimRight(abs, string(filepath.Separator))
	last := strings.LastIndex(abs, string(filepath.Separator))
	if last < 0 {
		return "", err
	}
	parent := abs[:last]
	if parent == "" {
		parent = string(filepath.Separator)
	}
	if parent == abs {
		return "", err
	}
	resolved, err = resolveWorkspace(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, abs[last+1:]), nil
}
