//go:build windows

package pathguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsJunctionAndLexicalPaths(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "game")
	outside := filepath.Join(r, "outside")
	os.MkdirAll(root, 0755)
	os.MkdirAll(outside, 0755)
	link := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, out)
	}
	for _, p := range []string{root, filepath.Join(r, "outside", "file"), filepath.Join(link, "file"), "relative-file"} {
		if err := Within(root, p); err == nil {
			t.Fatalf("invalid target accepted: %s", p)
		}
	}
	if err := Within(root, filepath.Join(root, "new", "file")); err != nil {
		t.Fatal(err)
	}
	if err := Within(link, filepath.Join(link, "file")); err == nil {
		t.Fatal("junction used as root was accepted")
	}
	plain := filepath.Join(r, "plain-file")
	if err := os.WriteFile(plain, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Within(plain, filepath.Join(plain, "child")); err == nil {
		t.Fatal("file used as root was accepted")
	}
}
