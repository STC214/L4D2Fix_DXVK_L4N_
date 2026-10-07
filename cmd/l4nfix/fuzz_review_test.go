//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzSteamVDFEdit(f *testing.F) {
	for _, seed := range []string{`"apps" { "550" { "LaunchOptions" "old" } }`, `// } "apps"\n`, `"apps" {}`, `"apps" { "550" { "nested" { "LaunchOptions" "keep" } } }`, `"apps" { "550" "bad" }`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		options := `-steam +exec "some file.cfg"`
		edited, err := setAppLaunchOptionsInText(text, options)
		if err != nil {
			return
		}
		if _, err := parseVDF(edited); err != nil {
			t.Fatalf("invalid edit: %v", err)
		}
		again, err := setAppLaunchOptionsInText(edited, options)
		if err != nil || again != edited {
			t.Fatalf("not idempotent: %v", err)
		}
	})
}

func FuzzPackageRelativeBoundary(f *testing.F) {
	for _, seed := range []string{"x32/dxgi.dll", "../outside", "a/../b", "C:/file", "\\server\\share\\file", "a//b", "a/./b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		rel, err := packageRelative(name)
		if err != nil || rel == "" {
			return
		}
		root := `C:\fixture`
		p := filepath.Join(root, rel)
		got, err := filepath.Rel(root, p)
		if err != nil || filepath.IsAbs(got) || got == ".." || strings.HasPrefix(got, `..\`) {
			t.Fatalf("escaped path: %q -> %q", name, p)
		}
	})
}
