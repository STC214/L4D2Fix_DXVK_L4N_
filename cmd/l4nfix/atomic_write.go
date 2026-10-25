//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Commit only after the complete new file has been written and closed.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return renameFileRetry(tmp, path)
}

// Antivirus/indexer handles can briefly deny replacement on Windows. Retry
// only sharing/access errors, bounded to 750 ms, without deleting the target.
func renameFileRetry(src, dst string) error {
	var err error
	for i := 0; i < 6; i++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) && !errors.Is(err, syscall.Errno(32)) && !errors.Is(err, syscall.Errno(33)) {
			return err
		}
		if i < 5 {
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
		}
	}
	return err
}
