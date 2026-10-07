//go:build windows

package main

import (
	"crypto/sha256"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func fontFileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func validateFontManifest(m *fontManifest, gameRoot, backupRoot string) error {
	if err := pathguard.Within(filepath.Dir(backupRoot), filepath.Join(backupRoot, "manifest.json")); err != nil {
		return err
	}
	if m.GameRoot == "" || !strings.EqualFold(clean(m.GameRoot), clean(gameRoot)) {
		return fmt.Errorf("font backup belongs to another game: %s", m.GameRoot)
	}
	seen := map[string]bool{}
	for _, e := range m.Files {
		if err := pathguard.Within(gameRoot, e.Target); err != nil {
			return err
		}
		rel, err := filepath.Rel(gameRoot, e.Target)
		if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("font target outside game: %s", e.Target)
		}
		key := strings.ToLower(clean(e.Target))
		if seen[key] {
			return fmt.Errorf("duplicate font target: %s", e.Target)
		}
		seen[key] = true
		if !e.Existed {
			continue
		}
		if err := pathguard.Within(backupRoot, e.Backup); err != nil {
			return err
		}
		rel, err = filepath.Rel(backupRoot, e.Backup)
		if e.Backup == "" || err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid font backup: %s", e.Backup)
		}
		hash, err := fontFileHash(e.Backup)
		if err != nil {
			return err
		}
		if e.BackupSHA256 != "" && !strings.EqualFold(hash, e.BackupSHA256) {
			return fmt.Errorf("font backup hash mismatch: %s", e.Backup)
		}
	}
	return nil
}

func checkedFontManifest(root, gameRoot string) (*fontManifest, error) {
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err == nil {
		m, err := readFontManifest(root)
		if err != nil {
			return nil, err
		}
		if err = validateFontManifest(m, gameRoot, root); err != nil {
			return nil, err
		}
		return m, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, fmt.Errorf("font backup directory has files but no manifest: %s", root)
	}
	return &fontManifest{CreatedAt: time.Now().Format(time.RFC3339), GameRoot: gameRoot}, nil
}
