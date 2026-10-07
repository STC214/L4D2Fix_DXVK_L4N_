//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFontBackupIntegrityAndTargetPreflight(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	backup := filepath.Join(root, "backup")
	os.MkdirAll(game, 0755)
	original := filepath.Join(game, "font.dll")
	source := filepath.Join(root, "source.dll")
	os.WriteFile(original, []byte("original"), 0644)
	os.WriteFile(source, []byte("new"), 0644)
	m, err := checkedFontManifest(backup, game)
	if err != nil {
		t.Fatal(err)
	}
	if err = copyWithFontBackup(m, backup, game, source, original); err != nil {
		t.Fatal(err)
	}
	if m.Files[0].BackupSHA256 == "" {
		t.Fatal("no backup hash")
	}
	os.WriteFile(m.Files[0].Backup, []byte("damaged"), 0644)
	if err = copyWithFontBackup(m, backup, game, source, original); err == nil {
		t.Fatal("damaged backup accepted")
	}
	if _, err = checkedFontManifest(backup, game); err == nil {
		t.Fatal("damaged manifest accepted")
	}
	m.Files = nil
	if err = copyWithFontBackup(m, backup, game, source, filepath.Join(root, "outside.dll")); err == nil {
		t.Fatal("outside target accepted")
	}
}

func TestFontBackupRejectCorruptForeignAndMissingManifest(t *testing.T) {
	for _, scenario := range []string{"corrupt", "foreign", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			backup := filepath.Join(root, "backup")
			os.MkdirAll(backup, 0755)
			switch scenario {
			case "corrupt":
				os.WriteFile(filepath.Join(backup, "manifest.json"), []byte("{"), 0644)
			case "foreign":
				saveFontManifest(&fontManifest{GameRoot: filepath.Join(root, "other")}, backup)
			case "missing":
				os.WriteFile(filepath.Join(backup, "orphan"), []byte("original"), 0644)
			}
			if _, err := checkedFontManifest(backup, filepath.Join(root, "game")); err == nil {
				t.Fatal("invalid backup accepted")
			}
		})
	}
}

func TestAtomicFontCopySamePath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("preserved"), 0644)
	if err := copyFile(p, p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "preserved" {
		t.Fatal("same-path copy truncated")
	}
}
