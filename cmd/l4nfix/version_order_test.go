//go:build windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestVersionOptionsNewestFirst(t *testing.T) {
	for _, tc := range []struct {
		kind        string
		input, want []string
	}{
		{"dxvk", []string{"dxvk-2.9.zip", "dxvk-3.1.zip", "dxvk-3.1.1.tar.gz", "dxvk-2.10.zip", "dxvk-async-2.0.zip"}, []string{"dxvk-3.1.1.tar.gz", "dxvk-3.1.zip", "dxvk-2.10.zip", "dxvk-2.9.zip", "dxvk-async-2.0.zip"}},
		{"l4n", []string{"L4N_dxvk2.7.1.zip", "L4N_v2.9.0.zip", "L4N_v2.50.3.zip", "L4N-2.51.0-1791462722-16068050382074527913.zip", "L4N_v2.100.0.zip"}, []string{"L4N_v2.100.0.zip", "L4N-2.51.0-1791462722-16068050382074527913.zip", "L4N_v2.50.3.zip", "L4N_v2.9.0.zip", "L4N_dxvk2.7.1.zip"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			var options []dxvkOption
			for i, name := range tc.input {
				p := filepath.Join(dir, name)
				if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				// Give earlier/older versions newer local timestamps: numeric
				// release order must survive copying/touching old archives.
				stamp := time.Unix(1000-int64(i), 0)
				if err := os.Chtimes(p, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				options = append(options, dxvkOption{Name: name, Dir: p})
			}
			sortVersionOptionsNewestFirst(tc.kind, options)
			var got []string
			for _, option := range options {
				got = append(got, option.Name)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestVersionOrderTimeTieBreakAndUnknownFallback(t *testing.T) {
	root := t.TempDir()
	names := []string{"dxvk-3.1.1.zip", "dxvk-3.1.1.tar.gz", "unknown-old.zip", "unknown-new.zip"}
	var options []dxvkOption
	for i, name := range names {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		x := time.Unix(int64(100+i), 0)
		if err := os.Chtimes(p, x, x); err != nil {
			t.Fatal(err)
		}
		options = append(options, dxvkOption{Name: name, Dir: p})
	}
	sortVersionOptionsNewestFirst("dxvk", options)
	want := []string{names[1], names[0], names[3], names[2]}
	for i, opt := range options {
		if opt.Name != want[i] {
			t.Fatal(options)
		}
	}
	if compareOptionVersions([]string{"3", "1"}, []string{"3", "01", "0"}) != 0 {
		t.Fatal("equivalent components differ")
	}
}

func TestVersionOrderPrefixlessManualImports(t *testing.T) {
	for _, kind := range []string{"l4n", "dxvk"} {
		options := []dxvkOption{{Name: kind + "-2.9.0.zip", Dir: "old"}, {Name: "v2.11.0.zip", Dir: "new"}, {Name: "2.10.0.zip", Dir: "middle"}}
		sortVersionOptionsNewestFirst(kind, options)
		if options[0].Dir != "new" || options[1].Dir != "middle" || options[2].Dir != "old" {
			t.Fatalf("%s: %v", kind, options)
		}
	}
	if optionVersionParts("l4n", "L4N_dxvk2.7.1.zip") != nil {
		t.Fatal("DXVK version treated as L4N version")
	}
}
