//go:build windows

package main

import (
	"fmt"
	"l4nfix/internal/pathguard"
	"path/filepath"
	"strings"
)

func validateManifestBackupPaths(m *manifest, backupRoot string) error {
	if err := pathguard.Within(filepath.Dir(backupRoot), filepath.Join(backupRoot, "manifest.json")); err != nil {
		return err
	}
	for _, e := range m.Files {
		if e.Existed {
			if err := pathguard.Within(backupRoot, e.Backup); err != nil {
				return err
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range m.SteamConfigs {
		if !e.Existed {
			return fmt.Errorf("unexpected missing Steam original")
		}
		if !filepath.IsAbs(e.Target) || !strings.EqualFold(filepath.Base(e.Target), "localconfig.vdf") || !strings.EqualFold(filepath.Base(filepath.Dir(e.Target)), "config") {
			return fmt.Errorf("invalid Steam config target: %s", e.Target)
		}
		account := filepath.Dir(filepath.Dir(e.Target))
		userdata := filepath.Dir(account)
		if !strings.EqualFold(filepath.Base(userdata), "userdata") {
			return fmt.Errorf("Steam config target not in userdata: %s", e.Target)
		}
		if err := pathguard.Within(userdata, e.Target); err != nil {
			return err
		}
		key := strings.ToLower(clean(e.Target))
		if seen[key] {
			return fmt.Errorf("duplicate Steam config target")
		}
		seen[key] = true
		if err := pathguard.Within(backupRoot, e.Backup); err != nil {
			return err
		}
	}
	return nil
}
