//go:build windows

package main

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed cleanup_signatures.json
var cleanupSeedJSON []byte

var cleanupCatalogMu sync.Mutex

type cleanupSignature struct {
	Kind   string `json:"kind"`
	Rel    string `json:"path"`
	SHA256 string `json:"sha256"`
}

func cleanupKindAllowed(scope, kind string) bool {
	return (scope == "base" && (kind == "l4n" || kind == "dxvk")) || (scope == "platform" && (kind == "skeeto" || kind == "evolved"))
}

// Every comparison uses a game-relative path AND content hash, never basename
// alone for a shared loader, engine file, VPK, shader, script, or generic marker.
func cleanupAliases(kind, rel string) []string {
	r := strings.ToLower(filepath.ToSlash(rel))
	out := []string{r}
	if kind == "dxvk" {
		name := filepath.Base(r)
		if strings.HasSuffix(name, ".dll") {
			out = []string{name, "bin/" + name}
			if name == "d3d9.dll" || name == "dxvk_d3d9.dll" {
				out = append(out, "d3d9.dll", "bin/d3d9.dll", "dxvk_d3d9.dll", "bin/dxvk_d3d9.dll")
			}
		} else {
			out = nil
		}
	}
	if kind == "l4n" {
		if r == "left4neko.dll" {
			out = append(out, "bin/left4neko.dll")
		}
		if strings.HasPrefix(r, "neko/") || strings.HasPrefix(r, "shaders/") {
			out = append(out, "left4dead2/"+r)
		}
		if strings.HasSuffix(r, "/config_template.vdf") {
			out = append(out, strings.TrimSuffix(r, "config_template.vdf")+"config.vdf")
		}
	}
	if kind == "skeeto" && r == "skeeto.dll" {
		out = append(out, "necola.dll", "bin/neko/plugins/skeeto.dll")
	}
	return out
}

func cleanupScopePath(scope, rel string) bool {
	r := strings.ToLower(filepath.ToSlash(rel))
	// These loaders/renderers are shared with the base route. Enhancement cleanup
	// must not remove them, even when an enhancement ZIP bundles identical bytes.
	if scope == "platform" {
		if r == "left4dead2.exe" || r == "bin/left4neko.dll" || r == "dxgi.dll" || r == "dxvk_d3d9.dll" || r == "bin/dxvk_d3d9.dll" || r == "d3d9.dll" || r == "bin/d3d9.dll" {
			return false
		}
		if strings.HasPrefix(r, "left4dead2/neko/") || strings.HasPrefix(r, "left4dead2/shaders/") || strings.HasPrefix(r, "left4dead2/materials/l4n/") {
			return false
		}
	}
	if scope == "base" {
		if r == skeetoAddonListRel || isSkeetoModRelative(r) {
			return false
		}
		if strings.HasPrefix(r, "bin/neko/plugins/") && r != "bin/neko/plugins/l4n_plugin.h" {
			return false
		}
		if cleanupPrivateKind(r) == "skeeto" || cleanupPrivateKind(r) == "evolved" || r == "bin/wininet.dll" || r == "winmm.dll" || r == "bin/winmm.dll" || r == "left4dead2/cfg/autoexec.cfg" {
			return false
		}
	}
	return true
}

