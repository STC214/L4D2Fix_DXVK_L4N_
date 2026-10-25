//go:build windows

package main

import (
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"syscall"
)

// Windows releases this exclusive file handle even after a process crash.
// Keep the lock file itself: removing it after closing would introduce a race.
func withPortableOperationLock(fn func() error) error {
	root, e := packageRoot()
	if e != nil {
		return e
	}
	release, e := acquireOperationLock(root, ".l4n-operation.lock")
	if e != nil {
		return e
	}
	defer release()
	return fn()
}
func acquireOperationLock(root, name string) (func(), error) {
	info, e := os.Lstat(root)
	if e != nil {
		return nil, e
	}
	if pathguard.Linked(info) || !info.IsDir() {
		return nil, fmt.Errorf("操作目录必须是普通目录: %s", root)
	}
	p := filepath.Join(root, name)
	if e = pathguard.Within(root, p); e != nil {
		return nil, e
	}
	ptr, e := syscall.UTF16PtrFromString(p)
	if e != nil {
		return nil, e
	}
	h, e := syscall.CreateFile(ptr, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, fmt.Errorf("另一个工具实例正在操作此目录，或目录不可写: %w", e)
	}
	return func() { syscall.CloseHandle(h) }, nil
}
