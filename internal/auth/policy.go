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
