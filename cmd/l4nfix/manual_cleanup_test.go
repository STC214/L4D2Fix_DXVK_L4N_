//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func cleanupTestSignature(kind, rel, content string) cleanupSignature {
	return cleanupSignature{kind, rel, cleanupHash([]byte(content))}
}
func cleanupPlanPaths(p *manualCleanupPlan) map[string]bool {
	m := map[string]bool{}
	for _, f := range p.Files {
		m[f.Rel] = true
	}
	return m
}

func TestManualCleanupPartialInstallAndIndependentScopes(t *testing.T) {
	for _, scope := range []string{"base", "platform"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			game := filepath.Join(root, "game")
			res := filepath.Join(root, "resources")
			files := map[string][]byte{
				"left4dead2.exe": []byte("original game"), "bin/engine.dll": []byte("original engine"),
				"bin/left4neko.dll": []byte("partial l4n core"), "dxgi.dll": []byte("known dxvk"), "left4dead2/neko/config.vdf": []byte("user l4n settings"),
				"skeeto.dll": []byte("partial skeeto core"), "bin/wininet.dll": []byte("known skeeto proxy"), "skeeto_update/READY": []byte("pending"),
				"Evolved/user-new-version.cfg": []byte("user data"), "bin/neko/plugins/L4D2Evolved.dll": []byte("partial evo core"),
				"bin/neko/plugins/CSessionDefaults.dll": []byte("other plugin"), "left4dead2/addons/other.vpk": []byte("other addon"),
				"left4dead2/sound/vrmod/user.wav": []byte("other sound"), "left4dead2/shaders/fxc/outline_ps30.vcs": []byte("unidentified shader"),
				"left4dead2/cfg/autoexec.cfg": []byte("echo keep\r\n" + skeetoNavBegin + "\r\nskeeto_nav 1\r\nskeeto_nav_reload\r\n"),
			}
			testFiles(t, game, files)
			rows := []cleanupSignature{cleanupTestSignature("dxvk", "dxgi.dll", "known dxvk"), cleanupTestSignature("skeeto", "bin/wininet.dll", "known skeeto proxy"), cleanupTestSignature("l4n", "left4dead2/shaders/fxc/outline_ps30.vcs", "known different shader")}
			p, e := buildManualCleanupPlan(game, scope, rows)
			if e != nil {
				t.Fatal(e)
			}
			if p.Scope == "base" && len(p.Kept) != 1 {
				t.Fatal("unidentified shared shader not reported", p.Kept)
			}
			archive, e := applyManualCleanup(res, p)
			if e != nil {
				t.Fatal(e)
			}
			if p.Status != "complete" || archive == "" {
				t.Fatal(p.Status, archive)
			}
			paths := cleanupPlanPaths(p)
			for rel, before := range files {
				got, e := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
				if paths[strings.ToLower(rel)] {
					if rel == "left4dead2/cfg/autoexec.cfg" {
						if string(got) != "echo keep\r\n" {
							t.Fatalf("cfg lost: %q", got)
						}
					} else if !os.IsNotExist(e) {
						t.Fatal("file still present", rel, e)
					}
					backup, e := os.ReadFile(filepath.Join(archive, "files", filepath.FromSlash(strings.ToLower(rel))))
					if e != nil || string(backup) != string(before) {
						t.Fatal("backup mismatch", rel, e)
					}
				} else if e != nil || string(got) != string(before) {
					t.Fatal("other scope/user file changed", rel, e)
				}
			}
			if scope == "base" && (paths["skeeto.dll"] || paths["bin/wininet.dll"] || paths["bin/neko/plugins/l4d2evolved.dll"]) {
				t.Fatal("base crossed scope")
			}
			if scope == "platform" && (paths["bin/left4neko.dll"] || paths["dxgi.dll"]) {
				t.Fatal("platform crossed scope")
			}
			again, e := buildManualCleanupPlan(game, scope, rows)
			if e != nil || len(again.Files) != 0 {
				t.Fatal("not idempotent", e, again)
			}
		})
	}
}

