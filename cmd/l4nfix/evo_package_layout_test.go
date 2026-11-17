//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvoPackageDrivenDeployAndRollback(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	payload := workshopFixture("evolved")
	additions := []string{"bin/neko/plugins/L4D2EvoNavigation.dll", "bin/neko/plugins/L4D2EvoFeedback.dll", "bin/neko/plugins/L4D2EvoDecalGuard.dll", "bin/neko/plugins/Left4Meow.dll", "bin/neko/plugins/evo/L4D2Evo.dll", "bin/neko/plugins/evo/L4D2EvoModelManagement.dll", "bin/neko/plugins/evo/L4D2EvoModManager.dll", "bin/neko/plugins/future/assets/settings.json"}
	for _, rel := range additions {
		payload[rel] = testDLL(0x14c)
	}
	payload["Evolved/config.txt"] = []byte("PackageDefault=keep\nNoHmdMinimapEnabled=false\n")
	payload["Evolved/config2.txt"] = []byte("NewDefault=keep\n")
	payload["left4dead2/scripts/future/evo.nut"] = []byte("// package runtime script")
	additions = append(additions, "left4dead2/scripts/future/evo.nut")
	// Existing user config must win over package defaults. New configs retain
	// package settings, and an overwritten plugin must restore byte-for-byte.
	if err := os.Remove(filepath.Join(game, "Evolved/config2.txt")); err != nil {
		t.Fatal(err)
	}
	testFiles(t, game, map[string][]byte{additions[0]: []byte("prior plugin")})
	archive := filepath.Join(res, "evolved/L4D2Evolved-test.zip")
	testZip(t, archive, payload)
	// This test exercises an updated verified resource, not a stale index hash.
	var index workshopIndex
	b, err := os.ReadFile(filepath.Join(res, "workshop-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &index); err != nil {
		t.Fatal(err)
	}
	for i := range index.Resources {
		if index.Resources[i].Platform == "evolved" {
			index.Resources[i].SHA256, _ = fileHash(archive)
			st, _ := os.Stat(archive)
			index.Resources[i].Detail.Size = steamInt(st.Size())
		}
	}
	b, err = json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(res, "workshop-index.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	if err := deployPlatform(root, res, game, "evolved", ""); err != nil {
		t.Fatal(err)
	}
	m, err := checkedInstallManifest(filepath.Join(root, platformBackupName), game)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range additions {
		target := filepath.Join(game, filepath.FromSlash(rel))
		if !exists(target) || !manifestHasFile(m, target) {
			t.Fatalf("package file omitted from deployment/journal: %s", rel)
		}
		actual, _ := fileHash(target)
		found := false
		for _, f := range m.Files {
			if f.Target == target && f.SourceSHA256 == actual {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing verified hash: %s", rel)
		}
	}
	for rel, expected := range map[string]string{"Evolved/config.txt": "Other=keep", "Evolved/config2.txt": "NewDefault=keep"} {
		b, err := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
		if err != nil || !strings.Contains(string(b), expected) || !strings.Contains(string(b), "NoHmdMinimapEnabled=true") {
			t.Fatalf("configuration merge failed: %s %v", rel, err)
		}
	}
	if err := os.Remove(filepath.Join(game, filepath.FromSlash(additions[1]))); err != nil {
		t.Fatal(err)
	}
	text, _, err := detectedPlatformText(game, res, filepath.Join(root, platformBackupName), "evolved")
	if err != nil || !strings.Contains(text, "部署不完整") {
		t.Fatalf("missing split module not detected: %s %v", text, err)
	}
	if err := restorePlatform(root, game, true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range additions {
		if rel == additions[0] {
			b, _ := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
			if string(b) != "prior plugin" {
				t.Fatal("overwritten plugin not restored")
			}
			continue
		}
		if exists(filepath.Join(game, filepath.FromSlash(rel))) {
			t.Fatalf("rollback retained %s", rel)
		}
	}
	t.Log("all package plugins and nested assets deployed, journaled, hashed and rolled back")
}

func TestEvoFutureRuntimeIsNotSilentlyDropped(t *testing.T) {
	root := t.TempDir()
	testFiles(t, root, map[string][]byte{"bin/neko/plugins/Future.dll": testDLL(0x14c), "NewRuntime/required.dat": []byte("runtime")})
	if m, err := platformMappings("evolved", root, []string{"bin/neko/plugins/Future.dll", "NewRuntime/required.dat"}); err != nil || len(m) != 2 {
		t.Fatalf("future runtime omitted: %v", err)
	}
	if _, err := platformMappings("evolved", root, []string{"bin/neko/plugins/Future.dll", "../outside.dll"}); err == nil {
		t.Fatal("invalid path accepted")
	}
}

func TestEvoRenamedPluginsAndArchitecture(t *testing.T) {
	for _, machine := range []uint16{0x14c, 0x8664} {
		root := t.TempDir()
		payload := map[string][]byte{"bin/neko/plugins/new-layout/FutureCore.dll": testDLL(machine), "bin/neko/plugins/new-layout/data.json": []byte("{}")}
		testFiles(t, root, payload)
		files := []string{"bin/neko/plugins/new-layout/FutureCore.dll", "bin/neko/plugins/new-layout/data.json"}
		mappings, err := platformMappings("evolved", root, files)
		if machine == 0x8664 {
			if err == nil {
				t.Fatal("x64 plugin accepted")
			}
			continue
		}
		if err != nil || len(mappings) != len(files) {
			t.Fatalf("renamed package rejected: %v", err)
		}
	}
}

func TestEvoActualPackageInventory(t *testing.T) {
	archive := os.Getenv("L4NFIX_EVO_AUDIT_ARCHIVE")
	if archive == "" {
		t.Skip("set L4NFIX_EVO_AUDIT_ARCHIVE to audit a local package")
	}
	root := t.TempDir()
	info, err := preparePackage(archive, root, "evolved")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(info.TemporaryDir)
	plugins := 0
	for _, f := range info.Files {
		h, err := fileHash(filepath.Join(info.NormalizedDir, f.Relative))
		if err != nil || h != f.SHA256 {
			t.Fatalf("payload hash mismatch: %s", f.Relative)
		}
		if filepath.Ext(f.Relative) == ".dll" {
			plugins++
		}
	}
	if plugins != 9 {
		t.Fatalf("expected nine plugins from audit package, got %d", plugins)
	}
	t.Logf("actual resource: %d files, %d plugin DLLs; all normalized payload hashes verified", len(info.Files), plugins)
}
