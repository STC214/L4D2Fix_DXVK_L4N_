//go:build windows

package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed platform_version_seeds.json
var platformVersionSeeds []byte

type platformVersionIdentity struct {
	Kind    string            `json:"kind"`
	Version string            `json:"version"`
	Hashes  map[string]string `json:"hashes"`
}

func platformVersionName(kind, name string) string {
	prefix := `(?:L4D2Evolved|Evolved|Evo)`
	if kind == "skeeto" {
		prefix = `Skeeto`
	}
	m := regexp.MustCompile(`(?i)^` + prefix + `[-_ ]v?(\d+(?:\.\d+){1,3})(?:[-_ .]|$)`).FindStringSubmatch(name)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func platformCoreNames(kind string) []string {
	if kind == "skeeto" {
		return []string{"skeeto.dll"}
	}
	return []string{"l4d2evolved.dll", "l4d2evomodel.dll"}
}

func platformIdentityMatches(kind string, expected, actual map[string]string) bool {
	for _, name := range platformCoreNames(kind) {
		want := expected[name]
		if want == "" {
			return false
		}
		found := false
		for rel, h := range actual {
			if strings.EqualFold(filepath.Base(rel), name) {
				found = true
				if h != want {
					return false
				}
			}
		}
		if !found {
			return false
		}
	}
	// A known proxy must also agree while present; never identify a mixed pair
	// as a fully matching package based on the core DLL alone.
	if want := expected["wininet.dll"]; kind == "skeeto" && want != "" && actual["bin/wininet.dll"] != "" && actual["bin/wininet.dll"] != want {
		return false
	}
	return true
}

func platformIdentityFromMappings(files []packageMapping) map[string]string {
	h := map[string]string{}
	for _, f := range files {
		key := strings.ToLower(filepath.Base(filepath.FromSlash(f.Relative)))
		if key != "skeeto.dll" && key != "wininet.dll" && key != "l4d2evolved.dll" && key != "l4d2evomodel.dll" {
			continue
		}
		if old := h[key]; old != "" && old != f.SHA256 {
			return nil
		}
		h[key] = f.SHA256
	}
	return h
}

// Read-only identities: no preparePackage, extraction into resources, script
// execution or game DLL loading. A stale filename/marker alone is no evidence.
func cachedPlatformIdentities(res, kind string) []platformVersionIdentity {
	var result []platformVersionIdentity
	var seeds []platformVersionIdentity
	if json.Unmarshal(platformVersionSeeds, &seeds) == nil {
		for _, s := range seeds {
			if s.Kind == kind {
				result = append(result, s)
			}
		}
	}
	indexed := map[string]string{}
	if idx, e := readWorkshopIndex(res); e == nil {
		for _, r := range idx.Resources {
			if r.Platform == kind {
				if p, e := verifiedWorkshopArchive(res, r); e == nil {
					indexed[clean(p)] = r.Version
				}
			}
		}
	}
	dir := filepath.Join(res, kind)
	if pathguard.Within(res, filepath.Join(dir, "probe")) != nil {
		return result
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return result
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, ok := archiveName(entry.Name()); !entry.IsDir() && !ok {
			continue
		}
		p := filepath.Join(dir, entry.Name())
		if pathguard.Within(res, p) != nil {
			continue
		}
		id, e := resourceIdentityFor(p, kind)
		if e != nil {
			continue
		}
		version := indexed[clean(p)]
		if version == "" {
			version = platformVersionName(kind, entry.Name())
		}
		if version == "" {
			continue
		}
		h := map[string]string{}
		ambiguous := false
		for rel, hash := range id.Hashes {
			name := strings.ToLower(filepath.Base(filepath.FromSlash(rel)))
			if h[name] != "" && h[name] != hash {
				ambiguous = true
			}
			h[name] = hash
		}
		if !ambiguous {
			result = append(result, platformVersionIdentity{kind, version, h})
		}
	}
	return result
}

func detectedPlatformText(game, res, backup, kind string) (string, bool, error) {
	actual := map[string]string{}
	coreCount := 0
	complete := false
	residual := false
	prefixes := []string{"", "bin/", "bin/neko/plugins/"}
	for _, prefix := range prefixes {
		groupComplete := true
		for _, name := range platformCoreNames(kind) {
			rel := prefix + name
			if kind == "skeeto" && prefix == "bin/" {
				continue
			}
			p := filepath.Join(game, filepath.FromSlash(rel))
			if e := pathguard.Within(game, p); e != nil {
				return "", false, e
			}
			h, e := fileHash(p)
			if os.IsNotExist(e) {
				groupComplete = false
				continue
			}
			if e != nil {
				return "", false, e
			}
			actual[rel] = h
			coreCount++
		}
		if kind == "skeeto" && prefix == "bin/" {
			continue
		}
		if groupComplete {
			complete = true
		}
	}
	identities := cachedPlatformIdentities(res, kind)
	if kind == "skeeto" {
		p := filepath.Join(game, "bin", "wininet.dll")
		if e := pathguard.Within(game, p); e != nil {
			return "", false, e
		}
		h, e := fileHash(p)
		if e != nil && !os.IsNotExist(e) {
			return "", false, e
		}
		if e == nil {
			actual["bin/wininet.dll"] = h
			for _, id := range identities {
				if id.Hashes["wininet.dll"] == h {
					residual = true
				}
			}
		}
		if actual["skeeto.dll"] != "" && actual["bin/wininet.dll"] == "" {
			complete = false
		}
	}
	private := "Evolved"
	if kind == "skeeto" {
		private = "Skeeto"
	}
	for _, rel := range []string{private} {
		p := filepath.Join(game, rel)
		if e := pathguard.Within(game, p); e != nil {
			return "", false, e
		}
		if _, e := os.Stat(p); e == nil {
			residual = true
		} else if !os.IsNotExist(e) {
			return "", false, e
		}
	}
	if coreCount == 0 {
		if residual {
			return "仅残留／部署不完整", false, nil
		}
		return "未使用", false, nil
	}
	version := "检测到文件，版本未知"
	// Managed version is accepted only with matching actual core hashes.
	if pathguard.Within(filepath.Dir(backup), filepath.Join(backup, "manifest.json")) == nil {
		if b, e := os.ReadFile(filepath.Join(backup, "manifest.json")); e == nil {
			var m manifest
			if json.Unmarshal(b, &m) == nil && m.Platform == kind && m.PlatformVersion != "" && strings.EqualFold(clean(m.GameRoot), clean(game)) {
				var mappings []packageMapping
				for _, f := range m.Files {
					mappings = append(mappings, packageMapping{Relative: f.Rel, SHA256: f.SourceSHA256})
				}
				if platformIdentityMatches(kind, platformIdentityFromMappings(mappings), actual) {
					version = m.PlatformVersion
				}
			}
		}
	}
	if version == "检测到文件，版本未知" {
		names := map[string]bool{}
		for _, id := range identities {
			if platformIdentityMatches(kind, id.Hashes, actual) {
				names[id.Version] = true
			}
		}
		var list []string
		for name := range names {
			list = append(list, name)
		}
		sort.Strings(list)
		if len(list) > 0 {
			shown := list
			if len(shown) > 2 {
				shown = shown[:2]
			}
			version = strings.Join(shown, " / ")
			if len(list) > 1 {
				version += fmt.Sprintf("（%d 个候选）", len(list))
			}
		}
	}
	if !complete {
		version += "（部署不完整）"
	}
	if coreCount > len(platformCoreNames(kind)) {
		version += "（多位置残留）"
	}
	return version, true, nil
}

func installedPlatformText(game, res, backup string) (string, error) {
	evo, evoCore, evoErr := detectedPlatformText(game, res, backup, "evolved")
	if evoErr != nil {
		evo = "检测失败（文件不可读）"
		evoCore = false
	}
	skeeto, skeetoCore, skeetoErr := detectedPlatformText(game, res, backup, "skeeto")
	if skeetoErr != nil {
		skeeto = "检测失败（文件不可读）"
		skeetoCore = false
	}
	text := fmt.Sprintf("增强平台（磁盘检测）：\r\nEVO：%s\r\nSkeeto：%s", evo, skeeto)
	if evoCore && skeetoCore {
		text += "\r\n注意：两个平台核心共存，请核对"
	}
	return text, errors.Join(evoErr, skeetoErr)
}
