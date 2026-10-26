//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manualCleanupBackupName = "manual_cleanup_backups"

// Namespaces, not version numbers. Shared directories (bin, plugins, shaders,
// addons, sound/vrmod) are deliberately absent. They require exact evidence.
func cleanupPrivateKind(relative string) string {
	r := strings.ToLower(filepath.ToSlash(relative))
	for _, p := range []string{"skeeto/", "skeeto_update/", "skeeto_updates/"} {
		if strings.HasPrefix(r, p) {
			return "skeeto"
		}
	}
	for _, p := range []string{"evolved/", "l4d2evomodel/", "evo_professor_physics/", "bin/neko/plugins/evo_professor_physics/"} {
		if strings.HasPrefix(r, p) {
			return "evolved"
		}
	}
	for _, p := range []string{"left4dead2/neko/", "neko/", "left4dead2/materials/l4n/", "reshade-shaders/shaders/l4n/"} {
		if strings.HasPrefix(r, p) {
			return "l4n"
		}
	}
	if r == "bin/left4neko.dll" || r == "left4neko.dll" || r == "left4dead2/bin/game_shader_generic_neko" || r == "left4dead2/bin/game_shader_generic_neko.dll" {
		return "l4n"
	}
	if r == "skeeto.dll" || r == "bin/neko/plugins/skeeto.dll" || r == "skeeto.ini" || r == "skeeto_setting.ini" || r == "skeeto.log" || r == "skeeto_apply_update.bat" || r == "skeeto_workshop.txt" || r == "skeeto_ws_show_changelog.txt" || r == "skeeto_ws_changelog_shown.txt" || r == "left4dead2_skeeto.exe" || r == "start_skeeto.bat" || r == "start_skeeto_with_update.bat" {
		return "skeeto"
	}
	for _, prefix := range []string{"", "bin/", "bin/neko/plugins/"} {
		for _, name := range []string{"l4d2evolved.dll", "l4d2evolved.pdb", "l4d2evomodel.dll", "l4d2evomodel.pdb", "l4d2modelruntime.dll", "l4d2modelruntime.pdb", "evoprofessorprediction.exe", "l4d2meshcachehost.exe", "l4d2framegenerationhost.exe"} {
			if r == prefix+name {
				return "evolved"
			}
		}
	}
	return ""
}

