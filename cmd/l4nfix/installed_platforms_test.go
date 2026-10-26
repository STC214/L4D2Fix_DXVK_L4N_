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

func TestInstalledPlatformsAbsentPartialAndCoexist(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	os.MkdirAll(game, 0755)
	backup := filepath.Join(r, platformBackupName)
	text, e := installedPlatformText(game, res, backup)
	if e != nil || !strings.Contains(text, "EVO：未使用") || !strings.Contains(text, "Skeeto：未使用") {
		t.Fatal(text, e)
	}
	testFiles(t, game, map[string][]byte{"bin/neko/plugins/L4D2Evolved.dll": []byte("unknown core")})
	text, e = installedPlatformText(game, res, backup)
	if e != nil || !strings.Contains(text, "部署不完整") || !strings.Contains(text, "版本未知") {
		t.Fatal(text, e)
	}
	testFiles(t, game, map[string][]byte{"skeeto.dll": []byte("unknown skeeto")})
	text, e = installedPlatformText(game, res, backup)
	if e != nil || !strings.Contains(text, "两个平台核心共存") {
		t.Fatal(text, e)
	}
	os.Remove(filepath.Join(game, "skeeto.dll"))
	testFiles(t, game, map[string][]byte{"Skeeto/user.cfg": []byte("settings")})
	text, e = installedPlatformText(game, res, backup)
	if e != nil || !strings.Contains(text, "Skeeto：仅残留") {
		t.Fatal(text, e)
	}
}

func TestInstalledPlatformsUnreadableOneDoesNotHideOther(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	files := workshopFixture("skeeto")
	testZip(t, filepath.Join(res, "skeeto", "Skeeto-99.2.3.zip"), files)
	core := map[string][]byte{}
	for rel, b := range files {
		if identityPath("skeeto", rel) {
			core[rel] = b
		}
	}
	core["bin/neko/plugins/L4D2Evolved.dll"] = []byte("locked evo")
	testFiles(t, game, core)
	name, _ := syscall.UTF16PtrFromString(filepath.Join(game, "bin/neko/plugins/L4D2Evolved.dll"))
	h, e := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.CloseHandle(h)
	text, e := installedPlatformText(game, res, filepath.Join(r, platformBackupName))
	if e == nil || !strings.Contains(text, "EVO：检测失败") || !strings.Contains(text, "Skeeto：99.2.3") {
		t.Fatal(text, e)
	}
}

func TestInstalledPlatformManifestRequiresActualCoreHashes(t *testing.T) {
	for _, kind := range []string{"evolved", "skeeto"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			game := filepath.Join(r, "game")
			backup := filepath.Join(r, platformBackupName)
			res := filepath.Join(r, "resources")
			files := map[string][]byte{"skeeto.dll": []byte("core"), "bin/wininet.dll": []byte("proxy")}
			if kind == "evolved" {
				files = map[string][]byte{"bin/neko/plugins/L4D2Evolved.dll": []byte("evo"), "bin/neko/plugins/L4d2evomodel.dll": []byte("model")}
			}
			testFiles(t, game, files)
			m := manifest{GameRoot: game, Platform: kind, PlatformVersion: "9.8.7"}
			var changed string
			for rel := range files {
				h, _ := fileHash(filepath.Join(game, filepath.FromSlash(rel)))
				m.Files = append(m.Files, fileEntry{Rel: rel, SourceSHA256: h})
				changed = rel
			}
			m.Files = append(m.Files, fileEntry{Rel: "one/README.txt", SourceSHA256: "first"}, fileEntry{Rel: "two/README.txt", SourceSHA256: "second"})
			b, _ := json.Marshal(m)
			testFiles(t, backup, map[string][]byte{"manifest.json": b})
			text, _, e := detectedPlatformText(game, res, backup, kind)
			if e != nil || text != "9.8.7" {
				t.Fatal(text, e)
			}
			os.WriteFile(filepath.Join(game, filepath.FromSlash(changed)), []byte("outside update"), 0644)
			text, _, e = detectedPlatformText(game, res, backup, kind)
			if e != nil || strings.Contains(text, "9.8.7") || !strings.Contains(text, "版本未知") {
				t.Fatal("stale manifest trusted", text, e)
			}
		})
	}
}

func TestInstalledPlatformCacheMatchAndReadOnly(t *testing.T) {
	for _, kind := range []string{"evolved", "skeeto"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			res := filepath.Join(r, "resources")
			game := filepath.Join(r, "game")
			files := workshopFixture(kind)
			archive := filepath.Join(res, kind, map[string]string{"skeeto": "Skeeto-99.2.3.zip", "evolved": "L4D2Evolved-99.2.3.zip"}[kind])
			testZip(t, archive, files)
			var core map[string][]byte = map[string][]byte{}
			for rel, b := range files {
				if identityPath(kind, rel) {
					core[rel] = b
				}
			}
			testFiles(t, game, core)
			before, _ := fileHash(archive)
			text, _, e := detectedPlatformText(game, res, filepath.Join(r, platformBackupName), kind)
			if e != nil || text != "99.2.3" {
				t.Fatal(text, e)
			}
			after, _ := fileHash(archive)
			if before != after || exists(filepath.Join(res, ".package_tmp")) || exists(filepath.Join(res, "cleanup-signatures.json")) {
				t.Fatal("read-only refresh wrote resources")
			}
		})
	}
}

func TestInstalledPlatformMixedLocationsDoNotClaimKnownVersion(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	files := workshopFixture("evolved")
	testZip(t, filepath.Join(res, "evolved", "Evolved-99.1.0.zip"), files)
	core := map[string][]byte{}
	for rel, b := range files {
		if identityPath("evolved", rel) {
			core[rel] = b
		}
	}
	core["L4D2Evolved.dll"] = []byte("different duplicate")
	testFiles(t, game, core)
	text, _, e := detectedPlatformText(game, res, filepath.Join(r, platformBackupName), "evolved")
	if e != nil || strings.Contains(text, "99.1.0") || !strings.Contains(text, "多位置残留") {
		t.Fatal(text, e)
	}
}
