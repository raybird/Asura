package clisandbox

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SnapshotTree 2026-09-30：複製依賴後對實際副本取摘要，拒絕跨樹 symlink。
func SnapshotTree(source, target string) (string, error) {
	digest := sha256.New()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			if err := os.MkdirAll(dest, 0700); err != nil {
				return err
			}
			fmt.Fprintf(digest, "d\x00%s\n", rel)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(rel), link))
			if filepath.IsAbs(link) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("runtime symlink escapes dependency tree")
			}
			if err := os.Symlink(link, dest); err != nil {
				return err
			}
			fmt.Fprintf(digest, "s\x00%s\x00%s\n", rel, link)
		case info.Mode().IsRegular():
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400|info.Mode().Perm()&0111)
			if err != nil {
				in.Close()
				return err
			}
			fileDigest := sha256.New()
			_, copyErr := io.Copy(io.MultiWriter(out, fileDigest), in)
			in.Close()
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			fmt.Fprintf(digest, "f\x00%s\x00%o\x00%x\n", rel, info.Mode().Perm()&0111, fileDigest.Sum(nil))
		default:
			return fmt.Errorf("unsupported runtime entry")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
