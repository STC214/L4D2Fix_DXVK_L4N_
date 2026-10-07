//go:build windows

package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var versionRefreshID atomic.Uint64

// Read the PE version resource via the system API; never load the game's DLL.
func embeddedL4nVersion(path string) string {
	lib := syscall.NewLazyDLL("version.dll")
	name, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return ""
	}
	size, _, _ := lib.NewProc("GetFileVersionInfoSizeW").Call(uintptr(unsafe.Pointer(name)), 0)
	if size == 0 || size > 1<<20 {
		return ""
	}
	buf := make([]byte, int(size))
	defer runtime.KeepAlive(buf)
	ok, _, _ := lib.NewProc("GetFileVersionInfoW").Call(uintptr(unsafe.Pointer(name)), 0, size, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return ""
	}
	query := func(key string) (unsafe.Pointer, uint32) {
		var ptr unsafe.Pointer
		var length uint32
		keyPtr, _ := syscall.UTF16PtrFromString(key)
		ok, _, _ := lib.NewProc("VerQueryValueW").Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(keyPtr)), uintptr(unsafe.Pointer(&ptr)), uintptr(unsafe.Pointer(&length)))
		if ok == 0 {
			return nil, 0
		}
		return ptr, length
	}
	translation, n := query(`\VarFileInfo\Translation`)
	if translation == nil || n < 4 {
		return ""
	}
	bytes := unsafe.Slice((*byte)(translation), int(n))
	validProduct := false
	for offset := 0; offset+4 <= len(bytes); offset += 4 {
		lang, code := binary.LittleEndian.Uint16(bytes[offset:]), binary.LittleEndian.Uint16(bytes[offset+2:])
		p, n := query(fmt.Sprintf(`\StringFileInfo\%04x%04x\ProductName`, lang, code))
		if p == nil || n == 0 || n > 1<<19 {
			continue
		}
		product := strings.ToLower(syscall.UTF16ToString(unsafe.Slice((*uint16)(p), int(n))))
		if product == "left for neko" || product == "left4neko" || product == "l4n" {
			validProduct = true
			break
		}
	}
	if !validProduct {
		return ""
	}
	p, n := query(`\`)
	if p == nil || n < 52 {
		return ""
	}
	fixed := unsafe.Slice((*byte)(p), 52)
	if binary.LittleEndian.Uint32(fixed) != 0xfeef04bd {
		return ""
	}
	ms, ls := binary.LittleEndian.Uint32(fixed[8:]), binary.LittleEndian.Uint32(fixed[12:])
	version := fmt.Sprintf("%d.%d.%d", ms>>16, ms&0xffff, ls>>16)
	if ls&0xffff != 0 {
		version += fmt.Sprintf(".%d", ls&0xffff)
	}
	if ms == 0 && ls == 0 {
		return ""
	}
	return version
}

type resourceIdentity struct {
	Name   string
	Hashes map[string]string
}

func identityPath(kind, name string) bool {
	name = strings.ToLower(filepath.ToSlash(name))
	if kind == "l4n" {
		return strings.HasSuffix("/"+name, "/left4neko.dll")
	}
	return strings.HasSuffix("/"+name, "/dxgi.dll") || strings.HasSuffix("/"+name, "/d3d9.dll") || strings.HasSuffix("/"+name, "/dxvk_d3d9.dll")
}

func resourceIdentityFor(source, kind string) (resourceIdentity, error) {
	id := resourceIdentity{Name: versionName(kind, filepath.Base(source)), Hashes: map[string]string{}}
	if strings.HasSuffix(strings.ToLower(source), ".zip") {
		z, e := zip.OpenReader(source)
		if e != nil {
			return id, e
		}
		defer z.Close()
		if err := validateZIPHeaders(z); err != nil {
			return id, err
		}
		for _, f := range z.File {
			name, e := zipResourceName(f)
			if e != nil {
				return id, e
			}
			if !identityPath(kind, name) || f.FileInfo().IsDir() {
				continue
			}
			rel, e := packageRelative(name)
			if e != nil {
				return id, e
			}
			key := strings.ToLower(filepath.ToSlash(rel))
			if _, ok := id.Hashes[key]; ok {
				return id, fmt.Errorf("资源身份文件重名")
			}
			if f.UncompressedSize64 > uint64(maxPackageBytes) {
				return id, fmt.Errorf("资源身份文件过大")
			}
			r, e := f.Open()
			if e != nil {
				return id, e
			}
			h := sha256.New()
			n, copyErr := io.Copy(h, io.LimitReader(r, int64(f.UncompressedSize64)+1))
			closeErr := r.Close()
			if copyErr != nil {
				return id, copyErr
			}
			if closeErr != nil {
				return id, closeErr
			}
			if n != int64(f.UncompressedSize64) {
				return id, fmt.Errorf("身份文件长度不符")
			}
			id.Hashes[key] = hex.EncodeToString(h.Sum(nil))
		}
		return id, nil
	}
	root := source
	if st, e := os.Stat(source); e != nil {
		return id, e
	} else if !st.IsDir() {
		tmp, e := os.MkdirTemp("", "l4n-version-")
		if e != nil {
			return id, e
		}
		defer os.RemoveAll(tmp)
		if e := extractPackage(source, tmp); e != nil {
			return id, e
		}
		root = tmp
	}
	files, e := packageInventory(root)
	if e != nil {
		return id, e
	}
	for _, rel := range files {
		if identityPath(kind, rel) {
			hash, e := fileHash(filepath.Join(root, rel))
			if e != nil {
				return id, e
			}
			id.Hashes[strings.ToLower(filepath.ToSlash(rel))] = hash
		}
	}
	return id, nil
}

func versionName(kind, name string) string {
	name, _ = archiveName(name)
	pattern := `(?i)^L4N_v(\d+\.\d+\.\d+)`
	if kind == "dxvk" {
		pattern = `(?i)^dxvk-(?:async-)?(\d+(?:\.\d+){1,2})`
	}
	if m := regexp.MustCompile(pattern).FindStringSubmatch(name); len(m) > 1 {
		if kind == "dxvk" && strings.Contains(strings.ToLower(name), "async") {
			return m[1] + " (async)"
		}
		return m[1]
	}
	return name
}

func identityMatches(kind string, hashes, installed map[string]string) bool {
	if kind == "l4n" {
		for name, hash := range hashes {
			if strings.HasSuffix("/"+strings.ToLower(filepath.ToSlash(name)), "/left4neko.dll") && hash == installed["bin/left4neko.dll"] {
				return true
			}
		}
		return false
	}
	for name, hash := range hashes {
		name = strings.ToLower(filepath.ToSlash(name))
		if !strings.HasSuffix("/"+name, "/dxgi.dll") || hash != installed["dxgi.dll"] {
			continue
		}
		parent := strings.TrimSuffix(name, "dxgi.dll")
		d9 := hashes[parent+"dxvk_d3d9.dll"]
		if d9 == "" {
			d9 = hashes[parent+"d3d9.dll"]
		}
		bin := hashes[parent+"bin/dxvk_d3d9.dll"]
		if bin == "" {
			bin = d9
		}
		if d9 != "" && d9 == installed["dxvk_d3d9.dll"] && bin == installed["bin/dxvk_d3d9.dll"] {
			return true
		}
	}
	return false
}

func detectedVersion(kind string, installed map[string]string, manifestSources []packageSource, options []dxvkOption) string {
	present := false
	for key := range installed {
		if (kind == "l4n" && key == "bin/left4neko.dll") || (kind == "dxvk" && isDxvkTargetRel(key)) {
			present = true
		}
	}
	if !present {
		return "未使用"
	}
	// The recorded name is accepted only while the actual installed DLL hashes match.
	for i := len(manifestSources) - 1; i >= 0; i-- {
		s := manifestSources[i]
		if s.Kind != kind {
			continue
		}
		hashes := map[string]string{}
		for _, f := range s.Files {
			hashes[strings.ToLower(filepath.ToSlash(f.Relative))] = f.SHA256
		}
		if kind == "dxvk" {
			if hashes["x32/dxgi.dll"] == installed["dxgi.dll"] && installed["dxgi.dll"] != "" && hashes["x32/dxvk_d3d9.dll"] == installed["dxvk_d3d9.dll"] && installed["dxvk_d3d9.dll"] != "" && hashes["x32/bin/dxvk_d3d9.dll"] == installed["bin/dxvk_d3d9.dll"] && installed["bin/dxvk_d3d9.dll"] != "" {
				return versionName(kind, filepath.Base(s.Source))
			}
		} else if identityMatches(kind, hashes, installed) {
			return versionName(kind, filepath.Base(s.Source))
		}
	}
	names := map[string]bool{}
	for _, opt := range options {
		id, e := resourceIdentityFor(opt.Dir, kind)
		if e == nil && identityMatches(kind, id.Hashes, installed) {
			names[id.Name] = true
		}
	}
	var matches []string
	for n := range names {
		matches = append(matches, n)
	}
	sort.Strings(matches)
	if len(matches) == 1 {
		return matches[0]
	}
	if len(matches) > 1 {
		return strings.Join(matches, " / ") + "（多包匹配）"
	}
	return "检测到文件，版本未知"
}

func installedVersionText(gameRoot, resRoot, backupRoot string) (string, error) {
	installed := map[string]string{}
	for _, rel := range []string{"bin/left4neko.dll", "dxgi.dll", "dxvk_d3d9.dll", "bin/dxvk_d3d9.dll"} {
		h, e := fileHash(filepath.Join(gameRoot, filepath.FromSlash(rel)))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", e
		}
		installed[rel] = h
	}
	var sources []packageSource
	if data, e := os.ReadFile(filepath.Join(backupRoot, "manifest.json")); e == nil {
		var m manifest
		if json.Unmarshal(data, &m) == nil && strings.EqualFold(clean(m.GameRoot), clean(gameRoot)) {
			sources = m.PackageSources
		}
	}
	dxvk := detectedVersion("dxvk", installed, sources, discoverDxvkOptions("", resRoot))
	l4n := detectedVersion("l4n", installed, sources, discoverL4nOptions("", resRoot))
	if installed["bin/left4neko.dll"] != "" {
		if embedded := embeddedL4nVersion(filepath.Join(gameRoot, "bin", "left4neko.dll")); embedded != "" {
			l4n = embedded
		}
	}
	return "当前游戏：DXVK " + dxvk + "  |  L4N " + l4n, nil
}

func refreshInstalledVersions() {
	id := versionRefreshID.Add(1)
	invokeUI(func() {
		procSetWindowTextW.Call(installedVersionCtl, uintptr(unsafe.Pointer(utf16Ptr("当前游戏：正在检测版本…"))))
	})
	go func() {
		text := "当前游戏：未定位到游戏目录"
		if root, e := packageRoot(); e == nil {
			if exe, e := resolveGameExe(root); e == nil {
				if detected, e := installedVersionText(filepath.Dir(exe), resourceRoot(root), filepath.Join(root, ".l4n_auto_backup")); e == nil {
					text = detected
				} else {
					text = "当前游戏：版本检测失败（文件不可读）"
					appendLog("[version] " + e.Error())
				}
			}
		}
		if versionRefreshID.Load() != id {
			return
		}
		invokeUI(func() {
			if versionRefreshID.Load() == id {
				procSetWindowTextW.Call(installedVersionCtl, uintptr(unsafe.Pointer(utf16Ptr(text))))
			}
		})
	}()
}
