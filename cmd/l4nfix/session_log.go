//go:build windows

package main

import (
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var sessionLogMu sync.Mutex

func appendSessionLog(root, message string) error {
	sessionLogMu.Lock()
	defer sessionLogMu.Unlock()
	now := time.Now()
	path := filepath.Join(root, "logs", now.Format("20060102")+".log")
	if err := pathguard.Within(filepath.Dir(root), path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(f, "[%s pid=%d] %s\r\n", now.Format(time.RFC3339Nano), os.Getpid(), message)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
