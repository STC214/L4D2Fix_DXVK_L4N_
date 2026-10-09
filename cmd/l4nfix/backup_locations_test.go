//go:build windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestMovedPortableBackupRestore(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "relative", true: "legacy-absolute"}[legacy], func(t *testing.T) {
			r := t.TempDir()
			game := filepath.Join(r, "game")
			old := filepath.Join(r, "old", ".l4n_auto_backup")
			newRoot := filepath.Join(r, "new", ".l4n_auto_backup")
			testFiles(t, game, map[string][]byte{"original.txt": []byte("installed"), "added.txt": []byte("added")})
			testFiles(t, old, map[string][]byte{"files/original.txt": []byte("original")})
			steam := filepath.Join(r, "Steam", "userdata", "123", "config", "localconfig.vdf")
			testFiles(t, filepath.Dir(steam), map[string][]byte{"localconfig.vdf": []byte("new steam")})
			steamBackup := filepath.Join(old, "steam", steamBackupName(steam))
			testFiles(t, filepath.Dir(steamBackup), map[string][]byte{filepath.Base(steamBackup): []byte("old steam")})
			original := filepath.Join(old, "files", "original.txt")
			h, _ := fileHash(original)
			sh, _ := fileHash(steamBackup)
			m := &manifest{GameRoot: game, Files: []fileEntry{{Target: filepath.Join(game, "original.txt"), Rel: "original.txt", Existed: true, Backup: original, BackupSHA256: h}, {Target: filepath.Join(game, "added.txt"), Rel: "added.txt"}}, SteamConfigs: []steamEntry{{Target: steam, Existed: true, Backup: steamBackup, BackupSHA256: sh}}}
			if legacy {
				b, _ := json.Marshal(m)
				if err := os.WriteFile(filepath.Join(old, "manifest.json"), b, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := saveManifest(m, old); err != nil {
					t.Fatal(err)
				}
				b, _ := os.ReadFile(filepath.Join(old, "manifest.json"))
				var saved manifest
				json.Unmarshal(b, &saved)
				if filepath.IsAbs(saved.Files[0].Backup) || filepath.IsAbs(saved.SteamConfigs[0].Backup) {
					t.Fatal("not portable")
				}
				if m.Files[0].Backup != original {
					t.Fatal("live manifest mutated")
				}
			}
			if err := os.MkdirAll(filepath.Dir(newRoot), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(old, newRoot); err != nil {
				t.Fatal(err)
			}
			// A stale old location must never take precedence over moved backups.
			testFiles(t, old, map[string][]byte{"files/original.txt": []byte("WRONG OLD COPY")})
			if _, err := checkedInstallManifest(newRoot, game); err != nil {
				t.Fatal(err)
			}
			loaded, err := checkedInstallManifest(newRoot, game)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveManifest(loaded, newRoot); err != nil {
				t.Fatal(err)
			}
			third := filepath.Join(r, "third", ".l4n_auto_backup")
			if err := os.MkdirAll(filepath.Dir(third), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(newRoot, third); err != nil {
				t.Fatal(err)
			}
			newRoot = third
			if err := restoreFromBackup(newRoot); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(filepath.Join(game, "original.txt"))
			if string(b) != "original" {
				t.Fatal(string(b))
			}
			b, _ = os.ReadFile(steam)
			if string(b) != "old steam" {
				t.Fatal(string(b))
			}
			if exists(filepath.Join(game, "added.txt")) || exists(newRoot) {
				t.Fatal("cleanup incomplete")
			}
		})
	}
}

func TestBackupLocationRejectsInvalidPaths(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, ".l4n_auto_backup")
	for _, p := range []string{"", `..\files\original.txt`, filepath.Join(r, "outside.txt"), `files\..\files\original.txt`, `files\other.txt`} {
		m := &manifest{GameRoot: filepath.Join(r, "game"), Files: []fileEntry{{Target: filepath.Join(r, "game", "original.txt"), Rel: "original.txt", Existed: true, Backup: p}}}
		if err := resolveBackupLocations(m, root); err == nil {
			t.Fatalf("accepted %q", p)
		}
		if m.Files[0].Backup != p {
			t.Fatal("failure mutated input")
		}
	}
	m := &manifest{GameRoot: filepath.Join(r, "game"), Files: []fileEntry{{Target: filepath.Join(r, "game", "different.txt"), Rel: "original.txt", Existed: true, Backup: `files\original.txt`}}}
	if err := resolveBackupLocations(m, root); err == nil {
		t.Fatal("target/relative mismatch accepted")
	}
}

