//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainFontBlockIgnoresQuotedBracesAndComments(t *testing.T) {
	lines := strings.SplitAfter("\"font\"\n{\n\"Tahoma\" \"Font } { Face\" // }\n// comment }\n}\n\"other\" \"value\"\n", "\n")
	start, end, err := findFontBlockLines(lines)
	if err != nil || start != 0 || end != 5 {
		t.Fatalf("wrong block: %d %d %v", start, end, err)
	}
}

func TestMainAtomicCopySamePath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("preserved"), 0644)
	if err := copyFile(p, p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "preserved" {
		t.Fatal("same-path copy truncated")
	}
}

func TestSteamPartialFailureIsReported(t *testing.T) {
	r := t.TempDir()
	steam := filepath.Join(r, "steam")
	good := filepath.Join(steam, "userdata", "1", "config", "localconfig.vdf")
	bad := filepath.Join(steam, "userdata", "2", "config", "localconfig.vdf")
	os.MkdirAll(filepath.Dir(good), 0755)
	os.MkdirAll(filepath.Dir(bad), 0755)
	os.WriteFile(good, []byte(`"apps" { "550" { "LaunchOptions" "old" } }`), 0644)
	os.WriteFile(bad, []byte(`"apps" {`), 0644)
	m := &manifest{GameRoot: filepath.Join(r, "game")}
	if err := setSteamLaunchOptionsForRoots(m, filepath.Join(r, "backup"), "-steam", []string{steam}); err == nil {
		t.Fatal("partial failure hidden")
	}
	b, _ := os.ReadFile(bad)
	if string(b) != `"apps" {` {
		t.Fatal("damaged config overwritten")
	}
	if len(m.SteamConfigs) != 1 {
		t.Fatal("successful config not backed up")
	}
}