// Collect the complete raw package layout, including alternate/manual loaders
// intentionally excluded by deployment normalization. Input was already bounded
// and validated by preparePackage. No script from a package is executed.
func cleanupPackageSignatures(kind, input string) ([]cleanupSignature, error) {
	files, err := packageInventory(input)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if kind != "dxvk" {
		anchor := "bin/left4neko.dll"
		if kind == "skeeto" {
			anchor = "skeeto.dll"
		}
		if kind == "evolved" {
			anchor = "bin/neko/plugins/l4d2evolved.dll"
		}
		found := false
		for _, f := range files {
			r := strings.ToLower(filepath.ToSlash(f))
			if r == anchor || strings.HasSuffix(r, "/"+anchor) {
				prefix = r[:len(r)-len(anchor)]
				found = true
				break
			}
		}
		if !found && kind == "l4n" {
			for _, f := range files {
				r := strings.ToLower(filepath.ToSlash(f))
				if r == "left4neko.dll" || strings.HasSuffix(r, "/left4neko.dll") {
					prefix = r[:len(r)-len("left4neko.dll")]
					found = true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("cleanup package anchor missing: %s", kind)
		}
	}
	var out []cleanupSignature
	for _, f := range files {
		r := strings.ToLower(filepath.ToSlash(f))
		if !strings.HasPrefix(r, prefix) {
			continue
		}
		r = strings.TrimPrefix(r, prefix)
		h, e := fileHash(filepath.Join(input, f))
		if e != nil {
			return nil, e
		}
		for _, rel := range cleanupAliases(kind, r) {
			out = append(out, cleanupSignature{kind, rel, h})
		}
	}
	return out, nil
}

func cleanupSignaturePath(res string) string { return filepath.Join(res, "cleanup-signatures.json") }

func mergeCleanupSignatures(rows ...[]cleanupSignature) ([]cleanupSignature, error) {
	seen := map[string]bool{}
	var out []cleanupSignature
	for _, list := range rows {
		for _, r := range list {
			if !cleanupKindAllowed("base", r.Kind) && !cleanupKindAllowed("platform", r.Kind) {
				return nil, fmt.Errorf("unknown cleanup kind: %s", r.Kind)
			}
			rel, e := packageRelative(r.Rel)
			if e != nil || rel == "" {
				return nil, fmt.Errorf("invalid cleanup path: %s", r.Rel)
			}
			if b, e := hex.DecodeString(r.SHA256); e != nil || len(b) != 32 {
				return nil, fmt.Errorf("invalid cleanup SHA256")
			}
			r.Rel = strings.ToLower(filepath.ToSlash(rel))
			r.SHA256 = strings.ToLower(r.SHA256)
			key := r.Kind + "\x00" + r.Rel + "\x00" + r.SHA256
			if !seen[key] {
				seen[key] = true
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Kind+"/"+a.Rel+a.SHA256 < b.Kind+"/"+b.Rel+b.SHA256
	})
	return out, nil
}

func readCleanupSignatures(res string) ([]cleanupSignature, error) {
	var seeds, local []cleanupSignature
	if e := json.Unmarshal(cleanupSeedJSON, &seeds); e != nil {
		return nil, e
	}
	p := cleanupSignaturePath(res)
	if e := pathguard.Within(res, p); e != nil {
		return nil, e
	}
	f, e := os.Open(p)
	if e == nil {
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
		if e != nil {
			return nil, e
		}
		if len(b) > 16<<20 {
			return nil, fmt.Errorf("cleanup catalog too large")
		}
		if e = json.Unmarshal(b, &local); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return mergeCleanupSignatures(seeds, local)
}

func rememberCleanupPackage(res, kind, input string) error {
	rows, e := cleanupPackageSignatures(kind, input)
	if e != nil {
		return e
	}
	return rememberCleanupRows(res, rows)
}

func rememberCleanupRows(res string, rows []cleanupSignature) error {
	// Serialize the read/merge/write transaction; atomic rename alone prevents
	// torn JSON, but simultaneous preparations could still lose newer entries.
	cleanupCatalogMu.Lock()
	defer cleanupCatalogMu.Unlock()
	old, e := readCleanupSignatures(res)
	if e != nil {
		return e
	}
	all, e := mergeCleanupSignatures(old, rows)
	if e != nil {
		return e
	}
	b, e := json.MarshalIndent(all, "", "  ")
	if e != nil {
		return e
	}
	return writeFileAtomic(cleanupSignaturePath(res), b, 0644)
}

// Discover all cached versions, not just the current selection/index's latest.
// A damaged archive is reported and skipped; the embedded historical catalog
// and other successful packages remain usable for partial installations.
func refreshCleanupSignatures(res string) ([]cleanupSignature, []string, error) {
	var warnings []string
	for _, kind := range []string{"l4n", "dxvk", "skeeto", "evolved"} {
		dir := filepath.Join(res, kind)
		if e := pathguard.Within(res, filepath.Join(dir, "probe")); e != nil {
			return nil, nil, e
		}
		entries, e := os.ReadDir(dir)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, nil, e
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if _, ok := archiveName(entry.Name()); !entry.IsDir() && !ok {
				continue
			}
			source := filepath.Join(dir, entry.Name())
			p, e := preparePackage(source, res, kind)
			if e != nil {
				warnings = append(warnings, kind+"/"+entry.Name()+": "+e.Error())
				continue
			}
			os.RemoveAll(p.TemporaryDir) // created and bounded by preparePackage
		}
	}
	rows, e := readCleanupSignatures(res)
	return rows, warnings, e
}
