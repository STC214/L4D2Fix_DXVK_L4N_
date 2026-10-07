//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMODBackupRestoreAndPreflight(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	testFiles(t, game, map[string][]byte{"left4dead2/addons/a.vpk": []byte("a"), "left4dead2/addons/nested/b.vpk": []byte("b"), videoSettingsRelativePath: []byte("video")})
	if err := backupModsAt(game, res); err != nil {
		t.Fatal(err)
	}
	if err := restoreModsAt(filepath.Join(r, "foreign"), res); err == nil {
		t.Fatal("foreign MOD restore accepted")
	}
	a := filepath.Join(game, "left4dead2/addons/a.vpk")
	os.WriteFile(a, []byte("changed"), 0644)
	os.WriteFile(videoSettingsPath(game), []byte("changed video"), 0644)
	if err := restoreModsAt(game, res); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(a)
	if string(content) != "a" {
		t.Fatal("MOD restore failed")
	}
	content, _ = os.ReadFile(videoSettingsPath(game))
	if string(content) != "video" {
		t.Fatal("video restore failed")
	}
	if _, err := os.Stat(filepath.Join(game, "left4dead2/addons", modBackupMetadata)); !os.IsNotExist(err) {
		t.Fatal("backup metadata deployed to game")
	}
	os.WriteFile(a, []byte("leave untouched"), 0644)
	os.WriteFile(filepath.Join(res, modBackupDirName, "nested/b.vpk"), []byte("damaged"), 0644)
	if err := restoreModsAt(game, res); err == nil {
		t.Fatal("damaged MOD backup accepted")
	}
	content, _ = os.ReadFile(a)
	if string(content) != "leave untouched" {
		t.Fatal("MOD partially restored before validation")
	}
}

func TestMODRestorePreservesLegacyUnidentifiedBackup(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	testFiles(t, res, map[string][]byte{filepath.Join(modBackupDirName, "a.vpk"): []byte("legacy")})
	if err := restoreModsAt(game, res); err == nil {
		t.Fatal("unidentified legacy backup accepted")
	}
	b, _ := os.ReadFile(filepath.Join(res, modBackupDirName, "a.vpk"))
	if string(b) != "legacy" {
		t.Fatal("legacy backup lost")
	}
}
