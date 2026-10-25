//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditRejectsMismatchedNewFileRelativeTarget(t *testing.T) {
	root, _, game := platformTestEnvironment(t)
	target := filepath.Join(game, "bin/neko/plugins/CSessionDefaults.dll")
	m := manifest{GameRoot: game, Files: []fileEntry{{Target: target, Rel: "skeeto.dll", Existed: false}}}
	if e := validateManifestTargets(&m); e == nil {
		t.Fatal("MISMATCH ACCEPTED: a platform relative name can target another file")
	}
	// Exercise the real cleanup entry using a corrupted on-disk journal.
	m.Platform = "skeeto"
	b, _ := json.Marshal(m)
	dir := filepath.Join(root, platformBackupName)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0644)
	if e := restorePlatform(root, game, true); e == nil {
		t.Fatal("mismatched cleanup journal accepted")
	}
	b, _ = os.ReadFile(target)
	if string(b) != "existing C" {
		t.Fatal("unmanaged target changed")
	}
}
func TestAuditMalformedNavigationStopsBeforeAnyRemoval(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	if e := deployPlatform(root, res, game, "skeeto", ""); e != nil {
		t.Fatal(e)
	}
	cfg := filepath.Join(game, "left4dead2/cfg/autoexec.cfg")
	b, _ := os.ReadFile(cfg)
	b = []byte(strings.ReplaceAll(string(b), skeetoNavEnd, "// removed end marker"))
	os.WriteFile(cfg, b, 0644)
	core := filepath.Join(game, "skeeto.dll")
	before, _ := fileHash(core)
	if e := restorePlatform(root, game, true); e == nil {
		t.Fatal("malformed marker accepted")
	}
	after, e := fileHash(core)
	if e != nil || after != before {
		t.Fatal("PARTIAL CLEANUP: core removed before malformed config was detected")
	}
	if !platformInstalled(root) {
		t.Fatal("rollback journal lost")
	}
}
func TestAuditEvolvedWhitespaceDoesNotLeaveDuplicateKeys(t *testing.T) {
	b := evolvedNavigationConfig([]byte("NoHmdMinimapEnabled = false\r\n NoHmdWorldNavigationEnabled\t= false\r\nOther=keep\r\n"))
	for _, key := range []string{"NoHmdMinimapEnabled", "NoHmdWorldNavigationEnabled"} {
		if strings.Count(string(b), key) != 1 {
			t.Fatalf("duplicate active key retained: %s", b)
		}
	}
	if !strings.Contains(string(b), "Other=keep") {
		t.Fatal("unrelated setting changed")
	}
}
func TestAuditResourceDiscoveryHidesPendingArchives(t *testing.T) {
	res := t.TempDir()
	testZip(t, filepath.Join(res, "dxvk", ".pending-verify.zip"), dxvkFixture("x32/"))
	testZip(t, filepath.Join(res, "dxvk", "dxvk-stable.zip"), dxvkFixture("x32/"))
	opts := discoverDxvkOptions("", res)
	if len(opts) != 1 || opts[0].Name != "dxvk-stable.zip" {
		t.Fatalf("temporary archive exposed: %v", opts)
	}
}
func TestAuditInlineNavigationMarkerStopsBeforeRemoval(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	if e := deployPlatform(root, res, game, "skeeto", ""); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(game, "left4dead2/cfg/autoexec.cfg")
	b, _ := os.ReadFile(p)
	b = []byte(strings.Replace(string(b), skeetoNavBegin, "echo quoted "+skeetoNavBegin, 1))
	os.WriteFile(p, b, 0644)
	if e := restorePlatform(root, game, true); e == nil {
		t.Fatal("inline marker accepted")
	}
	if !exists(filepath.Join(game, "skeeto.dll")) {
		t.Fatal("core removed")
	}
}
func TestAuditRejectsResourceRootLinkBeforeExtraction(t *testing.T) {
	r := t.TempDir()
	outside := filepath.Join(r, "outside")
	os.MkdirAll(outside, 0755)
	res := filepath.Join(r, "resources")
	if e := os.Symlink(outside, res); e != nil {
		t.Skip("symlink creation unavailable", e)
	}
	src := filepath.Join(r, "dxvk.zip")
	testZip(t, src, dxvkFixture("x32/"))
	if _, e := preparePackage(src, res, "dxvk"); e == nil {
		t.Fatal("linked resource root accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("linked root modified before rejection")
	}
}
