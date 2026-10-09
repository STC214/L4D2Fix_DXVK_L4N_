//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestSteamVDFStructuralEdit(t *testing.T) {
	original := `// "550" { "LaunchOptions" "comment" }
"other" { "550" { "LaunchOptions" "unrelated" } }
"apps" {
 "730" { "LaunchOptions" "keep" }
 "550" {
  // unmatched } { in comment
  "nested" { "LaunchOptions" "nested" }
  "LaunchOptions" "old" // preserve this comment
 }
}`
	original = steamConfigFixture(original)
	got, err := setAppLaunchOptionsInText(original, `-steam +exec "my file.cfg" $HOME`)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{`"LaunchOptions" "comment"`, `"LaunchOptions" "unrelated"`, `"LaunchOptions" "nested"`, `"LaunchOptions" "keep"`, `// preserve this comment`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("modified unrelated content: %s", keep)
		}
	}
	again, err := setAppLaunchOptionsInText(got, `-steam +exec "my file.cfg" $HOME`)
	if err != nil || again != got {
		t.Fatalf("not idempotent: %v", err)
	}
}

func TestSteamVDFRejectAmbiguity(t *testing.T) {
	for _, text := range []string{`"apps" { "550" {} "550" {} }`, `"apps" { "550" { "LaunchOptions" "a" "LaunchOptions" "b" } }`, `"apps" {} "apps" {}`, `"apps" {`, `"apps" { "550" "bad" }`} {
		if _, err := setAppLaunchOptionsInText(steamConfigFixture(text), "-steam"); err == nil {
			t.Fatalf("accepted ambiguous or malformed VDF: %s", text)
		}
	}
}
