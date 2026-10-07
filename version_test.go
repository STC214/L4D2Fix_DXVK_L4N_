//go:build windows

package main

import "testing"

func TestWindowTitleBuildVersion(t *testing.T) {
	old := buildVersion
	t.Cleanup(func() { buildVersion = old })
	buildVersion = "202610071234"
	if got := windowTitle(); got != "L4N 202610071234" {
		t.Fatalf("title=%q", got)
	}
	if first, second := windowTitle(), windowTitle(); first != second {
		t.Fatal("build version should be stable across calls")
	}
	buildVersion = "开发版"
	if got := windowTitle(); got != "L4N 开发版" {
		t.Fatalf("development title=%q", got)
	}
}
