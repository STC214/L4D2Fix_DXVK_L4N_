//go:build windows

package main

import (
	"crypto/sha256"
	"os"
	"strings"
	"testing"
)

func steamConfigFixture(body string) string {
	return "\"UserLocalConfigStore\" { \"Software\" { \"Valve\" { \"Steam\" {\n" + body + "\n} } } }"
}

func TestSteamAppsCanonicalPathOnly(t *testing.T) {
	for _, body := range []string{
		`"apps" { "550" { "LaunchOptions" "old" } "730" { "LaunchOptions" "keep" } }`,
		`"apps" { "550" { "nested" { "LaunchOptions" "keep" } } }`,
		`"apps" { "730" { "LaunchOptions" "keep" } }`,
	} {
		base := steamConfigFixture(body)
		// Put unrelated apps first, including a conflicting 550 launch option.
		other := `"WebStorage" { "apps" { "550" { "LaunchOptions" "WEB-KEEP" } "431960" {} } }`
		text := strings.Replace(base, `"UserLocalConfigStore" {`, `"UserLocalConfigStore" { `+other, 1)
		options := `-steam +exec "my file.cfg"`
		got, err := setAppLaunchOptionsInText(text, options)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, other) {
			t.Fatal("unrelated WebStorage changed")
		}
		nodes, err := parseVDF(got)
		if err != nil {
			t.Fatal(err)
		}
		apps, err := steamLaunchAppsBlock(nodes)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, game := range apps.children {
			if game.key.value != "550" {
				continue
			}
			for _, f := range game.children {
				if strings.EqualFold(f.key.value, "LaunchOptions") && !f.block && f.value.value == options {
					found++
				}
			}
		}
		if found != 1 {
			t.Fatalf("target option count=%d", found)
		}
		if !strings.Contains(got, `"LaunchOptions" "keep"`) {
			t.Fatal("other/nested field changed")
		}
		again, err := setAppLaunchOptionsInText(got, options)
		if err != nil || again != got {
			t.Fatal("not idempotent", err)
		}
	}
}

func TestSteamCanonicalPathRejectsAmbiguity(t *testing.T) {
	valid := steamConfigFixture(`"apps" { "550" {} }`)
	cases := []string{
		`"apps" { "550" {} }`,
		`"UserLocalConfigStore" { "WebStorage" { "apps" { "550" {} } } }`,
		valid + valid,
	}
	for _, key := range []string{"Software", "Valve", "Steam", "apps"} {
		// Duplicate either an object or a scalar at the exact target level.
		for _, duplicate := range []string{`"` + key + `" {} `, `"` + key + `" "bad" `} {
			cases = append(cases, strings.Replace(valid, `"`+key+`" {`, duplicate+`"`+key+`" {`, 1))
		}
		cases = append(cases, strings.Replace(valid, `"`+key+`"`, `"missing"`, 1))
	}
	for _, text := range cases {
		if _, err := setAppLaunchOptionsInText(text, "-steam"); err == nil {
			t.Fatalf("accepted invalid path: %s", text)
		}
	}
	mixed := strings.NewReplacer("UserLocalConfigStore", "userlocalconfigstore", "Software", "SOFTWARE", "Valve", "valve", "Steam", "STEAM", "apps", "Apps").Replace(valid)
	if _, err := setAppLaunchOptionsInText(mixed, "-steam"); err != nil {
		t.Fatal(err)
	}
}

func TestReportedSteamConfigReadOnly(t *testing.T) {
	path := os.Getenv("L4N_TEST_VDF_SAMPLE")
	if path == "" {
		t.Skip("set L4N_TEST_VDF_SAMPLE for received config regression")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseVDF(string(b))
	if err != nil {
		t.Fatal(err)
	}
	apps, err := steamLaunchAppsBlock(nodes)
	if err != nil {
		t.Fatal(err)
	}
	var fields []vdfNode
	for _, g := range apps.children {
		if g.key.value == "550" {
			for _, f := range g.children {
				if strings.EqualFold(f.key.value, "LaunchOptions") {
					fields = append(fields, f)
				}
			}
		}
	}
	if len(fields) != 1 || fields[0].block {
		t.Fatal("expected one original launch option")
	}
	f := fields[0]
	options := "-heapsize 2097152 -processheap -high -novid -nojoy -steam -vulkan"
	edited, err := setAppLaunchOptionsInText(string(b), options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(edited, string(b[:f.value.start])) || !strings.HasSuffix(edited, string(b[f.value.end:])) {
		t.Fatal("unrelated bytes changed")
	}
	again, err := setAppLaunchOptionsInText(edited, options)
	if err != nil || again != edited {
		t.Fatal("not idempotent", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(b) {
		t.Fatal("source sample modified")
	}
	t.Log("Received sample PASS: only canonical 550/LaunchOptions value changed in memory; WebStorage and all other bytes preserved; original file untouched")
}
