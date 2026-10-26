//go:build windows

package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A kind-specific resource directory already identifies the component, so
// manually imported archives may contain only a version, without a prefix.
var l4nOptionVersion = regexp.MustCompile(`(?i)^(?:l4n[-_ ]?)?v?(\d+(?:\.\d+){1,3})`)
var dxvkOptionVersion = regexp.MustCompile(`(?i)^(?:dxvk[-_ ](?:async[-_ ])?)?v?(\d+(?:\.\d+){1,3})`)

func optionVersionParts(kind, name string) []string {
	re := l4nOptionVersion
	if kind == "dxvk" {
		re = dxvkOptionVersion
	}
	match := re.FindStringSubmatch(name)
	if len(match) < 2 {
		return nil
	}
	return strings.Split(match[1], ".")
}

// Compare numeric components, not alphabetic names or floating point values.
// Arbitrarily long version components also remain deterministic.
func compareOptionVersions(a, b []string) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := "", ""
		if i < len(a) {
			x = strings.TrimLeft(a[i], "0")
		}
		if i < len(b) {
			y = strings.TrimLeft(b[i], "0")
		}
		if len(x) != len(y) {
			if len(x) > len(y) {
				return 1
			}
			return -1
		}
		if x != y {
			return strings.Compare(x, y)
		}
	}
	return 0
}

func sortVersionOptionsNewestFirst(kind string, options []dxvkOption) {
	versions := make(map[string][]string, len(options))
	modified := make(map[string]time.Time, len(options))
	for _, option := range options {
		versions[option.Dir] = optionVersionParts(kind, option.Name)
		if info, err := os.Stat(option.Dir); err == nil {
			modified[option.Dir] = info.ModTime()
		}
	}
	sort.SliceStable(options, func(i, j int) bool {
		a, b := versions[options[i].Dir], versions[options[j].Dir]
		// Legacy mixed L4N+DXVK names do not identify the L4N version.
		// Keep them available, below explicit versions, without mislabeling.
		if (len(a) > 0) != (len(b) > 0) {
			return len(a) > 0
		}
		if order := compareOptionVersions(a, b); order != 0 {
			return order > 0
		}
		x, y := modified[options[i].Dir], modified[options[j].Dir]
		if !x.Equal(y) {
			return x.After(y)
		}
		return strings.ToLower(options[i].Name) < strings.ToLower(options[j].Name)
	})
}