func TestRelocatedBackupAndLogRejectJunctions(t *testing.T) {
	for _, kind := range []string{"backup", "logs", "root"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			outside := filepath.Join(r, "outside")
			os.MkdirAll(outside, 0755)
			root := filepath.Join(r, "portable")
			os.MkdirAll(root, 0755)
			link := filepath.Join(root, "logs")
			if kind == "backup" {
				link = filepath.Join(root, ".l4n_auto_backup")
				testFiles(t, outside, map[string][]byte{"files/original.txt": []byte("keep")})
			}
			if kind == "root" {
				os.Remove(root)
				link = root
			}
			linkCmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
			linkCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if out, err := linkCmd.CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
			if kind == "backup" {
				m := &manifest{GameRoot: filepath.Join(r, "game"), Files: []fileEntry{{Target: filepath.Join(r, "game", "original.txt"), Rel: "original.txt", Existed: true, Backup: `files\original.txt`}}}
				if err := resolveBackupLocations(m, link); err == nil {
					t.Fatal("linked backup accepted")
				}
			} else if err := appendSessionLog(root, "blocked"); err == nil {
				t.Fatal("linked log accepted")
			}
		})
	}
}

func TestProductionBackupReadOnly(t *testing.T) {
	root := os.Getenv("L4N_TEST_EXISTING_BACKUP")
	if root == "" {
		t.Skip("set L4N_TEST_EXISTING_BACKUP for read-only diagnosis")
	}
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if err := resolveBackupLocations(&m, root); err != nil {
		t.Fatal(err)
	}
	if err := validateManifestBackupPaths(&m, root); err != nil {
		t.Fatal(err)
	}
	if err := validateManifestTargets(&m); err != nil {
		t.Fatal(err)
	}
	if err := validateOriginalBackups(&m); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil || string(after) != string(b) {
		t.Fatal("production manifest changed")
	}
	t.Logf("READ ONLY PASS: %d game records, %d Steam backups resolved and validated; no restore executed", len(m.Files), len(m.SteamConfigs))
}

func TestMovedBackupDamageStopsAllMutation(t *testing.T) {
	for _, damage := range []string{"missing", "hash"} {
		t.Run(damage, func(t *testing.T) {
			r := t.TempDir()
			game := filepath.Join(r, "game")
			root := filepath.Join(r, "new", ".l4n_auto_backup")
			testFiles(t, game, map[string][]byte{"added.txt": []byte("keep"), "original.txt": []byte("installed")})
			testFiles(t, root, map[string][]byte{"files/original.txt": []byte("damaged")})
			m := &manifest{GameRoot: game, Files: []fileEntry{{Target: filepath.Join(game, "added.txt"), Rel: "added.txt"}, {Target: filepath.Join(game, "original.txt"), Rel: "original.txt", Existed: true, Backup: filepath.Join(r, "old", ".l4n_auto_backup", "files", "original.txt"), BackupSHA256: strings.Repeat("0", 64)}}}
			b, _ := json.Marshal(m)
			os.WriteFile(filepath.Join(root, "manifest.json"), b, 0600)
			if damage == "missing" {
				os.Remove(filepath.Join(root, "files", "original.txt"))
			}
			if err := restoreFromBackup(root); err == nil {
				t.Fatal("accepted damaged backup")
			}
			b, _ = os.ReadFile(filepath.Join(game, "added.txt"))
			if string(b) != "keep" {
				t.Fatal("removed before preflight")
			}
			b, _ = os.ReadFile(filepath.Join(game, "original.txt"))
			if string(b) != "installed" {
				t.Fatal("modified before preflight")
			}
		})
	}
}

func TestSessionLogConcurrentAndFailure(t *testing.T) {
	r := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := appendSessionLog(r, "日志测试"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	files, _ := filepath.Glob(filepath.Join(r, "logs", "*.log"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	b, _ := os.ReadFile(files[0])
	if strings.Count(string(b), "日志测试") != 20 {
		t.Fatal(string(b))
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "logs"), []byte("blocked"), 0600)
	if err := appendSessionLog(bad, "failure"); err == nil {
		t.Fatal("ignored write failure")
	}
}
