//go:build windows

package main

import (
	"os"
	"testing"
)

func TestNativeFontEnumerationReadOnly(t *testing.T) {
	if os.Getenv("L4N_NATIVE_FONT_TEST") != "1" {
		t.Skip("native read-only check not enabled")
	}
	names := installedFontNames()
	if len(names) == 0 {
		t.Fatal("font enumeration empty")
	}
	t.Logf("native font enumeration: %d families", len(names))
}
