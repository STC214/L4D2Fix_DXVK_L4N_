//go:build windows

package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLaunchFilenameBothWords(t *testing.T) {
	for name, want := range map[string]bool{"启动项指令【四】.txt": true, "指令_启动.TXT": true, "启动.txt": false, "指令.txt": false, "启动指令.md": false} {
		if got := isLaunchInstructionFile(name); got != want {
			t.Fatalf("%s=%v", name, got)
		}
	}
}

func TestExtractLaunchInstructions(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"pure", "-novid -console", "-novid -console"},
		{"label", "使用说明\r\n启动项指令： -novid   -vulkan\r\n请复制上面的指令", "-novid -vulkan"},
		{"multiline", "```text\n-novid\n+mat_queue_mode 2\n```", "-novid +mat_queue_mode 2"},
		{"quoted", "启动指令: -language \"简体 中文\" +exec \"custom $1.cfg\" // 说明", "-language \"简体 中文\" +exec \"custom $1.cfg\""},
		{"comment", "# 说明\n-novid -console # 注释", "-novid -console"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := extractLaunchOptions(tc.input)
			if e != nil || got != tc.want {
				t.Fatalf("%q %v", got, e)
			}
		})
	}
	for _, bad := range []string{"没有指令", "-novid\n\n-vulkan", `-novid +exec "unterminated`} {
		if _, e := extractLaunchOptions(bad); e == nil {
			t.Fatal("ambiguous/invalid text accepted", bad)
		}
	}
}

func TestLaunchTextEncoding(t *testing.T) {
	want := "说明\n启动指令：-novid -console"
	for _, kind := range []string{"utf8", "bom", "utf16le", "utf16be", "gbk"} {
		t.Run(kind, func(t *testing.T) {
			var data []byte
			switch kind {
			case "utf8":
				data = []byte(want)
			case "bom":
				data = append([]byte{0xef, 0xbb, 0xbf}, []byte(want)...)
			case "utf16le", "utf16be":
				units := utf16.Encode([]rune(want))
				data = make([]byte, 2+len(units)*2)
				if kind == "utf16le" {
					data[0] = 0xff
					data[1] = 0xfe
					for i, u := range units {
						binary.LittleEndian.PutUint16(data[2+i*2:], u)
					}
				} else {
					data[0] = 0xfe
					data[1] = 0xff
					for i, u := range units {
						binary.BigEndian.PutUint16(data[2+i*2:], u)
					}
				}
			case "gbk":
				data = append([]byte{0xcb, 0xb5, 0xc3, 0xf7, '\n'}, []byte("-novid -console")...)
			}
			text, e := decodeLaunchText(data)
			if e != nil {
				t.Fatal(e)
			}
			options, e := extractLaunchOptions(text)
			if e != nil || options != "-novid -console" {
				t.Fatal(text, e)
			}
		})
	}
}

func TestL4NInstructionDirectoryAndZip(t *testing.T) {
	for _, kind := range []string{"directory", "zip"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			res := filepath.Join(r, "resources")
			f := l4nFixture()
			f["outside/启动项指令【四】.TXT"] = []byte("说明\n启动指令：-novid -console\n请复制")
			f["wrapper/L4N/启动指令.txt"] = []byte("-novid -console")
			var source string
			if kind == "zip" {
				source = filepath.Join(res, l4nVersionsDirName, "release.zip")
				testZip(t, source, f)
			} else {
				source = filepath.Join(res, l4nVersionsDirName, "release")
				testFiles(t, source, f)
			}
			testFiles(t, res, map[string][]byte{"l4n/公共启动指令.txt": []byte("-vulkan"), "启动项指令.txt": []byte("-heapsize 100")})
			prepared, e := preparePackage(source, res, "l4n")
			if e != nil {
				t.Fatal(e)
			}
			choice, e := resolveL4nLaunchOptions(prepared, res)
			if e != nil || choice.Options != "-novid -console" || choice.Scope != "selected-l4n" || len(choice.Documents) != 2 {
				t.Fatalf("choice=%+v err=%v", choice, e)
			}
			for _, m := range prepared.Files {
				if isLaunchInstructionFile(m.Relative) {
					t.Fatal("instruction TXT should not be copied into game", m)
				}
			}
			data, e := os.ReadFile(filepath.Join(prepared.TemporaryDir, "prepared.json"))
			if e != nil {
				t.Fatal(e)
			}
			var recorded packageSource
			if e = json.Unmarshal(data, &recorded); e != nil || recorded.LaunchInstructions == nil {
				t.Fatal("source metadata missing", e)
			}
			for _, doc := range choice.Documents {
				if len(doc.SHA256) != 64 {
					t.Fatal("TXT checksum missing")
				}
			}
			t.Logf("%s: outer/nested TXT detected, selected source wins, instruction files excluded from game payload", kind)
		})
	}
}