type manualCleanupFile struct {
	Rel          string `json:"path"`
	Kind         string `json:"kind"`
	BeforeSHA256 string `json:"beforeSHA256"`
	AfterSHA256  string `json:"afterSHA256,omitempty"`
	Rewrite      bool   `json:"rewrite,omitempty"`
	After        []byte `json:"-"`
}
type manualCleanupPlan struct {
	GameRoot string              `json:"gameRoot"`
	Scope    string              `json:"scope"`
	Status   string              `json:"status"`
	Files    []manualCleanupFile `json:"files"`
	Kept     []string            `json:"kept,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
}

func cleanupHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Remove only whole, standalone navigation commands/markers. Mixed commands,
// binds and other user configuration survive even a half-written marker block.
func cleanupManualAutoexec(b []byte) []byte {
	bom := ""
	text := string(b)
	if strings.HasPrefix(text, "\xef\xbb\xbf") {
		bom = "\xef\xbb\xbf"
		text = strings.TrimPrefix(text, bom)
	}
	var out []string
	quoted := false
	for _, line := range strings.SplitAfter(text, "\n") {
		wasQuoted := quoted
		for i := 0; i < len(line); i++ {
			if !quoted && i+1 < len(line) && line[i:i+2] == "//" {
				break
			}
			if line[i] == '\\' && i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\') {
				i++
				continue
			}
			if line[i] == '"' {
				quoted = !quoted
			}
		}
		if wasQuoted {
			out = append(out, line)
			continue
		}
		s := strings.TrimSpace(line)
		if s == skeetoNavBegin || s == skeetoNavEnd {
			continue
		}
		code, _, _ := strings.Cut(s, "//")
		fields := strings.Fields(code)
		if (len(fields) == 2 && strings.EqualFold(fields[0], "skeeto_nav") && (fields[1] == "1" || fields[1] == "0")) || (len(fields) == 1 && strings.EqualFold(fields[0], "skeeto_nav_reload")) {
			continue
		}
		out = append(out, line)
	}
	return []byte(bom + strings.Join(out, ""))
}

func buildManualCleanupPlan(game, scope string, signatures []cleanupSignature) (*manualCleanupPlan, error) {
	if scope != "base" && scope != "platform" {
		return nil, fmt.Errorf("invalid cleanup scope")
	}
	// Validate before even looking at a target; no traversal or linked component
	// may be used as evidence or archived/deleted later.
	if !filepath.IsAbs(game) {
		return nil, fmt.Errorf("game path must be absolute")
	}
	if e := pathguard.Within(game, filepath.Join(game, "left4dead2.exe")); e != nil {
		return nil, e
	}
	rows, e := mergeCleanupSignatures(signatures)
	if e != nil {
		return nil, e
	}
	plan := &manualCleanupPlan{GameRoot: game, Scope: scope, Status: "planned"}
	// Candidate set is restricted to known paths and explicit private trees;
	// never walk the entire game or match wildcard DLL/VPK names.
	candidates := map[string]string{}
	add := func(rel string, kind string) {
		r := strings.ToLower(filepath.ToSlash(rel))
		if cleanupScopePath(scope, r) {
			candidates[r] = kind
		}
	}
	for _, r := range rows {
		if cleanupKindAllowed(scope, r.Kind) {
			add(r.Rel, r.Kind)
		}
	}
	privateRoots := []string{"left4dead2/neko", "neko", "left4dead2/materials/l4n", "reshade-shaders/Shaders/L4N"}
	privateFiles := []string{"bin/left4neko.dll", "left4neko.dll", "left4dead2/bin/game_shader_generic_neko", "left4dead2/bin/game_shader_generic_neko.dll"}
	if scope == "platform" {
		privateRoots = []string{"Skeeto", "skeeto_update", "skeeto_updates", "Evolved", "L4d2evomodel", "evo_professor_physics", "bin/neko/plugins/evo_professor_physics"}
		privateFiles = []string{"skeeto.dll", "skeeto.ini", "skeeto_setting.ini", "skeeto.log", "bin/neko/plugins/skeeto.dll", "skeeto_apply_update.bat", "skeeto_workshop.txt", "skeeto_ws_show_changelog.txt", "skeeto_ws_changelog_shown.txt", "left4dead2_skeeto.exe", "start_skeeto.bat", "start_skeeto_with_update.bat"}
		for _, prefix := range []string{"", "bin/", "bin/neko/plugins/"} {
			for _, name := range []string{"L4D2Evolved.dll", "L4D2Evolved.pdb", "L4d2evomodel.dll", "L4d2evomodel.pdb", "L4D2ModelRuntime.dll", "L4D2ModelRuntime.pdb", "EvoProfessorPrediction.exe", "L4D2MeshCacheHost.exe", "L4D2FrameGenerationHost.exe"} {
				privateFiles = append(privateFiles, prefix+name)
			}
		}
		add("left4dead2/cfg/autoexec.cfg", "skeeto")
	}
	for _, r := range privateFiles {
		add(r, cleanupPrivateKind(r))
	}
	for _, rel := range privateRoots {
		p := filepath.Join(game, filepath.FromSlash(rel))
		if e := pathguard.Within(game, p); e != nil {
			return nil, e
		}
		st, e := os.Stat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("expected private directory: %s", rel)
		}
		files, e := packageInventory(p)
		if e != nil {
			return nil, e
		}
		for _, f := range files {
			r := filepath.ToSlash(filepath.Join(rel, f))
			add(r, cleanupPrivateKind(r))
		}
	}
	known := map[string]map[string]string{}
	for _, r := range rows {
		if cleanupKindAllowed(scope, r.Kind) {
			if known[r.Rel] == nil {
				known[r.Rel] = map[string]string{}
			}
			known[r.Rel][r.SHA256] = r.Kind
		}
	}
	var total int64
	for rel, kind := range candidates {
		p := filepath.Join(game, filepath.FromSlash(rel))
		if e := pathguard.Within(game, p); e != nil {
			return nil, e
		}
		st, e := os.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("cleanup target is not a regular file: %s", rel)
		}
		total += st.Size()
		if len(plan.Files) >= maxPackageFiles || total > maxPackageBytes {
			return nil, fmt.Errorf("cleanup inventory exceeds limit")
		}
		h, e := fileHash(p)
		if e != nil {
			return nil, e
		}
		f := manualCleanupFile{Rel: rel, Kind: kind, BeforeSHA256: h}
		if rel == "left4dead2/cfg/autoexec.cfg" {
			b, e := os.ReadFile(p)
			if e != nil {
				return nil, e
			}
			after := cleanupManualAutoexec(b)
			for _, line := range strings.Split(string(after), "\n") {
				code, _, _ := strings.Cut(line, "//")
				if strings.Contains(strings.ToLower(code), "skeeto_") {
					plan.Kept = append(plan.Kept, rel+"（混合命令或自定义绑定已保留，请手动核对）")
					break
				}
			}
			if string(b) == string(after) {
				continue
			}
			f.Rewrite = true
			f.After = after
			f.AfterSHA256 = cleanupHash(after)
		} else if owned := cleanupPrivateKind(rel); cleanupKindAllowed(scope, owned) {
			f.Kind = owned
		} else if match := known[rel][h]; match != "" {
			f.Kind = match
		} else {
			plan.Kept = append(plan.Kept, rel+"（内容未匹配已知资源，已保留）")
			continue
		}
		plan.Files = append(plan.Files, f)
	}
	sort.Slice(plan.Files, func(i, j int) bool {
		// Keep the detectable game entry until all other removals succeeded.
		if plan.Files[i].Rel == "left4dead2.exe" {
			return false
		}
		if plan.Files[j].Rel == "left4dead2.exe" {
			return true
		}
		return plan.Files[i].Rel < plan.Files[j].Rel
	})
	sort.Strings(plan.Kept)
	return plan, nil
}

func validateManualCleanupPlan(plan *manualCleanupPlan) error {
	if plan.Scope != "base" && plan.Scope != "platform" {
		return fmt.Errorf("invalid cleanup scope")
	}
	seen := map[string]bool{}
	for _, f := range plan.Files {
		before, e := hex.DecodeString(f.BeforeSHA256)
		if e != nil || len(before) != 32 {
			return fmt.Errorf("invalid cleanup snapshot hash")
		}
		if f.Rewrite && (f.Rel != "left4dead2/cfg/autoexec.cfg" || f.Kind != "skeeto" || cleanupHash(f.After) != f.AfterSHA256) {
			return fmt.Errorf("invalid cleanup rewrite snapshot")
		}
		r, e := packageRelative(f.Rel)
		if e != nil || r == "" {
			return fmt.Errorf("invalid cleanup target: %s", f.Rel)
		}
		key := strings.ToLower(filepath.ToSlash(r))
		if seen[key] || !cleanupScopePath(plan.Scope, key) || !cleanupKindAllowed(plan.Scope, f.Kind) {
			return fmt.Errorf("cleanup ownership conflict: %s", key)
		}
		seen[key] = true
		if e := pathguard.Within(plan.GameRoot, filepath.Join(plan.GameRoot, r)); e != nil {
			return e
		}
	}
	return nil
}

// Full-copy quarantine first, then hash-recheck and remove individual files.
// A copy/journal error changes no game file. Failed removal leaves the journal
// and original content for retry/manual restoration; no recursive delete.
func applyManualCleanup(res string, plan *manualCleanupPlan) (string, error) {
	if e := validateManualCleanupPlan(plan); e != nil {
		return "", e
	}
	if len(plan.Files) == 0 {
		return "", nil
	}
	parent := filepath.Join(res, manualCleanupBackupName)
	if e := pathguard.Within(res, filepath.Join(parent, "probe")); e != nil {
		return "", e
	}
	if e := os.MkdirAll(parent, 0755); e != nil {
		return "", e
	}
	backup, e := os.MkdirTemp(parent, time.Now().UTC().Format("20060102T150405")+"-")
	if e != nil {
		return "", e
	}
	writeJournal := func(status string) error {
		plan.Status = status
		b, e := json.MarshalIndent(plan, "", "  ")
		if e != nil {
			return e
		}
		return writeFileAtomic(filepath.Join(backup, "manifest.json"), b, 0644)
	}
	if e := writeJournal("copying"); e != nil {
		return backup, e
	}
	for _, f := range plan.Files {
		src := filepath.Join(plan.GameRoot, filepath.FromSlash(f.Rel))
		dst := filepath.Join(backup, "files", filepath.FromSlash(f.Rel))
		if e := pathguard.Within(plan.GameRoot, src); e != nil {
			return backup, e
		}
		if e := pathguard.Within(backup, dst); e != nil {
			return backup, e
		}
		h, e := fileHash(src)
		if e != nil || h != f.BeforeSHA256 {
			return backup, fmt.Errorf("cleanup target changed before backup: %s", f.Rel)
		}
		if e := copyFile(src, dst); e != nil {
			return backup, e
		}
		h, e = fileHash(dst)
		if e != nil || h != f.BeforeSHA256 {
			return backup, fmt.Errorf("cleanup backup mismatch: %s", f.Rel)
		}
	}
	if e := writeManualCleanupRollback(backup); e != nil {
		return backup, e
	}
	if e := writeJournal("prepared"); e != nil {
		return backup, e
	}
	// Revalidate the complete snapshot before the first removal.
	for _, f := range plan.Files {
		p := filepath.Join(plan.GameRoot, filepath.FromSlash(f.Rel))
		if e := pathguard.Within(plan.GameRoot, p); e != nil {
			return backup, e
		}
		h, e := fileHash(p)
		if e != nil || h != f.BeforeSHA256 {
			return backup, fmt.Errorf("cleanup target changed: %s", f.Rel)
		}
	}
	for _, f := range plan.Files {
		p := filepath.Join(plan.GameRoot, filepath.FromSlash(f.Rel))
		if e := pathguard.Within(plan.GameRoot, p); e != nil {
			return backup, e
		}
		h, e := fileHash(p)
		if e != nil || h != f.BeforeSHA256 {
			return backup, fmt.Errorf("cleanup target changed during removal: %s", f.Rel)
		}
		if f.Rewrite {
			if cleanupHash(f.After) != f.AfterSHA256 {
				return backup, fmt.Errorf("rewrite snapshot mismatch")
			}
			e = writeFileAtomic(p, f.After, 0644)
		} else {
			e = os.Remove(p)
		}
		if e != nil {
			_ = writeJournal("partial")
			return backup, fmt.Errorf("cleanup incomplete; backups retained at %s: %w", backup, e)
		}
		if f.Rewrite {
			h, e = fileHash(p)
			if e != nil || h != f.AfterSHA256 {
				return backup, fmt.Errorf("rewrite verification failed: %s", f.Rel)
			}
		} else if _, e = os.Lstat(p); !os.IsNotExist(e) {
			return backup, fmt.Errorf("removal verification failed: %s", f.Rel)
		}
		appendLog("[manual-clean] " + f.Rel)
	}
	// Only now prune empty parents; never remove a directory containing other mods.
	for _, f := range plan.Files {
		if !f.Rewrite {
			removeEmptyParents(filepath.Dir(filepath.Join(plan.GameRoot, filepath.FromSlash(f.Rel))), plan.GameRoot)
		}
	}
	return backup, writeJournal("complete")
}

func writeManualCleanupRollback(backup string) error {
	// This script restores archived files only. It rejects linked components,
	// refuses to overwrite subsequent edits, and never executes archived content.
	const script = `param([switch]$Force)
$ErrorActionPreference='Stop'
$m=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'manifest.json') -Raw -Encoding UTF8 | ConvertFrom-Json
$root=[IO.Path]::GetFullPath($m.gameRoot).TrimEnd('\')
function CheckPath([string]$base,[string]$path) {
 $base=[IO.Path]::GetFullPath($base).TrimEnd('\');$path=[IO.Path]::GetFullPath($path)
 if(-not $path.StartsWith($base+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'Rollback path outside root'}
 $cursor=$path
 while($cursor -and $cursor.Length -ge $base.Length){if(Test-Path -LiteralPath $cursor){if((Get-Item -LiteralPath $cursor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Rollback rejects links'}};$cursor=[IO.Path]::GetDirectoryName($cursor)}
}
if(Get-Process left4dead2,left4dead2_skeeto,left4dead2_necola -ErrorAction SilentlyContinue){throw 'Exit the game before rollback'}
foreach($f in $m.files){
 $source=Join-Path (Join-Path $PSScriptRoot 'files') $f.path;$target=Join-Path $root $f.path
 CheckPath $PSScriptRoot $source;CheckPath $root $target
 if((Get-FileHash -LiteralPath $source).Hash -ne $f.beforeSHA256){throw 'Rollback backup checksum mismatch'}
 if((Test-Path -LiteralPath $target) -and -not $Force){$h=(Get-FileHash -LiteralPath $target).Hash;if($h -ne $f.beforeSHA256 -and $h -ne $f.afterSHA256){throw 'Target edited after cleanup; review before using -Force'}}
}
foreach($f in $m.files){$source=Join-Path (Join-Path $PSScriptRoot 'files') $f.path;$target=Join-Path $root $f.path;CheckPath $PSScriptRoot $source;CheckPath $root $target;New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($target)) -Force | Out-Null;Copy-Item -LiteralPath $source -Destination $target -Force;if((Get-FileHash -LiteralPath $target).Hash -ne $f.beforeSHA256){throw 'Rollback verification failed'}}
Write-Output 'Rollback PASS: archived files restored and SHA256 verified.'
`
	return os.WriteFile(filepath.Join(backup, "rollback.ps1"), []byte(script), 0600)
}
