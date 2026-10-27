//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"l4nfix/internal/pathguard"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const platformBackupName = ".platform_auto_backup"
const skeetoNavBegin = "// BEGIN L4NFix Skeeto navigation"
const skeetoNavEnd = "// END L4NFix Skeeto navigation"

// Accept the known installation layout; fail closed when a future package
// changes loader architecture. Never install Evo's alternate winmm/DXVK entry.
func platformMappings(kind, root string, files []string) ([]packageMapping, error) {
	required := []string{"skeeto.dll", "bin/wininet.dll", "Skeeto/Manifest.json", "Skeeto/Commands.txt"}
	if kind == "evolved" {
		required = []string{"bin/neko/plugins/L4D2Evolved.dll", "bin/neko/plugins/L4d2evomodel.dll", "Evolved/config.sample"}
	}
	byPath := map[string]string{}
	for _, f := range files {
		key := strings.ToLower(filepath.ToSlash(f))
		if _, ok := byPath[key]; ok {
			return nil, fmt.Errorf("重复平台路径: %s", f)
		}
		byPath[key] = f
	}
	for _, f := range required {
		actual, ok := byPath[strings.ToLower(f)]
		if !ok {
			return nil, fmt.Errorf("%s 安装布局不受支持，缺少 %s", kind, f)
		}
		if strings.HasSuffix(f, ".dll") && !isX86DLL(filepath.Join(root, actual)) {
			return nil, fmt.Errorf("平台 DLL 不是 x86: %s", f)
		}
	}
	if kind == "skeeto" {
		b, e := os.ReadFile(filepath.Join(root, byPath["skeeto/manifest.json"]))
		if e != nil {
			return nil, e
		}
		var m struct {
			Layout  int  `json:"install_layout"`
			Migrate bool `json:"requires_manual_migrate"`
		}
		if json.Unmarshal(b, &m) != nil || m.Layout != 2 || m.Migrate {
			return nil, fmt.Errorf("Skeeto 需要单独迁移或布局审核")
		}
		b, e = os.ReadFile(filepath.Join(root, byPath["skeeto/commands.txt"]))
		if e != nil || !strings.Contains(string(b), "skeeto_nav [0|1]") {
			return nil, fmt.Errorf("Skeeto 导航命令版本待核对")
		}
	} else {
		b, e := os.ReadFile(filepath.Join(root, byPath["evolved/config.sample"]))
		if e != nil || !strings.Contains(string(b), "NoHmdMinimapEnabled") || !strings.Contains(string(b), "NoHmdWorldNavigationEnabled") {
			return nil, fmt.Errorf("Evolved 导航配置版本待核对")
		}
	}
	var out []packageMapping
	for key, f := range byPath {
		allow := platformOwnedRelative(kind, key) && key != "left4dead2/cfg/autoexec.cfg"
		if kind == "skeeto" && (key == skeetoAddonListRel || isSkeetoModRelative(key)) {
			allow = false
		}
		if allow {
			out = append(out, packageMapping{Source: filepath.Join(root, f), Relative: f})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Relative < out[j].Relative })
	return out, nil
}

func platformOwnedRelative(kind, relative string) bool {
	key := strings.ToLower(filepath.ToSlash(relative))
	if kind == "skeeto" {
		return key == "skeeto.dll" || key == "bin/wininet.dll" || key == "version.txt" || key == "skeeto_workshop.txt" || key == "skeeto_apply_update.bat" || key == "left4dead2/cfg/autoexec.cfg" || key == skeetoAddonListRel || isSkeetoModRelative(key) || strings.HasPrefix(key, "skeeto/")
	}
	if kind == "evolved" {
		return key == "bin/neko/plugins/l4d2evolved.dll" || key == "bin/neko/plugins/l4d2evomodel.dll" || (strings.HasPrefix(key, "evolved/") && key != "evolved/d3d9.dll") || strings.HasPrefix(key, "left4dead2/addons/") || strings.HasPrefix(key, "left4dead2/sound/vrmod/")
	}
	return false
}

