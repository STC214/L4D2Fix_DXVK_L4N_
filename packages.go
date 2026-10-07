//go:build windows

package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxPackageBytes int64 = 2 << 30
const maxPackageFiles = 20000

type packageSource struct {
	Kind          string           `json:"kind"`
	Source        string           `json:"source"`
	SHA256        string           `json:"sha256,omitempty"`
	TemporaryDir  string           `json:"temporaryDir"`
	NormalizedDir string           `json:"normalizedDir"`
	PreparedAt    string           `json:"preparedAt"`
	Files         []packageMapping `json:"files"`
}
type packageMapping struct {
	Source   string `json:"source"`
	Relative string `json:"relative"`
	SHA256   string `json:"sha256"`
}

func archiveName(name string) (string, bool) {
	for _, ext := range []string{".tar.gz", ".tgz", ".zip", ".tar"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return name[:len(name)-len(ext)], true
		}
	}
	return name, false
}

// Archive paths use Windows rules even when their separators come from Unix.
func packageRelative(name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("非法资源路径: %q", name)
	}
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	for _, p := range parts {
		if p == "." {
			continue
		}
		if p == "" || p == ".." || strings.ContainsAny(p, "\x00<>\"|?*") || strings.TrimRight(p, " .") != p {
			return "", fmt.Errorf("非法资源路径: %q", name)
		}
		base := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return "", fmt.Errorf("保留文件名: %q", name)
		}
	}
	rel := filepath.Clean(filepath.FromSlash(name))
	if rel == "." {
		return "", nil
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("资源路径越界: %q", name)
	}
	return rel, nil
}

type extractionBudget struct {
	bytes int64
	files int
	seen  map[string]bool
}

