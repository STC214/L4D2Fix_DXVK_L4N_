//go:build windows

package main

import (
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func skeetoAddonKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "/", `\`))
	for _, id := range skeetoModIDs {
		if key == `workshop\`+id+".vpk" {
			return true
		}
	}
	return false
}

func addonListRoot(text string) (vdfNode, error) {
	nodes, e := parseVDF(text)
	if e != nil {
		return vdfNode{}, e
	}
	var roots []vdfNode
	for _, n := range nodes {
		if strings.EqualFold(n.key.value, "AddonList") && n.block {
			roots = append(roots, n)
		}
	}
	if len(roots) != 1 {
		return vdfNode{}, fmt.Errorf("addonlist.txt 必须含一个 AddonList 块")
	}
	return roots[0], nil
}

// Replace just the two owned entries. Every unrelated byte/comment is retained.
func editSkeetoAddonList(current string, original *string) (string, error) {
	if original != nil {
		relevant := false
		for _, id := range skeetoModIDs {
			if strings.Contains(current, id) || strings.Contains(*original, id) {
				relevant = true
			}
		}
		if !relevant {
			return current, nil
		}
	}
	if strings.TrimSpace(strings.TrimPrefix(current, "\xef\xbb\xbf")) == "" {
		if original != nil && strings.TrimSpace(*original) == "" {
			return current, nil
		}
		current += "\"AddonList\"\r\n{\r\n}\r\n"
	}
	root, e := addonListRoot(current)
	if e != nil {
		return "", e
	}
	insert := ""
	if original == nil {
		for _, id := range skeetoModIDs {
			insert += "\r\n\t\"workshop\\" + id + ".vpk\"\t\"1\""
		}
	} else if strings.TrimSpace(strings.TrimPrefix(*original, "\xef\xbb\xbf")) != "" {
		old, e := addonListRoot(*original)
		if e != nil {
			return "", e
		}
		for _, n := range old.children {
			if skeetoAddonKey(n.key.value) {
				if n.block {
					return "", fmt.Errorf("invalid original MOD flag")
				}
				insert += "\r\n\t" + (*original)[n.key.start:n.value.end]
			}
		}
	}
	type edit struct {
		start, end int
		text       string
	}
	edits := []edit{{root.close, root.close, insert + "\r\n"}}
	for _, n := range root.children {
		if skeetoAddonKey(n.key.value) {
			if n.block {
				return "", fmt.Errorf("invalid MOD flag block")
			}
			edits = append(edits, edit{n.key.start, n.value.end, ""})
		}
	}
	if original != nil && len(edits) == 1 && insert == "" {
		return current, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, x := range edits {
		current = current[:x.start] + x.text + current[x.end:]
	}
	return current, nil
}

func skeetoPreparedModFiles(res, stage, game string, record workshopResource) ([]patchFile, error) {
	idx, e := preparedSkeetoMods(res, record.Detail.Content)
	if e != nil {
		return nil, e
	}
	var files []patchFile
	for _, r := range idx.Mods {
		if r.Detail.ID == "3787239937" && r.SHA256 != record.SHA256 {
			return nil, fmt.Errorf("Skeeto MOD 与已检查的平台包内容不一致")
		}
		src, e := verifiedSkeetoMod(res, r)
		if e != nil {
			return nil, e
		}
		rel := skeetoModRelative(r.Detail.ID)
		p := filepath.Join(stage, "workshop-mods", filepath.FromSlash(rel))
		if e := copyFile(src, p); e != nil {
			return nil, e
		}
		h, e := fileHash(p)
		if e != nil || h != r.SHA256 {
			return nil, fmt.Errorf("MOD stage checksum mismatch")
		}
		files = append(files, patchFile{Src: p, Rel: filepath.FromSlash(rel)})
	}
	path := filepath.Join(game, filepath.FromSlash(skeetoAddonListRel))
	if e := pathguard.Within(game, path); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	updated, e := editSkeetoAddonList(string(b), nil)
	if e != nil {
		return nil, e
	}
	p := filepath.Join(stage, "workshop-mods", "addonlist.txt")
	if e := os.WriteFile(p, []byte(updated), 0644); e != nil {
		return nil, e
	}
	return append(files, patchFile{Src: p, Rel: filepath.FromSlash(skeetoAddonListRel)}), nil
}

func restoreModifiedSkeetoAddonList(m *manifest, f fileEntry, apply bool) (bool, error) {
	if m.Platform != "skeeto" || !strings.EqualFold(filepath.ToSlash(f.Rel), skeetoAddonListRel) || !exists(f.Target) {
		return false, nil
	}
	h, e := fileHash(f.Target)
	if e != nil {
		return true, e
	}
	if h == f.SourceSHA256 {
		return false, nil
	}
	b, e := os.ReadFile(f.Target)
	if e != nil {
		return true, e
	}
	original := ""
	if f.Existed {
		data, e := os.ReadFile(f.Backup)
		if e != nil {
			return true, e
		}
		original = string(data)
	}
	text, e := editSkeetoAddonList(string(b), &original)
	if e != nil {
		return true, e
	}
	if !apply {
		return true, nil
	}
	return true, writeFileAtomic(f.Target, []byte(text), 0644)
}
