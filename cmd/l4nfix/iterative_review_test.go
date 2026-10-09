//go:build windows

package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDirectCopyRejectsOutsideGame(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	os.MkdirAll(game, 0755)
	src := filepath.Join(r, "source")
	os.WriteFile(src, []byte("new"), 0644)
	outside := filepath.Join(r, "outside")
	m := &manifest{GameRoot: game}
	if err := copyWithBackup(m, filepath.Join(r, "backup"), game, src, outside); err == nil {
		t.Fatal("outside target accepted")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatal("outside target changed")
	}
}

func TestReplaceDirKeepsUnrelatedOldFolder(t *testing.T) {
	r := t.TempDir()
	src := filepath.Join(r, "stage")
	dst := filepath.Join(r, "backup")
	for p, value := range map[string]string{filepath.Join(src, "new"): "new", filepath.Join(dst, "current"): "current", filepath.Join(dst+".old", "unrelated"): "preserve"} {
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(value), 0644)
	}
	if err := replaceDir(src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst+".old", "unrelated"))
	if err != nil || string(b) != "preserve" {
		t.Fatal("unrelated .old directory deleted")
	}
}

func TestInstallManifestRejectsOutsideBackupSource(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	backup := filepath.Join(r, "backup")
	testFiles(t, game, map[string][]byte{"target": []byte("old")})
	src := filepath.Join(r, "source")
	os.WriteFile(src, []byte("new"), 0644)
	m := &manifest{GameRoot: game}
	if err := copyWithBackup(m, backup, game, src, filepath.Join(game, "target")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(r, "outside-backup")
	os.WriteFile(outside, []byte("old"), 0644)
	m.Files[0].Backup = outside
	if err := saveManifest(m, backup); err == nil {
		t.Fatal("outside backup accepted by save")
	}
	// Simulate an externally tampered manifest despite the writer rejecting it.
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkedInstallManifest(backup, game); err == nil {
		t.Fatal("outside backup source accepted")
	}
}

func TestPatchTargetsAllPreflightBeforeCopy(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	outside := filepath.Join(r, "outside")
	os.MkdirAll(game, 0755)
	os.MkdirAll(outside, 0755)
	linkCmd := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(game, "z"), outside)
	linkCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := linkCmd.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, out)
	}
	src := filepath.Join(r, "source")
	os.WriteFile(src, []byte("new"), 0644)
	m := &manifest{GameRoot: game}
	files := []patchFile{{Src: src, Rel: "a.txt"}, {Src: src, Rel: "z/external.txt"}}
	if err := copyPatchFiles(m, filepath.Join(r, "backup"), game, files); err == nil {
		t.Fatal("linked target accepted")
	}
	if _, err := os.Stat(filepath.Join(game, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("mutation before all targets validated")
	}
	if _, err := os.Stat(filepath.Join(outside, "external.txt")); !os.IsNotExist(err) {
		t.Fatal("outside folder changed")
	}
}

func TestManifestRejectsDuplicateTargetsAndRelativeGame(t *testing.T) {
	game := t.TempDir()
	target := filepath.Join(game, "file")
	m := &manifest{GameRoot: game, Files: []fileEntry{{Target: target}, {Target: target}}}
	if err := validateManifestTargets(m); err == nil {
		t.Fatal("duplicate targets accepted")
	}
	if err := validateManifestTargets(&manifest{GameRoot: "relative-game"}); err == nil {
		t.Fatal("relative game root accepted")
	}
}

func TestDisplayBackupBoundToGameAndMissingSource(t *testing.T) {
	r := t.TempDir()
	res := filepath.Join(r, "resources")
	a := filepath.Join(r, "game-a")
	b := filepath.Join(r, "game-b")
	for game, value := range map[string]string{a: "A", b: "B"} {
		p := videoSettingsPath(game)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte(value), 0644)
	}
	if err := backupDisplaySettings(a, res); err != nil {
		t.Fatal(err)
	}
	if err := restoreDisplaySettings(b, res); err == nil {
		t.Fatal("foreign display backup accepted")
	}
	contents, _ := os.ReadFile(videoSettingsPath(b))
	if string(contents) != "B" {
		t.Fatal("foreign backup overwrote game")
	}
	os.Remove(videoSettingsPath(b))
	if err := backupDisplaySettings(b, res); err != nil {
		t.Fatal(err)
	}
	if err := restoreDisplaySettings(b, res); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(videoSettingsPath(b)); !os.IsNotExist(err) {
		t.Fatal("stale display backup restored")
	}
}

func TestVersionIdentityRejectsInvalidArchiveMembers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.zip")
	f, _ := os.Create(p)
	z := zip.NewWriter(f)
	w, _ := z.Create("../unrelated.txt")
	w.Write([]byte("bad"))
	w, _ = z.Create("x32/dxgi.dll")
	w.Write([]byte("identity"))
	z.Close()
	f.Close()
	if _, err := resourceIdentityFor(p, "dxvk"); err == nil {
		t.Fatal("invalid archive accepted for version identity")
	}
}

func TestReplaceDirFailureRestoresOriginal(t *testing.T) {
	r := t.TempDir()
	dst := filepath.Join(r, "backup")
	testFiles(t, dst, map[string][]byte{"original": []byte("keep")})
	if err := replaceDir(filepath.Join(r, "missing-stage"), dst); err == nil {
		t.Fatal("missing source accepted")
	}
	b, err := os.ReadFile(filepath.Join(dst, "original"))
	if err != nil || string(b) != "keep" {
		t.Fatal("original directory lost")
	}
}

func TestResourceInventoryRejectsJunction(t *testing.T) {
	r := t.TempDir()
	source := filepath.Join(r, "source")
	outside := filepath.Join(r, "outside")
	os.MkdirAll(source, 0755)
	testFiles(t, outside, map[string][]byte{"dxgi.dll": []byte("outside")})
	linkCmd := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(source, "linked"), outside)
	linkCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := linkCmd.CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, out)
	}
	if _, err := packageInventory(source); err == nil {
		t.Fatal("linked resource directory accepted")
	}
	if err := copyDirContents(source, filepath.Join(r, "copy"), 0, 100); err == nil {
		t.Fatal("linked MOD source accepted")
	}
}

func TestDisplayCorruptionPreventsRestore(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	testFiles(t, game, map[string][]byte{videoSettingsRelativePath: []byte("original")})
	if err := backupDisplaySettings(game, res); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(displaySettingsBackupPath(res), []byte("damaged"), 0644)
	os.WriteFile(videoSettingsPath(game), []byte("leave unchanged"), 0644)
	if err := restoreDisplaySettings(game, res); err == nil {
		t.Fatal("damaged display backup accepted")
	}
	b, _ := os.ReadFile(videoSettingsPath(game))
	if string(b) != "leave unchanged" {
		t.Fatal("target overwritten")
	}
}

func TestAllOwnerDrawActionLabels(t *testing.T) {
	for _, id := range []uint32{idRefreshVersion, idRun, idBackupMod, idRestoreMod, idClean, idClose} {
		if label := buttonText(id); label == "" {
			t.Fatalf("blank owner-draw label: id=%d", id)
		}
	}
}
