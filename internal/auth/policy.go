package auth

import (
	"errors"
	"fmt"
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
		abs, err := filepath.Abs(root)
		if err == nil {
			cleaned = append(cleaned, filepath.Clean(abs))
		}
	}
	return &Policy{allowedRoots: cleaned}
}

func (p *Policy) AuthorizeWorkspace(targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
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
