//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func platformTestEnvironment(t *testing.T) (root, res, game string) {
	t.Helper()
	root = t.TempDir()
	res = filepath.Join(root, "resources")
	game = filepath.Join(root, "game")
	testFiles(t, game, map[string][]byte{"left4dead2.exe": []byte("game"), "left4dead2/cfg/autoexec.cfg": []byte("// user\necho hello\n"), "Evolved/config.txt": []byte("Other=keep\nNoHmdMinimapEnabled=false\nNoHmdWorldNavigationEnabled=false\n"), "Evolved/config2.txt": []byte("Other2=keep\nNoHmdMinimapEnabled=false\n"), "bin/neko/plugins/CSessionDefaults.dll": []byte("existing C")})
	backup := filepath.Join(root, ".l4n_auto_backup")
	m := &manifest{GameRoot: game}
	src := filepath.Join(root, "base-source.dll")
	os.WriteFile(src, testDLL(0x14c), 0644)
	for _, rel := range []string{"bin/left4neko.dll", "dxgi.dll", "dxvk_d3d9.dll", "bin/dxvk_d3d9.dll"} {
		if e := copyWithBackup(m, backup, game, src, filepath.Join(game, filepath.FromSlash(rel))); e != nil {
			t.Fatal(e)
		}
	}
	var idx workshopIndex
	for i, p := range workshopPlatforms {
		archive := filepath.Join(p.Kind, p.Name+"-test.zip")
		testZip(t, filepath.Join(res, archive), workshopFixture(p.Kind))
		st, _ := os.Stat(filepath.Join(res, archive))
		h, _ := fileHash(filepath.Join(res, archive))
		idx.Resources = append(idx.Resources, workshopResource{Platform: p.Kind, Detail: workshopDetail{ID: p.ID, Content: "123", Updated: int64(100 + i), Size: steamInt(st.Size())}, Version: "test", Archive: archive, SHA256: h})
	}
	b, _ := json.Marshal(idx)
	if e := os.WriteFile(filepath.Join(res, "workshop-index.json"), b, 0644); e != nil {
		t.Fatal(e)
	}
	return
}
func TestPlatformDeployMutualExclusionNavigationAndRollback(t *testing.T) {
	for _, kind := range []string{"skeeto", "evolved"} {
		t.Run(kind, func(t *testing.T) {
			root, res, game := platformTestEnvironment(t)
			auto, _ := os.ReadFile(filepath.Join(game, "left4dead2/cfg/autoexec.cfg"))
			cfg, _ := os.ReadFile(filepath.Join(game, "Evolved/config.txt"))
			cfg2, _ := os.ReadFile(filepath.Join(game, "Evolved/config2.txt"))
			baseline, _ := os.ReadFile(filepath.Join(root, ".l4n_auto_backup/manifest.json"))
			if e := deployPlatform(root, res, game, kind, ""); e != nil {
				t.Fatal(e)
			}
			if _, e := ensureBaseDeployed(root, game); e != nil {
				t.Fatal("base changed:", e)
			}
			if e := deployPlatform(root, res, game, kind, ""); e == nil {
				t.Fatal("second platform allowed")
			}
			if kind == "skeeto" {
				b, _ := os.ReadFile(filepath.Join(game, "left4dead2/cfg/autoexec.cfg"))
				if !strings.Contains(string(b), "skeeto_nav 1") || !strings.Contains(string(b), "echo hello") {
					t.Fatal("navigation missing/user cfg lost")
				}
			} else {
				for _, rel := range []string{"Evolved/config.txt", "Evolved/config2.txt"} {
					b, _ := os.ReadFile(filepath.Join(game, rel))
					if !strings.Contains(string(b), "NoHmdMinimapEnabled=true") || !strings.Contains(string(b), "NoHmdWorldNavigationEnabled=true") {
						t.Fatal("Evo navigation disabled")
					}
				}
				if exists(filepath.Join(game, "winmm.dll")) || exists(filepath.Join(game, "bin/winmm.dll")) || exists(filepath.Join(game, "Evolved/d3d9.dll")) {
					t.Fatal("alternate renderer/loader installed")
				}
			}
			if e := restorePlatform(root, game, true); e != nil {
				t.Fatal(e)
			}
			for rel, want := range map[string][]byte{"left4dead2/cfg/autoexec.cfg": auto, "Evolved/config.txt": cfg, "Evolved/config2.txt": cfg2} {
				b, _ := os.ReadFile(filepath.Join(game, rel))
				if string(b) != string(want) {
					t.Fatalf("original not restored: %s", rel)
				}
			}
			now, _ := os.ReadFile(filepath.Join(root, ".l4n_auto_backup/manifest.json"))
			if string(now) != string(baseline) {
				t.Fatal("base manifest changed")
			}
			if platformInstalled(root) {
				t.Fatal("platform journal not cleared")
			}
			b, _ := os.ReadFile(filepath.Join(game, "bin/neko/plugins/CSessionDefaults.dll"))
			if string(b) != "existing C" {
				t.Fatal("C changed")
			}
			if exists(filepath.Join(game, "skeeto.dll")) || exists(filepath.Join(game, "bin/neko/plugins/L4D2Evolved.dll")) {
				t.Fatal("core not removed")
			}
			t.Log("base intact; navigation enabled; second deployment blocked; exact originals restored")
		})
	}
}
func TestPlatformRequiresBaseAndVerifiedDownload(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	if e := os.Remove(filepath.Join(game, "dxgi.dll")); e != nil {
		t.Fatal(e)
	}
	if e := deployPlatform(root, res, game, "skeeto", ""); e == nil {
		t.Fatal("missing base accepted")
	}
	if platformInstalled(root) || exists(filepath.Join(game, "skeeto.dll")) {
		t.Fatal("prerequisite failure wrote files")
	}
	root, res, game = platformTestEnvironment(t)
	_, p, e := selectWorkshopResource(res, "skeeto", "")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("tampered"), 0644)
	if e = deployPlatform(root, res, game, "skeeto", ""); e == nil {
		t.Fatal("tampered archive accepted")
	}
}
func TestPlatformLeavesUnmanagedAndModifiedFiles(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	testFiles(t, game, map[string][]byte{"bin/neko/plugins/L4D2Evolved.dll": []byte("unmanaged")})
	if e := deployPlatform(root, res, game, "skeeto", ""); e == nil {
		t.Fatal("unmanaged platform accepted")
	}
	b, _ := os.ReadFile(filepath.Join(game, "bin/neko/plugins/L4D2Evolved.dll"))
	if string(b) != "unmanaged" {
		t.Fatal("unmanaged deleted")
	}
	root, res, game = platformTestEnvironment(t)
	if e := deployPlatform(root, res, game, "skeeto", ""); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(game, "skeeto.dll"), []byte("external update"), 0644)
	if e := restorePlatform(root, game, true); e != nil {
		t.Fatal(e)
	}
	entries, _ := filepath.Glob(filepath.Join(resourceRoot(root), "platform_cleanup_backups/skeeto/*/skeeto.dll"))
	if len(entries) != 1 {
		t.Fatal("updated platform not archived before cleanup")
	}
	b, _ = os.ReadFile(entries[0])
	if string(b) != "external update" {
		t.Fatal("archive mismatch")
	}
	if platformInstalled(root) || exists(filepath.Join(game, "skeeto.dll")) {
		t.Fatal("cleanup incomplete")
	}
}
func TestPlatformCleanupKeepsLaterAutoexecEdits(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	if e := deployPlatform(root, res, game, "skeeto", ""); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(game, "left4dead2/cfg/autoexec.cfg")
	b, _ := os.ReadFile(p)
	b = append(b, []byte("echo later-user-edit\r\n")...)
	os.WriteFile(p, b, 0644)
	if e := restorePlatform(root, game, true); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "echo later-user-edit") || strings.Contains(string(b), "skeeto_nav") {
		t.Fatal("user cfg edits lost or platform block remained")
	}
}
func TestPlatformFailedDeploymentRollsBack(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	rel := "Skeeto/README.txt"
	testFiles(t, game, map[string][]byte{rel: []byte("original README")})
	ptr, _ := syscall.UTF16PtrFromString(filepath.Join(game, filepath.FromSlash(rel)))
	h, e := syscall.CreateFile(ptr, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	e = deployPlatform(root, res, game, "skeeto", "")
	syscall.CloseHandle(h)
	if e == nil {
		t.Fatal("locked target did not fail")
	}
	if platformInstalled(root) || exists(filepath.Join(game, "skeeto.dll")) || exists(filepath.Join(game, "bin/wininet.dll")) {
		t.Fatal("failed deployment left installed files/journal")
	}
	b, _ := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
	if string(b) != "original README" {
		t.Fatal("original lost")
	}
	if _, e := ensureBaseDeployed(root, game); e != nil {
		t.Fatal(e)
	}
}
func TestOperationLockExcludesOtherInstances(t *testing.T) {
	root := t.TempDir()
	release, e := acquireOperationLock(root, "test.lock")
	if e != nil {
		t.Fatal(e)
	}
	if second, e := acquireOperationLock(root, "test.lock"); e == nil {
		second()
		t.Fatal("second lock acquired")
	}
	release()
	again, e := acquireOperationLock(root, "test.lock")
	if e != nil {
		t.Fatal("lock not released:", e)
	}
	again()
}
func TestPlatformRejectsForeignProxyAndCleanupTargets(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	testFiles(t, game, map[string][]byte{"bin/wininet.dll": []byte("another proxy")})
	if e := deployPlatform(root, res, game, "skeeto", ""); e == nil {
		t.Fatal("foreign proxy overwritten")
	}
	b, _ := os.ReadFile(filepath.Join(game, "bin/wininet.dll"))
	if string(b) != "another proxy" {
		t.Fatal("foreign proxy changed")
	}
	root, res, game = platformTestEnvironment(t)
	if e := deployPlatform(root, res, game, "evolved", ""); e != nil {
		t.Fatal(e)
	}
	m, e := checkedInstallManifest(filepath.Join(root, platformBackupName), game)
	if e != nil {
		t.Fatal(e)
	}
	rel := "bin/neko/plugins/CSessionDefaults.dll"
	target := filepath.Join(game, filepath.FromSlash(rel))
	hash, _ := fileHash(target)
	m.Files = append(m.Files, fileEntry{Target: target, Rel: filepath.FromSlash(rel), SourceSHA256: hash})
	if e = saveManifest(m, filepath.Join(root, platformBackupName)); e != nil {
		t.Fatal(e)
	}
	if e = restorePlatform(root, game, true); e == nil {
		t.Fatal("foreign cleanup target accepted")
	}
	b, _ = os.ReadFile(target)
	if string(b) != "existing C" {
		t.Fatal("C file changed")
	}
}
func TestEvolvedConfigPreservesOtherValuesAndRemovesDuplicates(t *testing.T) {
	b := evolvedNavigationConfig([]byte("\xef\xbb\xbf# header\nOther=keep\nNoHmdMinimapEnabled=false\nNoHmdMinimapEnabled=true\nNoHmdWorldNavigationEnabled=false\n"))
	for _, k := range []string{"NoHmdMinimapEnabled=", "NoHmdWorldNavigationEnabled="} {
		if strings.Count(string(b), k) != 1 {
			t.Fatal(string(b))
		}
	}
	if !strings.Contains(string(b), "Other=keep") {
		t.Fatal(string(b))
	}
}