func TestManualCleanupHandlesSingleFileWithoutFullInstallation(t *testing.T) {
	for _, tc := range []struct{ scope, kind, path string }{
		{"base", "l4n", "bin/left4neko.dll"}, {"base", "dxvk", "bin/d3d9.dll"},
		{"platform", "skeeto", "bin/wininet.dll"}, {"platform", "evolved", "bin/winmm.dll"},
		{"platform", "evolved", "left4dead2/addons/killicon.vpk"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := t.TempDir()
			game := filepath.Join(r, "game")
			testFiles(t, game, map[string][]byte{tc.path: []byte("partial")})
			p, e := buildManualCleanupPlan(game, tc.scope, []cleanupSignature{cleanupTestSignature(tc.kind, tc.path, "partial")})
			if e != nil || len(p.Files) != 1 {
				t.Fatal(e, p)
			}
			if _, e = applyManualCleanup(filepath.Join(r, "resources"), p); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestManualCleanupKeepsUnknownLoadersAndNativeNecola(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	testFiles(t, game, map[string][]byte{"bin/wininet.dll": []byte("other proxy"), "winmm.dll": []byte("other loader"), "necola.dll": []byte("native necola"), "necola_orig.dll": []byte("native original"), "current.hud": []byte("user hud")})
	rows := []cleanupSignature{cleanupTestSignature("skeeto", "bin/wininet.dll", "Skeeto proxy"), cleanupTestSignature("evolved", "winmm.dll", "Evo loader"), cleanupTestSignature("skeeto", "necola.dll", "Skeeto alias")}
	p, e := buildManualCleanupPlan(game, "platform", rows)
	if e != nil || len(p.Files) != 0 || len(p.Kept) != 3 {
		t.Fatal(e, p)
	}
}

func TestManualAutoexecPreservesMixedCommandsAndComments(t *testing.T) {
	in := []byte("// skeeto_nav 1\r\necho keep\r\nskeeto_nav 1; echo keep\r\nbind x \"skeeto_nav 1\"\r\nskeeto_nav 1 // standalone\r\nskeeto_nav_reload\r\n" + skeetoNavEnd + "\r\n")
	want := "// skeeto_nav 1\r\necho keep\r\nskeeto_nav 1; echo keep\r\nbind x \"skeeto_nav 1\"\r\n"
	if got := string(cleanupManualAutoexec(in)); got != want {
		t.Fatalf("%q", got)
	}
}

func TestManualCleanupPreflightPreventsPartialDeletion(t *testing.T) {
	for _, tamper := range []string{"changed", "locked", "rewrite", "traversal"} {
		t.Run(tamper, func(t *testing.T) {
			r := t.TempDir()
			game := filepath.Join(r, "game")
			res := filepath.Join(r, "resources")
			testFiles(t, game, map[string][]byte{"skeeto.dll": []byte("core"), "Skeeto/user.cfg": []byte("config"), "left4dead2/cfg/autoexec.cfg": []byte("skeeto_nav 1\n")})
			p, e := buildManualCleanupPlan(game, "platform", nil)
			if e != nil {
				t.Fatal(e)
			}
			switch tamper {
			case "changed":
				os.WriteFile(filepath.Join(game, "skeeto.dll"), []byte("changed"), 0644)
			case "locked":
				name, _ := syscall.UTF16PtrFromString(filepath.Join(game, "skeeto.dll"))
				h, e := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
				if e != nil {
					t.Fatal(e)
				}
				defer syscall.CloseHandle(h)
			case "rewrite":
				for i := range p.Files {
					if p.Files[i].Rewrite {
						p.Files[i].After = []byte("tamper")
					}
				}
			case "traversal":
				p.Files[0].Rel = "../outside"
			}
			if _, e := applyManualCleanup(res, p); e == nil {
				t.Fatal("unsafe snapshot accepted")
			}
			if !exists(filepath.Join(game, "skeeto.dll")) || !exists(filepath.Join(game, "Skeeto/user.cfg")) {
				t.Fatal("preflight removed file")
			}
		})
	}
}

func TestManualCleanupRejectsLinkedPrivateTree(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	outside := filepath.Join(r, "outside")
	testFiles(t, outside, map[string][]byte{"private.txt": []byte("keep")})
	os.MkdirAll(game, 0755)
	if e := os.Symlink(outside, filepath.Join(game, "Skeeto")); e != nil {
		t.Skip(e)
	}
	if _, e := buildManualCleanupPlan(game, "platform", nil); e == nil {
		t.Fatal("link accepted")
	}
	b, _ := os.ReadFile(filepath.Join(outside, "private.txt"))
	if string(b) != "keep" {
		t.Fatal("outside changed")
	}
}

func TestManualCleanupRunnableRollback(t *testing.T) {
	if e := checkCleanupGameStopped(); e != nil {
		t.Skip("real game running; no rollback process test")
	}
	r := t.TempDir()
	if base := os.Getenv("L4N_CLEANUP_REVIEW_DIR"); base != "" {
		var e error
		r, e = os.MkdirTemp(base, "rollback-fixture-")
		if e != nil {
			t.Fatal(e)
		}
	}
	game := filepath.Join(r, "game")
	res := filepath.Join(r, "resources")
	original := map[string][]byte{"skeeto.dll": []byte("core"), "Skeeto/user.cfg": []byte("my cfg"), "left4dead2/cfg/autoexec.cfg": []byte("echo keep\nskeeto_nav 1\n")}
	testFiles(t, game, original)
	p, e := buildManualCleanupPlan(game, "platform", nil)
	if e != nil {
		t.Fatal(e)
	}
	backup, e := applyManualCleanup(res, p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var journal manualCleanupPlan
	if e = json.Unmarshal(b, &journal); e != nil || journal.Status != "complete" {
		t.Fatal(e, string(b))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(backup, "rollback.ps1"))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, e := cmd.CombinedOutput()
	if e != nil || !strings.Contains(string(out), "Rollback PASS") {
		t.Fatalf("rollback exit=%v: %s", e, out)
	}
	for rel, before := range original {
		got, e := os.ReadFile(filepath.Join(game, filepath.FromSlash(rel)))
		if e != nil || string(got) != string(before) {
			t.Fatal("rollback mismatch", rel, e)
		}
	}
	t.Log("quarantine + SHA256 + journal + runnable PowerShell rollback PASS (fixture only)")
	if base := os.Getenv("L4N_CLEANUP_REVIEW_DIR"); base != "" {
		record := map[string]any{"fixture": r, "backup": backup, "rollback": filepath.Join(backup, "rollback.ps1"), "command": cmd.Args, "output": string(out), "exit": 0, "cleanupStatus": journal.Status, "restoredFiles": len(original)}
		b, e := json.MarshalIndent(record, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(base, "rollback-verification.json"), b, 0644); e != nil {
			t.Fatal(e)
		}
	}
}

func TestCleanupCatalogLearnsNewVersionFiles(t *testing.T) {
	for _, kind := range []string{"l4n", "dxvk", "skeeto", "evolved"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			res := filepath.Join(r, "resources")
			game := filepath.Join(r, "game")
			var files map[string][]byte
			scope := "platform"
			newFile := "future_helper_999.dll"
			if kind == "dxvk" {
				files = map[string][]byte{"dxvk-999/x32/dxgi.dll": testDLL(0x14c), "dxvk-999/x32/d3d9.dll": testDLL(0x14c), "dxvk-999/x32/d3d12.dll": testDLL(0x14c)}
				newFile = "d3d12.dll"
				scope = "base"
			} else {
				files = workshopFixture(kind)
				if kind == "l4n" {
					scope = "base"
				}
				files[newFile] = []byte("future shared helper content")
			}
			archive := filepath.Join(res, kind, "version-999.zip")
			testZip(t, archive, files)
			prepared, e := preparePackage(archive, res, kind)
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(prepared.TemporaryDir)
			rows, e := readCleanupSignatures(res)
			if e != nil {
				t.Fatal(e)
			}
			data := files[newFile]
			if kind == "dxvk" {
				data = testDLL(0x14c)
			}
			testFiles(t, game, map[string][]byte{newFile: data})
			p, e := buildManualCleanupPlan(game, scope, rows)
			if e != nil || !cleanupPlanPaths(p)[newFile] {
				t.Fatal("new version file not learned", e, p)
			}
		})
	}
}

func TestCleanupCatalogRejectsTraversalAndSupportsHistoricalSeed(t *testing.T) {
	r := t.TempDir()
	rows, e := readCleanupSignatures(r)
	if e != nil || len(rows) < 300 {
		t.Fatal(e, len(rows))
	}
	for _, bad := range []cleanupSignature{{"skeeto", "../outside", strings.Repeat("a", 64)}, {"unknown", "a", strings.Repeat("a", 64)}, {"l4n", "valid", "invalid"}} {
		if _, e := mergeCleanupSignatures([]cleanupSignature{bad}); e == nil {
			t.Fatal("bad catalog accepted", bad)
		}
	}
}

func TestCleanupAutoexecPreservesMultilineAliasAndBOM(t *testing.T) {
	in := "\xef\xbb\xbfalias custom \"\nskeeto_nav 1\nskeeto_nav_reload\n\"\n// quote in comment: \"\nskeeto_nav 1 // remove this standalone line\necho keep\n"
	want := "\xef\xbb\xbfalias custom \"\nskeeto_nav 1\nskeeto_nav_reload\n\"\n// quote in comment: \"\necho keep\n"
	if got := string(cleanupManualAutoexec([]byte(in))); got != want {
		t.Fatalf("alias/BOM changed: %q", got)
	}
	if got := string(cleanupManualAutoexec([]byte("\xef\xbb\xbfsky 1\nskeeto_nav_reload"))); got != "\xef\xbb\xbfsky 1\n" {
		t.Fatalf("BOM/last line: %q", got)
	}
}

func TestCleanupCatalogConcurrentPreparationsRetainAllVersions(t *testing.T) {
	root := t.TempDir()
	res := filepath.Join(root, "resources")
	if e := os.MkdirAll(res, 0755); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("future-%d.dll", i)
		input := filepath.Join(root, fmt.Sprintf("source-%d", i))
		testFiles(t, input, map[string][]byte{"skeeto.dll": []byte("anchor"), name: []byte(name)})
		wg.Add(1)
		go func() { defer wg.Done(); errors <- rememberCleanupPackage(res, "skeeto", input) }()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	rows, e := readCleanupSignatures(res)
	if e != nil {
		t.Fatal(e)
	}
	found := map[string]bool{}
	for _, r := range rows {
		found[r.Rel] = true
	}
	for i := 0; i < 8; i++ {
		if !found[fmt.Sprintf("future-%d.dll", i)] {
			t.Fatal("lost concurrent version", i)
		}
	}
}
