//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledVersionStates(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	res := filepath.Join(root, "resources")
	backup := filepath.Join(root, "backup")
	os.MkdirAll(game, 0755)
	os.MkdirAll(res, 0755)
	text, e := installedVersionText(game, res, backup)
	if e != nil || !strings.Contains(text, "DXVK 未使用") || !strings.Contains(text, "L4N 未使用") {
		t.Fatal(text, e)
	}
	l4nDir := filepath.Join(res, "l4n", "L4N_v2.50.3")
	dxvkDir := filepath.Join(res, "dxvk", "dxvk-2.7.1")
	testFiles(t, l4nDir, map[string][]byte{"bin/left4neko.dll": testDLL(0x14c), configRelativePath: []byte("default")})
	testFiles(t, dxvkDir, dxvkFixture("x32/"))
	testFiles(t, game, map[string][]byte{"bin/left4neko.dll": testDLL(0x14c), "dxgi.dll": testDLL(0x14c), "dxvk_d3d9.dll": testDLL(0x14c), "bin/dxvk_d3d9.dll": testDLL(0x14c), configRelativePath: []byte("user edited")})
	text, e = installedVersionText(game, res, backup)
	if e != nil || !strings.Contains(text, "DXVK 2.7.1") || !strings.Contains(text, "L4N 2.50.3") {
		t.Fatal(text, e)
	}
	os.WriteFile(filepath.Join(game, "bin/left4neko.dll"), []byte("external unknown version"), 0600)
	os.Remove(filepath.Join(game, "bin/dxvk_d3d9.dll"))
	text, e = installedVersionText(game, res, backup)
	if e != nil || strings.Count(text, "版本未知") != 2 {
		t.Fatal(text, e)
	}
	os.Remove(filepath.Join(game, "bin/left4neko.dll"))
	os.Remove(filepath.Join(game, "dxgi.dll"))
	os.Remove(filepath.Join(game, "dxvk_d3d9.dll"))
	text, e = installedVersionText(game, res, backup)
	if e != nil || strings.Count(text, "未使用") != 2 {
		t.Fatal(text, e)
	}
}

func TestInstalledManifestMustMatchActualDLLs(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "game")
	res := filepath.Join(root, "resources")
	backup := filepath.Join(root, "backup")
	testFiles(t, game, map[string][]byte{"bin/left4neko.dll": []byte("known l4n")})
	h, _ := fileHash(filepath.Join(game, "bin/left4neko.dll"))
	m := manifest{GameRoot: game, PackageSources: []packageSource{{Kind: "l4n", Source: "L4N_v2.47.2.zip", Files: []packageMapping{{Relative: "bin/left4neko.dll", SHA256: h}}}}}
	os.MkdirAll(backup, 0755)
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(backup, "manifest.json"), b, 0600)
	text, e := installedVersionText(game, res, backup)
	if e != nil || !strings.Contains(text, "L4N 2.47.2") {
		t.Fatal(text, e)
	}
	os.WriteFile(filepath.Join(game, "bin/left4neko.dll"), []byte("replaced outside tool"), 0600)
	text, e = installedVersionText(game, res, backup)
	if e != nil || strings.Contains(text, "2.47.2") || !strings.Contains(text, "版本未知") {
		t.Fatal("stale manifest trusted", text, e)
	}
}

func TestVersionIdentityZIPAndAmbiguity(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "L4N_v2.41.1.zip")
	b := filepath.Join(root, "L4N_v2.47.2.zip")
	payload := l4nFixture()
	testZip(t, a, payload)
	testZip(t, b, payload)
	id, e := resourceIdentityFor(a, "l4n")
	if e != nil {
		t.Fatal(e)
	}
	var hash string
	for _, h := range id.Hashes {
		hash = h
	}
	installed := map[string]string{"bin/left4neko.dll": hash}
	text := detectedVersion("l4n", installed, nil, []dxvkOption{{Dir: a}, {Dir: b}})
	if !strings.Contains(text, "2.41.1 / 2.47.2") || !strings.Contains(text, "多包匹配") {
		t.Fatal(text)
	}
	p := filepath.Join(root, "dxvk-2.4.zip")
	testZip(t, p, dxvkFixture("wrapper/x32/"))
	id, e = resourceIdentityFor(p, "dxvk")
	if e != nil {
		t.Fatal(e)
	}
	installed = map[string]string{"dxgi.dll": id.Hashes["wrapper/x32/dxgi.dll"], "dxvk_d3d9.dll": id.Hashes["wrapper/x32/d3d9.dll"], "bin/dxvk_d3d9.dll": id.Hashes["wrapper/x32/d3d9.dll"]}
	if !identityMatches("dxvk", id.Hashes, installed) {
		t.Fatal("ZIP wrapper and d3d9 alias not matched")
	}
}

func TestLocalInstalledVersionReadOnly(t *testing.T) {
	game, res := os.Getenv("L4N_VERSION_GAME"), os.Getenv("L4N_VERSION_RESOURCES")
	if game == "" || res == "" {
		t.Skip("read-only local check environment not set")
	}
	before, e := fileHash(filepath.Join(game, filepath.FromSlash(configRelativePath)))
	if e != nil {
		t.Fatal(e)
	}
	text, e := installedVersionText(game, res, filepath.Join(filepath.Dir(res), ".l4n_auto_backup"))
	if e != nil {
		t.Fatal(e)
	}
	after, _ := fileHash(filepath.Join(game, filepath.FromSlash(configRelativePath)))
	if before != after {
		t.Fatal("game config changed")
	}
	t.Log(text)
}