func ensureBaseDeployed(exeRoot, game string) (*manifest, error) {
	backup := filepath.Join(exeRoot, ".l4n_auto_backup")
	if !exists(filepath.Join(backup, "manifest.json")) {
		return nil, fmt.Errorf("请先部署 L4N＋DXVK")
	}
	m, e := checkedInstallManifest(backup, game)
	if e != nil {
		return nil, e
	}
	for _, rel := range []string{"bin/left4neko.dll", "dxgi.dll", "dxvk_d3d9.dll", "bin/dxvk_d3d9.dll"} {
		found := false
		for _, f := range m.Files {
			if strings.EqualFold(filepath.ToSlash(f.Rel), rel) && f.SourceSHA256 != "" {
				h, err := fileHash(f.Target)
				if err != nil || h != f.SourceSHA256 {
					return nil, fmt.Errorf("基础部署文件变化，请先修复 L4N＋DXVK: %s", rel)
				}
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("L4N＋DXVK 部署记录不完整: %s", rel)
		}
	}
	return m, nil
}

func platformInstalled(exeRoot string) bool {
	return exists(filepath.Join(exeRoot, platformBackupName, "manifest.json"))
}
func readPlatformManifest(exeRoot string) (*manifest, error) {
	p := filepath.Join(exeRoot, platformBackupName, "manifest.json")
	if e := pathguard.Within(exeRoot, p); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var m manifest
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	return &m, nil
}
func archiveConfigBeforeRestore(backupRoot string, m *manifest) (string, error) {
	if m.Platform != "" {
		return "", nil
	}
	return archiveL4nConfigIfChanged(m.GameRoot, resourceRoot(filepath.Dir(backupRoot)), "", m)
}
func rejectUnmanagedPlatforms(game string) error {
	for _, rel := range []string{"skeeto.dll", "bin/neko/plugins/skeeto.dll", "bin/neko/plugins/L4D2Evolved.dll", "bin/neko/plugins/L4d2evomodel.dll"} {
		if exists(filepath.Join(game, filepath.FromSlash(rel))) {
			return fmt.Errorf("检测到现有平台文件 %s，请先用原安装方式清理/迁移；本工具保留未托管文件", rel)
		}
	}
	return nil
}
func evolvedNavigationConfig(b []byte) []byte {
	// Preserve unrelated settings and comments; remove all active duplicates.
	text := strings.TrimPrefix(string(b), "\xef\xbb\xbf")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for _, l := range lines {
		k, _, ok := strings.Cut(strings.TrimSpace(l), "=")
		k = strings.TrimSpace(k)
		if ok && (strings.EqualFold(k, "NoHmdMinimapEnabled") || strings.EqualFold(k, "NoHmdWorldNavigationEnabled")) {
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.TrimRight(strings.Join(out, "\r\n"), "\r\n") + "\r\nNoHmdMinimapEnabled=true\r\nNoHmdWorldNavigationEnabled=true\r\n")
}

func restoreModifiedPlatformAutoexec(m *manifest, f fileEntry, apply bool) (bool, error) {
	if m.Platform != "skeeto" || !strings.EqualFold(filepath.ToSlash(f.Rel), "left4dead2/cfg/autoexec.cfg") || !exists(f.Target) {
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
	s := string(b)
	a, z := strings.Index(s, skeetoNavBegin), strings.Index(s, skeetoNavEnd)
	if a < 0 && z < 0 {
		return true, nil
	} // User already removed our block.
	if a < 0 || z < a || strings.Count(s, skeetoNavBegin) != 1 || strings.Count(s, skeetoNavEnd) != 1 {
		return true, fmt.Errorf("autoexec 导航块不完整，请核对留档后重试")
	}
	for _, marker := range []struct {
		start int
		text  string
	}{{a, skeetoNavBegin}, {z, skeetoNavEnd}} {
		end := marker.start + len(marker.text)
		if (marker.start > 0 && s[marker.start-1] != '\n') || (end < len(s) && s[end] != '\r' && s[end] != '\n') {
			return true, fmt.Errorf("autoexec 导航标记必须独占一行，请核对留档后重试")
		}
	}
	end := z + len(skeetoNavEnd)
	if strings.HasPrefix(s[end:], "\r\n") {
		end += 2
	} else if strings.HasPrefix(s[end:], "\n") {
		end++
	}
	if a >= 2 && s[a-2:a] == "\r\n" {
		a -= 2
	}
	if !apply {
		return true, nil
	}
	return true, writeFileAtomic(f.Target, []byte(s[:a]+s[end:]), 0644)
}
func platformNavigationFiles(kind, stage, game string) ([]patchFile, error) {
	var files []patchFile
	add := func(rel string, b []byte) error {
		p := filepath.Join(stage, "navigation", filepath.FromSlash(rel))
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		if e := os.WriteFile(p, b, 0644); e != nil {
			return e
		}
		files = append(files, patchFile{Src: p, Rel: filepath.FromSlash(rel)})
		return nil
	}
	if kind == "skeeto" {
		rel := "left4dead2/cfg/autoexec.cfg"
		p := filepath.Join(game, filepath.FromSlash(rel))
		if e := pathguard.Within(game, p); e != nil {
			return nil, e
		}
		b, e := os.ReadFile(p)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if strings.Contains(string(b), skeetoNavBegin) || strings.Contains(string(b), skeetoNavEnd) {
			return nil, fmt.Errorf("autoexec 已有本工具导航块，请先清理旧部署")
		}
		b = append(b, []byte("\r\n"+skeetoNavBegin+"\r\nskeeto_nav 1\r\n"+skeetoNavEnd+"\r\n")...)
		if e = add(rel, b); e != nil {
			return nil, e
		}
	} else {
		for _, rel := range []string{"Evolved/config.txt", "Evolved/config2.txt"} {
			p := filepath.Join(game, filepath.FromSlash(rel))
			if e := pathguard.Within(game, p); e != nil {
				return nil, e
			}
			b, e := os.ReadFile(p)
			if e != nil && !os.IsNotExist(e) {
				return nil, e
			}
			if os.IsNotExist(e) && strings.HasSuffix(rel, "config2.txt") {
				continue
			}
			if e = add(rel, evolvedNavigationConfig(b)); e != nil {
				return nil, e
			}
		}
	}
	return files, nil
}

func deployPlatform(exeRoot, res, game, kind, version string) (err error) {
	release, e := acquireOperationLock(game, ".l4n-game-operation.lock")
	if e != nil {
		return e
	}
	defer release()
	return deployPlatformLocked(exeRoot, res, game, kind, version)
}
func deployPlatformLocked(exeRoot, res, game, kind, version string) (err error) {
	if kind != "skeeto" && kind != "evolved" {
		return fmt.Errorf("请选择 Skeeto 或 L4D2Evolved")
	}
	base, e := ensureBaseDeployed(exeRoot, game)
	if e != nil {
		return e
	}
	if platformInstalled(exeRoot) {
		return fmt.Errorf("已有平台部署，请先点击清理平台；二选一互斥")
	}
	if e = rejectUnmanagedPlatforms(game); e != nil {
		return e
	}
	if kind == "skeeto" && exists(filepath.Join(game, "bin", "wininet.dll")) {
		return fmt.Errorf("bin/wininet.dll 已存在，请先核对原代理 DLL；保留现有文件")
	}
	record, archive, e := selectDeployablePlatformResource(res, kind, version)
	if e != nil {
		return e
	}
	source, e := preparePackage(archive, res, kind)
	if e != nil {
		return e
	}
	defer os.RemoveAll(source.TemporaryDir)
	var files []patchFile
	for _, m := range source.Files {
		files = append(files, patchFile{Src: filepath.Join(source.NormalizedDir, m.Relative), Rel: m.Relative})
	}
	nav, e := platformNavigationFiles(kind, source.TemporaryDir, game)
	if e != nil {
		return e
	}
	files = append(files, nav...)
	if kind == "skeeto" {
		mods, e := skeetoPreparedModFiles(res, source.TemporaryDir, game, record)
		if e != nil {
			return e
		}
		files = append(files, mods...)
	}
	// No enhancement package may take ownership of files in the base manifest.
	seen := map[string]bool{}
	for _, f := range files {
		k := strings.ToLower(filepath.Clean(f.Rel))
		if seen[k] {
			return fmt.Errorf("平台目标重复: %s", f.Rel)
		}
		seen[k] = true
		if manifestHasFile(base, filepath.Join(game, f.Rel)) {
			return fmt.Errorf("平台与基础部署目标重叠: %s", f.Rel)
		}
		if e = pathguard.Within(game, filepath.Join(game, f.Rel)); e != nil {
			return e
		}
	}
	backup := filepath.Join(exeRoot, platformBackupName)
	m, e := checkedInstallManifest(backup, game)
	if e != nil {
		return e
	}
	m.Platform = kind
	m.PlatformVersion = record.Version
	m.PackageSources = []packageSource{source}
	m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if e = saveManifest(m, backup); e != nil {
		return e
	}
	defer func() {
		if err != nil {
			if rollbackErr := restorePlatformLocked(exeRoot, game, false); rollbackErr != nil {
				err = fmt.Errorf("%w；回滚待重试: %v", err, rollbackErr)
			}
		}
	}()
	for i, f := range files {
		if e = copyWithBackup(m, backup, game, f.Src, filepath.Join(game, f.Rel)); e != nil {
			return e
		}
		setProgress(10 + (i+1)*80/max(1, len(files)))
	}
	appendLog("[platform] " + kind + " " + record.Version + " 部署完成，小地图/导航已写为开启；请进图验证")
	if kind == "skeeto" {
		appendLog("[platform] 两个原始 VPK 已从缓存预置到 addons/workshop，addonlist 仅启用对应两项；Steam 仍可能校验或同步")
	}
	return nil
}

func restorePlatform(exeRoot, game string, protectEdits bool) error {
	release, e := acquireOperationLock(game, ".l4n-game-operation.lock")
	if e != nil {
		return e
	}
	defer release()
	return restorePlatformLocked(exeRoot, game, protectEdits)
}
func restorePlatformLocked(exeRoot, game string, protectEdits bool) error {
	backup := filepath.Join(exeRoot, platformBackupName)
	if !exists(filepath.Join(backup, "manifest.json")) {
		return fmt.Errorf("未找到本工具的平台部署记录，无需选择平台版本")
	}
	m, e := checkedInstallManifest(backup, game)
	if e != nil {
		return e
	}
	if m.Platform != "skeeto" && m.Platform != "evolved" {
		return fmt.Errorf("平台部署清单身份无效")
	}
	if len(m.SteamConfigs) != 0 {
		return fmt.Errorf("平台清单含基础部署项目，已停止清理")
	}
	base, e := checkedInstallManifest(filepath.Join(exeRoot, ".l4n_auto_backup"), game)
	if e != nil {
		return e
	}
	for _, f := range m.Files {
		if !platformOwnedRelative(m.Platform, f.Rel) {
			return fmt.Errorf("平台清单含非平台文件: %s", f.Rel)
		}
		if manifestHasFile(base, f.Target) {
			return fmt.Errorf("平台清单与基础部署重叠，已停止清理")
		}
		if protectEdits && exists(f.Target) {
			h, err := fileHash(f.Target)
			if err != nil {
				return err
			}
			if h != f.SourceSHA256 {
				// Preserve post-install updates/settings before restoring only the
				// files owned by our journal. Never clear an entire platform tree.
				archive := filepath.Join(resourceRoot(exeRoot), "platform_cleanup_backups", m.Platform, h, filepath.FromSlash(f.Rel))
				if err = pathguard.Within(resourceRoot(exeRoot), archive); err != nil {
					return err
				}
				if err = copyFile(f.Target, archive); err != nil {
					return err
				}
				actual, err := fileHash(archive)
				if err != nil {
					return err
				}
				if actual != h {
					return fmt.Errorf("清理前留档校验失败: %s", f.Rel)
				}
				appendLog("[platform] 清理前保留变化文件: " + archive)
			}
		}
	}
	return restoreFromBackup(backup)
}
