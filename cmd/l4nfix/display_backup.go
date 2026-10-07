//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type displayBackupRecord struct {
	GameRoot string `json:"gameRoot"`
	Present  bool   `json:"present"`
	SHA256   string `json:"sha256,omitempty"`
	Mode     uint32 `json:"mode,omitempty"`
	ModTime  string `json:"modTime,omitempty"`
}

func backupDisplaySettings(gameRoot, resRoot string) error {
	src := videoSettingsPath(gameRoot)
	if err := pathguard.Within(gameRoot, src); err != nil {
		return err
	}
	if err := os.MkdirAll(resRoot, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(resRoot, ".display-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	record := displayBackupRecord{GameRoot: clean(gameRoot)}
	if info, err := os.Stat(src); err == nil {
		record.Present = true
		record.Mode = uint32(info.Mode().Perm())
		record.ModTime = info.ModTime().Format(time.RFC3339Nano)
		dst := filepath.Join(stage, "video.txt")
		if err := copyFile(src, dst); err != nil {
			return err
		}
		record.SHA256, err = fileHash(dst)
		if err != nil {
			return err
		}
		current, err := fileHash(src)
		if err != nil || current != record.SHA256 {
			return fmt.Errorf("display settings changed during backup")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(stage, "metadata.json"), data, 0644); err != nil {
		return err
	}
	return replaceDir(stage, filepath.Dir(displaySettingsBackupPath(resRoot)))
}

func checkedDisplayBackup(gameRoot, resRoot string) (*displayBackupRecord, error) {
	folder := filepath.Dir(displaySettingsBackupPath(resRoot))
	if _, err := os.Stat(folder); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := pathguard.Within(resRoot, filepath.Join(folder, "metadata.json")); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(folder, "metadata.json"))
	if err != nil {
		return nil, fmt.Errorf("display backup metadata missing; existing backup preserved: %w", err)
	}
	var record displayBackupRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if !strings.EqualFold(clean(record.GameRoot), clean(gameRoot)) {
		return nil, fmt.Errorf("display backup belongs to another game: %s", record.GameRoot)
	}
	if record.Present {
		if err := pathguard.Within(gameRoot, videoSettingsPath(gameRoot)); err != nil {
			return nil, err
		}
		if _, err := time.Parse(time.RFC3339Nano, record.ModTime); err != nil {
			return nil, fmt.Errorf("invalid display backup timestamp: %w", err)
		}
		src := displaySettingsBackupPath(resRoot)
		if err := pathguard.Within(resRoot, src); err != nil {
			return nil, err
		}
		hash, err := fileHash(src)
		if err != nil {
			return nil, err
		}
		if record.SHA256 == "" || !strings.EqualFold(hash, record.SHA256) {
			return nil, fmt.Errorf("display backup hash mismatch")
		}
	}
	return &record, nil
}

func restoreDisplaySettings(gameRoot, resRoot string) error {
	record, err := checkedDisplayBackup(gameRoot, resRoot)
	if err != nil {
		return err
	}
	if record == nil || !record.Present {
		return nil
	}
	dst := videoSettingsPath(gameRoot)
	if err := pathguard.Within(gameRoot, dst); err != nil {
		return err
	}
	if err := copyFile(displaySettingsBackupPath(resRoot), dst); err != nil {
		return err
	}
	if err := os.Chmod(dst, os.FileMode(record.Mode)); err != nil {
		return err
	}
	modified, err := time.Parse(time.RFC3339Nano, record.ModTime)
	if err != nil {
		return err
	}
	return os.Chtimes(dst, modified, modified)
}
