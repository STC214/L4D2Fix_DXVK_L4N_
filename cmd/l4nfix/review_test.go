//go:build windows

package main

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyZIPInstructionNames(t *testing.T) {
	for _, unicodeExtra := range []bool{false, true} {
		root := t.TempDir()
		p := filepath.Join(root, "legacy.zip")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		z := zip.NewWriter(f)
		for n, b := range l4nFixture() {
			w, e := z.Create(n)
			if e != nil {
				t.Fatal(e)
			}
			w.Write(b)
		}
		raw := "wrapper/L4N/" + string([]byte{0xc6, 0xf4, 0xb6, 0xaf, 0xcf, 0xee, 0xd6, 0xb8, 0xc1, 0xee, 0xa1, 0xbe, 0xcb, 0xc4, 0xa1, 0xbf}) + ".txt"
		decoded := "wrapper/L4N/启动项指令【四】.txt"
		h := &zip.FileHeader{Name: raw, NonUTF8: true, Method: zip.Deflate}
		if unicodeExtra {
			extra := make([]byte, 9)
			binary.LittleEndian.PutUint16(extra, 0x7075)
			binary.LittleEndian.PutUint16(extra[2:], uint16(5+len(decoded)))
			extra[4] = 1
			binary.LittleEndian.PutUint32(extra[5:], crc32.ChecksumIEEE([]byte(raw)))
			h.Extra = append(extra, []byte(decoded)...)
		}
		w, e := z.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte("-steam -vulkan"))
		z.Close()
		f.Close()
		info, e := preparePackage(p, filepath.Join(root, "resources"), "l4n")
		if e != nil {
			t.Fatal(e)
		}
		if info.LaunchInstructions == nil || info.LaunchInstructions.Options != "-steam -vulkan" {
			t.Fatal("legacy TXT not recognized")
		}
		for _, m := range info.Files {
			if isLaunchInstructionFile(m.Source) {
				t.Fatal("instruction copied to game")
			}
		}
	}
}

func TestDamagedManifestAndBackupStopsMutation(t *testing.T) {
	for _, scenario := range []string{"invalid-json", "foreign-game", "missing-backup", "changed-backup", "outside-target"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			game := filepath.Join(root, "game")
			backup := filepath.Join(root, "backup")
			target := filepath.Join(game, "original.txt")
			testFiles(t, game, map[string][]byte{"original.txt": []byte("installed")})
			testFiles(t, backup, map[string][]byte{"files/original.txt": []byte("original")})
			original := filepath.Join(backup, "files/original.txt")
			h, _ := fileHash(original)
			m := &manifest{GameRoot: game, Files: []fileEntry{{Target: target, Rel: "original.txt", Existed: true, Backup: original, BackupSHA256: h}}}
			switch scenario {
			case "foreign-game":
				m.GameRoot = filepath.Join(root, "other")
			case "missing-backup":
				os.Remove(original)
			case "changed-backup":
				os.WriteFile(original, []byte("damaged"), 0600)
			case "outside-target":
				m.Files[0].Target = filepath.Join(root, "outside.txt")
			}
			data, _ := json.Marshal(m)
			if scenario == "invalid-json" {
				data = []byte("{")
			}
			os.WriteFile(filepath.Join(backup, "manifest.json"), data, 0600)
			if _, e := checkedInstallManifest(backup, game); e == nil {
				t.Fatal("bad manifest accepted")
			}
			if scenario != "foreign-game" {
				if e := restoreFromBackup(backup); e == nil {
					t.Fatal("bad restore accepted")
				}
			}
			b, _ := os.ReadFile(target)
			if string(b) != "installed" {
				t.Fatal("game changed before rejection")
			}
			b, _ = os.ReadFile(filepath.Join(backup, "manifest.json"))
			if string(b) != string(data) {
				t.Fatal("manifest overwritten")
			}
		})
	}
}

func TestRestoreArchivesUserEditedConfig(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	backup := filepath.Join(root, ".l4n_auto_backup")
	res := filepath.Join(root, "resources")
	os.MkdirAll(res, 0755)
	config := filepath.Join(game, filepath.FromSlash(configRelativePath))
	incoming := filepath.Join(root, "new.vdf")
	testFiles(t, game, map[string][]byte{configRelativePath: []byte("original user configuration")})
	testFiles(t, root, map[string][]byte{"new.vdf": []byte("installed defaults")})
	m := loadManifest(backup, game)
	if e := copyWithBackup(m, backup, game, incoming, config); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(config, []byte("edited after install"), 0600)
	if e := restoreFromBackup(backup); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(config)
	if string(b) != "original user configuration" {
		t.Fatal("original not restored")
	}
	count := 0
	filepath.WalkDir(filepath.Join(res, configArchiveDirName), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && d.Name() == "config.vdf" {
			b, _ := os.ReadFile(p)
			if string(b) == "edited after install" {
				count++
			}
		}
		return nil
	})
	if count != 1 {
		t.Fatalf("edited config archives=%d", count)
	}
}

func TestPatchTraversalRejectedBeforeCopy(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	m := &manifest{GameRoot: game}
	files := []patchFile{{Src: "missing", Rel: "good.txt"}, {Src: "missing", Rel: "../outside.txt"}}
	if e := copyPatchFiles(m, filepath.Join(root, "backup"), game, files); e == nil || !strings.Contains(e.Error(), "路径") {
		t.Fatal("unsafe plan not rejected up front", e)
	}
}

func TestMissingManifestDoesNotReplaceOldBackups(t *testing.T) {
	root := t.TempDir()
	backup := filepath.Join(root, "backup")
	testFiles(t, backup, map[string][]byte{"files/old.txt": []byte("irreplaceable original")})
	if _, e := checkedInstallManifest(backup, filepath.Join(root, "game")); e == nil {
		t.Fatal("orphaned backups ignored")
	}
	b, _ := os.ReadFile(filepath.Join(backup, "files/old.txt"))
	if string(b) != "irreplaceable original" {
		t.Fatal("old backup changed")
	}
}

func TestSteamBackupIntegrityPreflight(t *testing.T) {
	root := t.TempDir()
	backup := filepath.Join(root, "steam.vdf")
	os.WriteFile(backup, []byte("original"), 0600)
	hash, _ := fileHash(backup)
	m := &manifest{SteamConfigs: []steamEntry{{Existed: true, Backup: backup, BackupSHA256: hash}}}
	if e := validateOriginalBackups(m); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(backup, []byte("changed"), 0600)
	if e := validateOriginalBackups(m); e == nil {
		t.Fatal("changed Steam backup accepted")
	}
}