func (b *extractionBudget) write(root, name string, size int64, dir bool, r io.Reader) error {
	rel, err := packageRelative(name)
	if err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	if size < 0 || size > maxPackageBytes-b.bytes || b.files >= maxPackageFiles {
		return fmt.Errorf("压缩包超过解压限制")
	}
	key := strings.ToLower(rel)
	if b.seen[key] {
		return fmt.Errorf("压缩包重复路径: %s", name)
	}
	b.seen[key] = true
	b.files++
	dst := filepath.Join(root, rel)
	if dir {
		return os.MkdirAll(dst, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, size+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return fmt.Errorf("解压文件长度不符: %s", name)
	}
	b.bytes += n
	return nil
}

func extractPackage(source, root string) error {
	b := extractionBudget{seen: map[string]bool{}}
	if strings.HasSuffix(strings.ToLower(source), ".zip") {
		z, err := zip.OpenReader(source)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
				return fmt.Errorf("压缩包含链接或特殊文件: %s", f.Name)
			}
			if f.UncompressedSize64 > uint64(maxPackageBytes) {
				return fmt.Errorf("压缩文件过大: %s", f.Name)
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = b.write(root, f.Name, int64(f.UncompressedSize64), f.FileInfo().IsDir(), r)
			closeErr := r.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	var reader io.Reader = f
	lower := strings.ToLower(source)
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		g, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer g.Close()
		reader = g
	}
	t := tar.NewReader(reader)
	for {
		h, err := t.Next()
		if err == io.EOF {
			// Drain gzip to verify its checksum; bound trailing archive padding.
			n, e := io.Copy(io.Discard, io.LimitReader(reader, 1<<20))
			if e != nil {
				return e
			}
			if n == 1<<20 {
				return fmt.Errorf("压缩包尾部数据超过限制")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && h.Typeflag != tar.TypeDir {
			return fmt.Errorf("压缩包含链接或特殊文件: %s", h.Name)
		}
		if err := b.write(root, h.Name, h.Size, h.Typeflag == tar.TypeDir, t); err != nil {
			return err
		}
	}
}

func packageInventory(root string) ([]string, error) {
	var files []string
	var total int64
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("资源包含链接: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxPackageBytes {
			return fmt.Errorf("资源内容超过大小限制")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("资源包含特殊文件: %s", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if _, err = packageRelative(rel); err != nil {
			return err
		}
		files = append(files, rel)
		if len(files) > maxPackageFiles {
			return fmt.Errorf("资源文件数量超过限制")
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}
func fileHash(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Read PE identity, never load DLLs. Reject x64 and renamed/non-PE files.
func isX86DLL(p string) bool {
	f, e := os.Open(p)
	if e != nil {
		return false
	}
	defer f.Close()
	header := make([]byte, 64)
	if _, e = io.ReadFull(f, header); e != nil || string(header[:2]) != "MZ" {
		return false
	}
	offset := int64(binary.LittleEndian.Uint32(header[60:64]))
	if offset < 64 {
		return false
	}
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return false
	}
	pe := make([]byte, 24)
	if _, e = io.ReadFull(f, pe); e != nil {
		return false
	}
	return string(pe[:4]) == "PE\x00\x00" && binary.LittleEndian.Uint16(pe[4:6]) == 0x14c && binary.LittleEndian.Uint16(pe[22:24])&0x2000 != 0
}

func normalizedMappings(kind, root string) ([]packageMapping, error) {
	files, err := packageInventory(root)
	if err != nil {
		return nil, err
	}
	byPath := map[string]string{}
	for _, p := range files {
		key := strings.ToLower(filepath.ToSlash(p))
		if _, ok := byPath[key]; ok {
			return nil, fmt.Errorf("资源重名: %s", p)
		}
		byPath[key] = p
	}
	var mappings []packageMapping
	add := func(src, rel string) {
		mappings = append(mappings, packageMapping{Source: filepath.Join(root, src), Relative: filepath.FromSlash(rel)})
	}
	if kind == "dxvk" {
		var candidates []string
		for key, p := range byPath {
			if filepath.Base(key) != "dxgi.dll" {
				continue
			}
			parent := filepath.ToSlash(filepath.Dir(key))
			if parent == "." {
				parent = ""
			} else {
				parent += "/"
			}
			d9, ok := byPath[parent+"dxvk_d3d9.dll"]
			if !ok {
				d9, ok = byPath[parent+"d3d9.dll"]
			}
			if !ok || !isX86DLL(filepath.Join(root, p)) || !isX86DLL(filepath.Join(root, d9)) {
				continue
			}
			candidates = append(candidates, parent)
		}
		if len(candidates) != 1 {
			return nil, fmt.Errorf("DXVK 需要唯一一组 32 位 dxgi.dll 和 d3d9.dll/dxvk_d3d9.dll，找到 %d 组", len(candidates))
		}
		parent := candidates[0]
		d9 := byPath[parent+"dxvk_d3d9.dll"]
		if d9 == "" {
			d9 = byPath[parent+"d3d9.dll"]
		}
		if alias := byPath[parent+"d3d9.dll"]; alias != "" && alias != d9 {
			a, e := fileHash(filepath.Join(root, alias))
			if e != nil {
				return nil, e
			}
			b, e := fileHash(filepath.Join(root, d9))
			if e != nil {
				return nil, e
			}
			if a != b {
				return nil, fmt.Errorf("d3d9.dll 与 dxvk_d3d9.dll 内容冲突")
			}
		}
		bin := byPath[parent+"bin/dxvk_d3d9.dll"]
		if bin == "" {
			bin = d9
		}
		if !isX86DLL(filepath.Join(root, bin)) {
			return nil, fmt.Errorf("bin/dxvk_d3d9.dll 不是 32 位 DLL")
		}
		add(byPath[parent+"dxgi.dll"], "x32/dxgi.dll")
		add(d9, "x32/dxvk_d3d9.dll")
		add(bin, "x32/bin/dxvk_d3d9.dll")
	} else if kind == "l4n" {
		var keys []string
		for k := range byPath {
			if filepath.Base(k) == "left4neko.dll" {
				keys = append(keys, k)
			}
		}
		if len(keys) != 1 {
			return nil, fmt.Errorf("L4N 需要唯一 left4neko.dll，找到 %d 个", len(keys))
		}
		key := keys[0]
		if !isX86DLL(filepath.Join(root, byPath[key])) {
			return nil, fmt.Errorf("left4neko.dll 不是 32 位 DLL")
		}
		parent := filepath.ToSlash(filepath.Dir(key))
		if strings.EqualFold(filepath.Base(parent), "bin") {
			parent = filepath.ToSlash(filepath.Dir(parent))
		}
		prefix := parent + "/"
		if parent == "." {
			prefix = ""
		}
		for k, p := range byPath {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			rel := strings.TrimPrefix(k, prefix)
			if rel == "left4neko.dll" {
				rel = "bin/left4neko.dll"
			} else if strings.HasPrefix(rel, "neko/") || strings.HasPrefix(rel, "shaders/") {
				rel = "left4dead2/" + rel
			}
			if rel == "d3d9.dll" {
				rel = "dxvk_d3d9.dll"
			}
			if rel == "bin/d3d9.dll" {
				rel = "bin/dxvk_d3d9.dll"
			}
			if isDxvkTargetRel(rel) {
				continue
			}
			add(p, rel)
		}
		foundConfig := false
		for _, m := range mappings {
			if filepath.ToSlash(m.Relative) == "left4dead2/neko/config.vdf" {
				foundConfig = true
			}
		}
		if !foundConfig {
			return nil, fmt.Errorf("L4N 缺少 left4dead2/neko/config.vdf 或 neko/config.vdf；目录层级有歧义时请整理资源包")
		}
	} else {
		return nil, fmt.Errorf("未知资源类型: %s", kind)
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].Relative < mappings[j].Relative })
	seen := map[string]bool{}
	for _, m := range mappings {
		rel, err := packageRelative(m.Relative)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return nil, fmt.Errorf("规范化目标冲突: %s", rel)
		}
		seen[key] = true
	}
	return mappings, nil
}

func preparePackage(source, resRoot, kind string) (info packageSource, err error) {
	source, err = filepath.Abs(source)
	if err != nil {
		return info, err
	}
	st, err := os.Lstat(source)
	if err != nil {
		return info, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return info, fmt.Errorf("资源源路径是链接")
	}
	resRoot, err = filepath.Abs(resRoot)
	if err != nil {
		return info, err
	}
	cache := filepath.Join(resRoot, ".package_tmp")
	if st, e := os.Lstat(cache); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return info, fmt.Errorf("临时资源目录必须是普通目录")
	} else if e != nil && !os.IsNotExist(e) {
		return info, e
	}
	if err = os.MkdirAll(cache, 0755); err != nil {
		return info, err
	}
	temp, err := os.MkdirTemp(cache, kind+"-")
	if err != nil {
		return info, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(temp)
		}
	}()
	info = packageSource{Kind: kind, Source: source, TemporaryDir: temp, NormalizedDir: filepath.Join(temp, "normalized"), PreparedAt: time.Now().Format(time.RFC3339)}
	input := source
	if !st.IsDir() {
		if _, ok := archiveName(source); !ok {
			return info, fmt.Errorf("支持 ZIP、TAR、TAR.GZ、TGZ 压缩包")
		}
		info.SHA256, err = fileHash(source)
		if err != nil {
			return info, err
		}
		input = filepath.Join(temp, "extracted")
		if err = os.MkdirAll(input, 0755); err != nil {
			return info, err
		}
		if err = extractPackage(source, input); err != nil {
			return info, err
		}
		after, e := fileHash(source)
		if e != nil {
			return info, e
		}
		if after != info.SHA256 {
			return info, fmt.Errorf("解压期间源压缩包发生变化")
		}
	}
	info.Files, err = normalizedMappings(kind, input)
	if err != nil {
		return info, err
	}
	for i := range info.Files {
		m := &info.Files[i]
		dst := filepath.Join(info.NormalizedDir, m.Relative)
		if err = copyFile(m.Source, dst); err != nil {
			return info, err
		}
		m.SHA256, err = fileHash(dst)
		if err != nil {
			return info, err
		}
		original, e := fileHash(m.Source)
		if e != nil {
			return info, e
		}
		if original != m.SHA256 {
			return info, fmt.Errorf("资源复制期间发生变化: %s", m.Source)
		}
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return info, err
	}
	err = os.WriteFile(filepath.Join(temp, "prepared.json"), data, 0644)
	return info, err
}

func discoverL4nOptions(root, resRoot string) []dxvkOption {
	var options []dxvkOption
	seen := map[string]bool{}
	for _, base := range packageSearchRoots(root) {
		for _, dir := range []string{base, filepath.Join(base, "L4N其他版本")} {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				name, archive := archiveName(e.Name())
				if !e.IsDir() && !archive {
					continue
				}
				if dir == base && !strings.Contains(strings.ToLower(name), "l4n") {
					continue
				}
				path := filepath.Join(dir, e.Name())
				if e.IsDir() {
					files, err := packageInventory(path)
					if err != nil {
						continue
					}
					found := false
					for _, f := range files {
						if strings.EqualFold(filepath.Base(f), "left4neko.dll") {
							found = true
							break
						}
					}
					if !found {
						continue
					}
				}
				key := strings.ToLower(e.Name())
				if seen[key] {
					continue
				}
				seen[key] = true
				options = append(options, dxvkOption{Name: e.Name(), Dir: path})
			}
		}
	}
	sort.Slice(options, func(i, j int) bool { return strings.ToLower(options[i].Name) < strings.ToLower(options[j].Name) })
	return options
}
