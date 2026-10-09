//go:build windows

package main

import (
	"fmt"
	"l4nfix/internal/pathguard"
	"path/filepath"
	"strings"
)

func steamBackupName(target string) string {
	return strings.NewReplacer(":", "_", "\\", "_", "/", "_").Replace(clean(target))
}

// Resolve only canonical backup names. A legacy absolute location is accepted
// only when its suffix identifies our own backup tree, never an arbitrary file.
func resolveBackupLocations(m *manifest, root string) error {
	if err := pathguard.Within(filepath.Dir(root), filepath.Join(root, "manifest.json")); err != nil {
		return err
	}
	resolve := func(recorded, relative string) (string, error) {
		local := filepath.Join(root, relative)
		if recorded == "" {
			return "", fmt.Errorf("missing backup path")
		}
		if filepath.IsAbs(recorded) {
			if strings.EqualFold(clean(recorded), clean(local)) {
				return local, pathguard.Within(root, local)
			}
			suffix := string(filepath.Separator) + filepath.Join(".l4n_auto_backup", relative)
			if !strings.HasSuffix(strings.ToLower(clean(recorded)), strings.ToLower(suffix)) {
				return "", fmt.Errorf("unexpected legacy backup path: %s", recorded)
			}
		} else if !strings.EqualFold(recorded, relative) {
			return "", fmt.Errorf("invalid relative backup path: %s", recorded)
		}
		return local, pathguard.Within(root, local)
	}
	// Work on a copy so a malformed later entry never partially migrates state.
	c := *m
	c.Files = append([]fileEntry(nil), m.Files...)
	c.SteamConfigs = append([]steamEntry(nil), m.SteamConfigs...)
	for i := range c.Files {
		e := &c.Files[i]
		if !e.Existed {
			continue
		}
		rel, err := packageRelative(e.Rel)
		if err != nil || rel == "" {
			return fmt.Errorf("invalid backup relative target: %s", e.Rel)
		}
		targetRel, err := filepath.Rel(m.GameRoot, e.Target)
		if err != nil || !strings.EqualFold(targetRel, rel) {
			return fmt.Errorf("backup relative target mismatch: %s", e.Target)
		}
		if e.Backup, err = resolve(e.Backup, filepath.Join("files", filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	for i := range c.SteamConfigs {
		e := &c.SteamConfigs[i]
		var err error
		if e.Backup, err = resolve(e.Backup, filepath.Join("steam", steamBackupName(e.Target))); err != nil {
			return err
		}
	}
	*m = c
	return nil
}

// Persist relative locations without changing the live absolute paths used by
// the current operation. Both new and migrated manifests become portable.
func portableManifest(m *manifest, root string) (*manifest, error) {
	c := *m
	if err := resolveBackupLocations(&c, root); err != nil {
		return nil, err
	}
	for i := range c.Files {
		e := &c.Files[i]
		if !e.Existed {
			continue
		}
		if err := pathguard.Within(root, e.Backup); err != nil {
			return nil, err
		}
		r, err := filepath.Rel(root, e.Backup)
		if err != nil {
			return nil, err
		}
		e.Backup = r
	}
	for i := range c.SteamConfigs {
		e := &c.SteamConfigs[i]
		if err := pathguard.Within(root, e.Backup); err != nil {
			return nil, err
		}
		r, err := filepath.Rel(root, e.Backup)
		if err != nil {
			return nil, err
		}
		e.Backup = r
	}
	return &c, nil
}
