//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigArchiveCases(t *testing.T) {
	for _, scenario := range []string{"absent", "same", "different", "tracked-same", "tracked-edited", "foreign-manifest", "blocked"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			game, res := filepath.Join(root, "game"), filepath.Join(root, "resources")
			target := filepath.Join(game, filepath.FromSlash(configRelativePath))
			incoming := filepath.Join(root, "incoming.vdf")
			testFiles(t, root, map[string][]byte{"incoming.vdf": []byte("new default")})
			current := "new default"
			if scenario != "same" && scenario != "absent" {
				current = "user config"
			}
			if scenario != "absent" {
				testFiles(t, game, map[string][]byte{configRelativePath: []byte(current)})
			}
			man := &manifest{GameRoot: game}
			if scenario == "tracked-same" || scenario == "tracked-edited" || scenario == "foreign-manifest" {
				last := filepath.Join(root, "last.vdf")
				text := current
				if scenario == "tracked-edited" {
					text = "previous installed config"
				}
				os.WriteFile(last, []byte(text), 0600)
				hash, _ := fileHash(last)
				man.Files = []fileEntry{{Target: target, SourceSHA256: hash}}
				if scenario == "foreign-manifest" {
					man.GameRoot = filepath.Join(root, "other-game")
				}
			}
			if scenario == "blocked" {
				os.MkdirAll(res, 0755)
				os.WriteFile(filepath.Join(res, configArchiveDirName), []byte("blocked"), 0600)
			}
			archived, err := archiveL4nConfigIfChanged(game, res, incoming, man)
			if scenario == "blocked" {
				if err == nil {
					t.Fatal("archive failure was not propagated")
				}
				b, _ := os.ReadFile(target)
				if string(b) != current {
					t.Fatal("game modified")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			shouldArchive := scenario != "absent" && scenario != "same" && scenario != "tracked-same"
			if (archived != "") != shouldArchive {
				t.Fatalf("unexpected archive %q", archived)
			}
			if shouldArchive {
				b, err := os.ReadFile(archived)
				if err != nil || string(b) != current {
					t.Fatal("snapshot mismatch", err)
				}
				again, err := archiveL4nConfigIfChanged(game, res, incoming, man)
				if err != nil || again != archived {
					t.Fatal("duplicate snapshot", err)
				}
				os.WriteFile(target, []byte("second edit"), 0600)
				second, err := archiveL4nConfigIfChanged(game, res, incoming, man)
				if err != nil || second == archived {
					t.Fatal("second revision lost", err)
				}
				b, _ = os.ReadFile(archived)
				if string(b) != current {
					t.Fatal("earlier snapshot overwritten")
				}
			}
		})
	}
}

func TestArchiveLocalConfigSnapshot(t *testing.T) {
	game, res, incoming := os.Getenv("L4N_ARCHIVE_GAME"), os.Getenv("L4N_ARCHIVE_RESOURCES"), os.Getenv("L4N_ARCHIVE_BASELINE")
	if game == "" || res == "" || incoming == "" {
		t.Skip("explicit local snapshot environment not set")
	}
	source := filepath.Join(game, filepath.FromSlash(configRelativePath))
	before, err := fileHash(source)
	if err != nil {
		t.Fatal(err)
	}
	path, err := archiveL4nConfigIfChanged(game, res, incoming, nil)
	if err != nil || path == "" {
		t.Fatal("local snapshot failed", err)
	}
	after, _ := fileHash(source)
	saved, _ := fileHash(path)
	if before != after || before != saved {
		t.Fatal("local file or archive changed")
	}
	t.Log("local snapshot:", path, "SHA256:", saved)
}
