//go:build windows

// Package pathguard validates paths before modifying files beneath a known root.
package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Windows junctions and other reparse points are not always ModeSymlink.
func Linked(info os.FileInfo) bool {
	if attrs, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return true
	}
	return info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}

func Within(root, path string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return fmt.Errorf("absolute root and target required: %s / %s", root, path)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target outside root: %s", path)
	}
	current := filepath.Clean(root)
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if Linked(info) {
			return fmt.Errorf("linked target component: %s", current)
		}
	}
	return nil
}
