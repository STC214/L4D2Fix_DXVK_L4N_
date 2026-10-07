//go:build windows

package main

import (
	"archive/zip"
	"fmt"
	"os"
	"strings"
)

// Identity scans must enforce the same path/type/budget rules as deployment.
func validateZIPHeaders(z *zip.ReadCloser) error {
	if len(z.File) > maxPackageFiles {
		return fmt.Errorf("ZIP entry limit exceeded")
	}
	var total uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		name, err := zipResourceName(f)
		if err != nil {
			return err
		}
		rel, err := packageRelative(name)
		if err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return fmt.Errorf("ZIP special file: %s", name)
		}
		key := strings.ToLower(rel)
		if rel != "" {
			if seen[key] {
				return fmt.Errorf("ZIP duplicate path: %s", name)
			}
			seen[key] = true
		}
		if f.UncompressedSize64 > uint64(maxPackageBytes)-total {
			return fmt.Errorf("ZIP expanded byte limit exceeded")
		}
		total += f.UncompressedSize64
	}
	return nil
}
