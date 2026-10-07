//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"strings"
)

const modBackupMetadata = ".l4n-mod-backup.json"

type modBackupFile struct {
	Relative string `json:"relative"`
	SHA256   string `json:"sha256"`
}
type modBackupRecord struct {
	GameRoot string          `json:"gameRoot"`
	Files    []modBackupFile `json:"files"`
}

func backupModsAt(gameRoot, resRoot string) error {
	addons := filepath.Join(gameRoot, "left4dead2", "addons")
	if err := pathguard.Within(gameRoot, addons); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(addons, modBackupMetadata)); err == nil {
		return fmt.Errorf("reserved backup metadata name in addons")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(resRoot, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(resRoot, ".mods-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copyDirContents(addons, stage, 10, 75); err != nil {
		return err
	}
	record := modBackupRecord{GameRoot: clean(gameRoot)}
	err = filepath.WalkDir(stage, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stage, p)
		if err != nil {
			return err
		}
		hash, err := fileHash(p)
		if err != nil {
			return err
		}
		sourceHash, err := fileHash(filepath.Join(addons, rel))
		if err != nil || hash != sourceHash {
			return fmt.Errorf("MOD changed during backup: %s", rel)
		}
		record.Files = append(record.Files, modBackupFile{Relative: rel, SHA256: hash})
		return nil
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(stage, modBackupMetadata), data, 0644); err != nil {
		return err
	}
	if err := replaceDir(stage, filepath.Join(resRoot, modBackupDirName)); err != nil {
		return err
	}
	if err := backupDisplaySettings(gameRoot, resRoot); err != nil {
		return fmt.Errorf("MOD backup saved; display backup incomplete: %w", err)
	}
	return nil
}

func restoreModsAt(gameRoot, resRoot string) error {
	backup := filepath.Join(resRoot, modBackupDirName)
	meta := filepath.Join(backup, modBackupMetadata)
	if err := pathguard.Within(resRoot, meta); err != nil {
		return err
	}
	data, err := os.ReadFile(meta)
	if err != nil {
		return fmt.Errorf("MOD backup metadata missing; existing backup preserved: %w", err)
	}
	var record modBackupRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	if !strings.EqualFold(clean(record.GameRoot), clean(gameRoot)) {
		return fmt.Errorf("MOD backup belongs to another game: %s", record.GameRoot)
	}
	seen := map[string]bool{}
	addons := filepath.Join(gameRoot, "left4dead2", "addons")
	if err := pathguard.Within(gameRoot, addons); err != nil {
		return err
	}
	for _, file := range record.Files {
		rel, err := packageRelative(file.Relative)
		if err != nil || rel == "" || strings.EqualFold(rel, modBackupMetadata) {
			return fmt.Errorf("invalid MOD backup path")
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return fmt.Errorf("duplicate MOD backup path")
		}
		seen[key] = true
		src := filepath.Join(backup, rel)
		if err := pathguard.Within(resRoot, src); err != nil {
			return err
		}
		if err := pathguard.Within(gameRoot, filepath.Join(addons, rel)); err != nil {
			return err
		}
		hash, err := fileHash(src)
		if err != nil {
			return err
		}
		if file.SHA256 == "" || !strings.EqualFold(hash, file.SHA256) {
			return fmt.Errorf("MOD backup hash mismatch: %s", rel)
		}
	}
	err = filepath.WalkDir(backup, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if pathguard.Linked(info) {
			return fmt.Errorf("linked MOD backup: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(backup, p)
		if err != nil {
			return err
		}
		if rel != modBackupMetadata && !seen[strings.ToLower(rel)] {
			return fmt.Errorf("unrecorded MOD backup file: %s", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err := checkedDisplayBackup(gameRoot, resRoot); err != nil {
		return err
	}
	if err := copyDirContentsSkipping(backup, addons, 10, 75, map[string]bool{modBackupMetadata: true}); err != nil {
		return err
	}
	if err := restoreDisplaySettings(gameRoot, resRoot); err != nil {
		return fmt.Errorf("MOD restored; display restore incomplete: %w", err)
	}
	return nil
}