func TestLaunchSharedAndFallback(t *testing.T) {
	r := t.TempDir()
	res := filepath.Join(r, "resources")
	if e := os.MkdirAll(res, 0755); e != nil {
		t.Fatal(e)
	}
	choice, e := resolveL4nLaunchOptions(packageSource{}, res)
	if e != nil || choice.Scope != "built-in-default" || choice.Options != defaultLaunchOptions {
		t.Fatal(choice, e)
	}
	testFiles(t, res, map[string][]byte{"启动项指令.txt": []byte("-novid")})
	choice, e = resolveL4nLaunchOptions(packageSource{}, res)
	if e != nil || choice.Options != "-novid" || choice.Scope != "resources-root" {
		t.Fatal(choice, e)
	}
	testFiles(t, res, map[string][]byte{"l4n/通用启动指令.TXT": []byte("-console"), "l4n/unselected/启动指令.txt": []byte("-vulkan")})
	choice, e = resolveL4nLaunchOptions(packageSource{}, res)
	if e != nil || choice.Options != "-console" || choice.Scope != "l4n-shared" {
		t.Fatal(choice, e)
	}
	t.Log("unselected version docs ignored; shared l4n TXT > legacy resource TXT > built-in default")
}

func TestLaunchInstructionConflicts(t *testing.T) {
	r := t.TempDir()
	f := l4nFixture()
	f["启动指令A.txt"] = []byte("-novid")
	f["启动指令B.txt"] = []byte("-console")
	p := filepath.Join(r, "bad.zip")
	testZip(t, p, f)
	if _, e := preparePackage(p, r, "l4n"); e == nil {
		t.Fatal("conflicting instructions accepted")
	}
	testFiles(t, filepath.Join(r, "shared"), map[string][]byte{"启动指令.txt": []byte("说明，没有可提取参数")})
	if _, e := readLaunchDocuments(filepath.Join(r, "shared"), false); e == nil {
		t.Fatal("empty instruction accepted")
	}
}

func TestSteamExtractedInstructionsAndRestore(t *testing.T) {
	r := t.TempDir()
	res := filepath.Join(r, "resources")
	f := l4nFixture()
	f["启动指令.txt"] = []byte(`启动指令：-novid +exec "custom $1.cfg"`)
	p := filepath.Join(res, l4nVersionsDirName, "release.zip")
	testZip(t, p, f)
	prepared, e := preparePackage(p, res, "l4n")
	if e != nil {
		t.Fatal(e)
	}
	selection, e := resolveL4nLaunchOptions(prepared, res)
	if e != nil {
		t.Fatal(e)
	}
	steam := filepath.Join(r, "steam")
	config := filepath.Join(steam, "userdata", "1", "config", "localconfig.vdf")
	original := "\"apps\"\n{\n\"550\"\n{\n\t\"LaunchOptions\" \"-old +exec \\\"old.cfg\\\"\"\n}\n\"730\"\n{\n\t\"LaunchOptions\" \"-untouched\"\n}\n}\n"
	original = steamConfigFixture(original)
	testFiles(t, steam, map[string][]byte{"userdata/1/config/localconfig.vdf": []byte(original)})
	backup := filepath.Join(r, "backup")
	man := loadManifest(backup, filepath.Join(r, "game"))
	man.LaunchOptions = selection.Options
	man.LaunchInstruction = &selection
	man.PackageSources = []packageSource{prepared}
	if e = saveManifest(man, backup); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = setSteamLaunchOptionsForRoots(man, backup, selection.Options, []string{steam}); e != nil {
			t.Fatal(e)
		}
	}
	updated, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(string(updated), "custom $1.cfg") != 1 || strings.Contains(string(updated), "old.cfg") || !strings.Contains(string(updated), "-untouched") {
		t.Fatalf("Steam text mismatch: %s", updated)
	}
	recorded := loadManifest(backup, "")
	if recorded.LaunchInstruction == nil || recorded.LaunchInstruction.Options != selection.Options || len(recorded.SteamConfigs) != 1 {
		t.Fatal("manifest missing selection/backup")
	}
	t.Log("STEAM APPLY PASS: extracted quoted/$ arguments written to AppID 550 twice; neighboring game preserved; source TXT/hash recorded")
	if e = restoreFromBackup(backup); e != nil {
		t.Fatal(e)
	}
	restored, e := os.ReadFile(config)
	if e != nil || string(restored) != original {
		t.Fatal("Steam configuration rollback mismatch", e)
	}
	t.Log("STEAM ROLLBACK PASS: original localconfig.vdf restored byte-for-byte")
}
