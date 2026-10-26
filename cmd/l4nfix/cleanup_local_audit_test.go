//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in audit of real, locally retained archives. CI uses the synthetic tests;
// no network download, game installation or Steam account is involved here.
func TestLocalCleanupPackageAudit(t *testing.T) {
	config := os.Getenv("L4N_CLEANUP_PACKAGE_AUDIT")
	if config == "" {
		t.Skip("local archive inventory not selected")
	}
	var sources []struct{ Kind, Source string }
	b, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &sources); e != nil {
		t.Fatal(e)
	}
	for _, source := range sources {
		t.Run(source.Kind+"/"+filepath.Base(source.Source), func(t *testing.T) {
			r := t.TempDir()
			input := filepath.Join(r, "input")
			os.MkdirAll(input, 0755)
			archivePath := source.Source
			if strings.HasSuffix(strings.ToLower(archivePath), ".vpk") {
				archivePath = filepath.Join(r, "workshop.zip")
				if e := copyFile(source.Source, archivePath); e != nil {
					t.Fatal(e)
				}
			}
			if e := extractPackage(archivePath, input); e != nil {
				t.Fatal(e)
			}
			rows, e := cleanupPackageSignatures(source.Kind, input)
			if e != nil {
				t.Fatal(e)
			}
			inventory, e := packageInventory(input)
			if e != nil {
				t.Fatal(e)
			}
			byHash := map[string]string{}
			for _, f := range inventory {
				p := filepath.Join(input, f)
				h, e := fileHash(p)
				if e != nil {
					t.Fatal(e)
				}
				byHash[h] = p
			}
			scope := "platform"
			if source.Kind == "l4n" || source.Kind == "dxvk" {
				scope = "base"
			}
			game := filepath.Join(r, "game")
			testFiles(t, game, map[string][]byte{"left4dead2/cfg/user.cfg": []byte("unrelated user config"), "left4dead2/addons/other-user-addon.vpk": []byte("unrelated addon")})
			selected := map[string]string{}
			// Deliberately install only a subset, not a complete validated layout.
			for i, row := range rows {
				if i%3 != 0 || !cleanupScopePath(scope, row.Rel) {
					continue
				}
				selected[row.Rel] = row.SHA256
				if e := copyFile(byHash[row.SHA256], filepath.Join(game, filepath.FromSlash(row.Rel))); e != nil {
					t.Fatal(e)
				}
			}
			p, e := buildManualCleanupPlan(game, scope, rows)
			if e != nil {
				t.Fatal(e)
			}
			archive, e := applyManualCleanup(filepath.Join(r, "resources"), p)
			if e != nil {
				t.Fatal(e)
			}
			for rel, hash := range selected {
				if exists(filepath.Join(game, filepath.FromSlash(rel))) {
					t.Fatal("known partial file remains", rel)
				}
				h, e := fileHash(filepath.Join(archive, "files", filepath.FromSlash(rel)))
				if e != nil || h != hash {
					t.Fatal("quarantine mismatch", rel, e)
				}
			}
			if !exists(filepath.Join(game, "left4dead2/cfg/user.cfg")) || !exists(filepath.Join(game, "left4dead2/addons/other-user-addon.vpk")) {
				t.Fatal("unrelated data removed")
			}
			t.Logf("%d signatures, %d partial-install files quarantined and removed", len(rows), len(selected))
		})
	}
}
