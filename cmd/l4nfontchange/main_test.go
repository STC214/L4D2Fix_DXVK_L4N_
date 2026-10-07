//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFontBlockIgnoresQuotedBracesAndComments(t *testing.T) {
	text := "\"font\"\n{\n\"Tahoma\" \"Font } { Face\" // }\n// comment }\n}\n\"other\" \"value\"\n"
	lines := strings.SplitAfter(text, "\n")
	start, end, err := findFontBlockLines(lines)
	if err != nil || start != 0 || end != 5 {
		t.Fatalf("wrong block: %d %d %v", start, end, err)
	}
}

func TestAtomicFontCopySamePath(t *testing.T) {
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

func TestFontNameEscapingAndRepeatedApply(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.vdf")
	original := "\"font\"\n{\n\"Tahoma\" \"Tahoma\"\n}\n"
	os.WriteFile(p, []byte(original), 0644)
	for i := 0; i < 2; i++ {
		if err := updateConfigTahomaFont(p, `Face "Quoted"`); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), `"Tahoma" "Face \"Quoted\""`) != 1 {
		t.Fatalf("bad escaping: %s", b)
	}
	backup, _ := os.ReadFile(p + ".l4nfontchange.bak")
	if string(backup) != original {
		t.Fatal("original backup changed")
	}
}
