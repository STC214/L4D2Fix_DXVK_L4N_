//go:build windows

package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestPlatformSubscriptionsOnlyAfterSuccessfulSkeetoOperation(t *testing.T) {
	for _, kind := range []string{"skeeto", "evolved", ""} {
		for _, fail := range []bool{true, false} {
			updated := false
			err := withPlatformSubscriptions(kind, func() error {
				if fail {
					return errors.New("file operation failed")
				}
				return nil
			}, func() { updated = true })
			if (err != nil) != fail || updated != (kind == "skeeto" && !fail) {
				t.Fatalf("kind=%s fail=%t err=%v updated=%t", kind, fail, err, updated)
			}
		}
	}
}

func TestSubscriptionConfirmationMarker(t *testing.T) {
	marker := "SKEETO_unsubscribe_CONFIRMED:3782512354,3787239937"
	for _, tc := range []struct {
		out  string
		want bool
	}{
		{"Steam diagnostic\r\n" + marker + "\r\n", true},
		{"opened workshop page", false},
		{"prefix" + marker, false},
		{"SKEETO_subscribe_CONFIRMED:3782512354,3787239937", false},
	} {
		if subscriptionConfirmed(tc.out, marker) != tc.want {
			t.Fatal(tc)
		}
	}
}

func TestSteamInstallLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name          string
		auto, running bool
		failAt        string
		want          []string
		write         bool
	}{
		{"manual", false, true, "", []string{"install"}, false},
		{"auto running", true, true, "", []string{"running", "shutdown", "wait", "install", "start"}, true},
		{"auto closed", true, false, "", []string{"running", "wait", "install", "start"}, true},
		{"query failure", true, true, "running", []string{"running"}, false},
		{"shutdown failure", true, true, "shutdown", []string{"running", "shutdown", "start"}, false},
		{"timeout", true, true, "wait", []string{"running", "shutdown", "wait", "start"}, false},
		{"install failure", true, true, "install", []string{"running", "shutdown", "wait", "install", "start"}, true},
		{"closed install failure", true, false, "install", []string{"running", "wait", "install"}, true},
		{"restart failure", true, true, "start", []string{"running", "shutdown", "wait", "install", "start"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			wrote := false
			call := func(s string) error {
				got = append(got, s)
				if tc.failAt == s {
					return errors.New(s)
				}
				return nil
			}
			h := steamInstallHooks{running: func() (bool, error) { return tc.running, call("running") }, shutdown: func() error { return call("shutdown") }, wait: func() error { return call("wait") }, start: func() error { return call("start") }}
			e := withSteamInstall(tc.auto, h, func(write bool) error { wrote = write; return call("install") })
			if (e != nil) != (tc.failAt != "") || !reflect.DeepEqual(got, tc.want) || wrote != tc.write {
				t.Fatalf("error=%v calls=%v write=%v", e, got, wrote)
			}
		})
	}
}
