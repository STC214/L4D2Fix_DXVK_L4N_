//go:build windows

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDLL(machine uint16) []byte {
	b := make([]byte, 128)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[60:], 64)
	copy(b[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[68:], machine)
	binary.LittleEndian.PutUint16(b[86:], 0x2000)
	return b
}
func testFiles(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for p, b := range files {
		p = filepath.Join(root, filepath.FromSlash(p))
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, b, 0644); e != nil {
			t.Fatal(e)
		}
	}
}
func testZip(t *testing.T, p string, files map[string][]byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for name, b := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
}
func dxvkFixture(prefix string) map[string][]byte {
	return map[string][]byte{prefix + "dxgi.dll": testDLL(0x14c), prefix + "d3d9.dll": testDLL(0x14c)}
}
func l4nFixture() map[string][]byte {
	return map[string][]byte{"wrapper/L4N/left4neko.dll": testDLL(0x14c), "wrapper/L4N/neko/config.vdf": []byte("config"), "wrapper/L4N/shaders/test.vcs": []byte("shader"), "wrapper/L4N/readme_l4n.txt": []byte("readme")}
}

func TestZipDXVKNormalizeAndPreserve(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "dxvk-3.zip")
	testZip(t, p, dxvkFixture("wrap/dxvk-3/x32/"))
	before, _ := fileHash(p)
	info, e := preparePackage(p, filepath.Join(root, "resources"), "dxvk")
	if e != nil {
		t.Fatal(e)
	}
	after, _ := fileHash(p)
	if before != after || info.SHA256 != before {
		t.Fatal("archive changed")
	}
	files, e := collectDxvkPatchFiles(info.NormalizedDir)
	if e != nil || len(files) != 3 {
		t.Fatalf("mapping: %v %v", files, e)
	}
	for _, f := range files {
		if !isX86DLL(f.Src) {
			t.Fatal("bad normalized DLL")
		}
	}
	if !exists(filepath.Join(info.TemporaryDir, "prepared.json")) {
		t.Fatal("metadata missing")
	}
	t.Log("ZIP wrapped x32/d3d9.dll -> normalized x32 + 3 game mappings; archive unchanged")
}
func TestFlatDXVKDirectory(t *testing.T) {
	r := t.TempDir()
	testFiles(t, filepath.Join(r, "flat"), dxvkFixture(""))
	info, e := preparePackage(filepath.Join(r, "flat"), filepath.Join(r, "resources"), "dxvk")
	if e != nil {
		t.Fatal(e)
	}
	if !exists(filepath.Join(info.NormalizedDir, "x32", "dxvk_d3d9.dll")) {
		t.Fatal("flat mapping failed")
	}
	if !exists(filepath.Join(r, "flat", "d3d9.dll")) {
		t.Fatal("source changed")
	}
}
func TestL4NNormalize(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "L4N-new.zip")
	testZip(t, p, l4nFixture())
	info, e := preparePackage(p, filepath.Join(r, "resources"), "l4n")
	if e != nil {
		t.Fatal(e)
	}
	for _, rel := range []string{"bin/left4neko.dll", "left4dead2/neko/config.vdf", "left4dead2/shaders/test.vcs"} {
		if !exists(filepath.Join(info.NormalizedDir, rel)) {
			t.Fatal(rel)
		}
	}
	t.Log("wrapper + flat left4neko + neko/shaders normalized to game-relative tree")
}
func TestTarFormats(t *testing.T) {
	for _, ext := range []string{".tar", ".tar.gz", ".tgz"} {
		t.Run(ext, func(t *testing.T) {
			r := t.TempDir()
			p := filepath.Join(r, "dxvk"+ext)
			var buf bytes.Buffer
			var tw *tar.Writer
			var gz *gzip.Writer
			if ext == ".tar" {
				tw = tar.NewWriter(&buf)
			} else {
				gz = gzip.NewWriter(&buf)
				tw = tar.NewWriter(gz)
			}
			for n, b := range dxvkFixture("package/x32/") {
				if e := tw.WriteHeader(&tar.Header{Name: n, Mode: 0644, Size: int64(len(b))}); e != nil {
					t.Fatal(e)
				}
				if _, e := tw.Write(b); e != nil {
					t.Fatal(e)
				}
			}
			if e := tw.Close(); e != nil {
				t.Fatal(e)
			}
			if gz != nil {
				if e := gz.Close(); e != nil {
					t.Fatal(e)
				}
			}
			if e := os.WriteFile(p, buf.Bytes(), 0644); e != nil {
				t.Fatal(e)
			}
			if _, e := preparePackage(p, filepath.Join(r, "resources"), "dxvk"); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRejectInvalidDXVK(t *testing.T) {
	for _, kind := range []string{"missing", "x64", "nonPE", "multiple", "aliases"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			f := dxvkFixture("x32/")
			switch kind {
			case "missing":
				delete(f, "x32/d3d9.dll")
			case "x64":
				f = map[string][]byte{"x64/dxgi.dll": testDLL(0x8664), "x64/d3d9.dll": testDLL(0x8664)}
			case "nonPE":
				f["x32/d3d9.dll"] = []byte("not dll")
			case "multiple":
				for n, b := range dxvkFixture("second/x32/") {
					f[n] = b
				}
			case "aliases":
				b := testDLL(0x14c)
				b[100] = 1
				f["x32/dxvk_d3d9.dll"] = b
			}
			p := filepath.Join(r, "bad.zip")
			testZip(t, p, f)
			res := filepath.Join(r, "resources")
			if _, e := preparePackage(p, res, "dxvk"); e == nil {
				t.Fatal("invalid package accepted")
			}
			entries, e := os.ReadDir(filepath.Join(res, ".package_tmp"))
			if e != nil || len(entries) != 0 {
				t.Fatal("failed temporary tree not cleaned")
			}
		})
	}
}
func TestRejectInvalidL4N(t *testing.T) {
	for _, kind := range []string{"missingConfig", "multiple", "collision"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			f := l4nFixture()
			switch kind {
			case "missingConfig":
				delete(f, "wrapper/L4N/neko/config.vdf")
			case "multiple":
				f["other/bin/left4neko.dll"] = testDLL(0x14c)
			case "collision":
				f["wrapper/L4N/left4dead2/neko/config.vdf"] = []byte("other")
			}
			p := filepath.Join(r, "L4N.zip")
			testZip(t, p, f)
			if _, e := preparePackage(p, filepath.Join(r, "resources"), "l4n"); e == nil {
				t.Fatal("invalid L4N accepted")
			}
		})
	}
}
func TestArchivePathValidation(t *testing.T) {
	for _, name := range []string{"../outside.dll", `..\outside.dll`, "/absolute.dll", "C:/drive.dll", "file.dll:stream", "NUL.dll", "trailing. ", "a/../../x"} {
		t.Run(name, func(t *testing.T) {
			r := t.TempDir()
			p := filepath.Join(r, "bad.zip")
			testZip(t, p, map[string][]byte{name: []byte("bad")})
			if _, e := preparePackage(p, filepath.Join(r, "resources"), "dxvk"); e == nil {
				t.Fatal("unsafe path accepted")
			}
			if exists(filepath.Join(r, "outside.dll")) {
				t.Fatal("escaped extraction")
			}
		})
	}
}
func TestZipDuplicatesAndLinks(t *testing.T) {
	for _, kind := range []string{"caseDuplicate", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			p := filepath.Join(r, "bad.zip")
			f, e := os.Create(p)
			if e != nil {
				t.Fatal(e)
			}
			z := zip.NewWriter(f)
			if kind == "symlink" {
				h := &zip.FileHeader{Name: "link"}
				h.SetMode(os.ModeSymlink | 0777)
				w, e := z.CreateHeader(h)
				if e != nil {
					t.Fatal(e)
				}
				w.Write([]byte("../outside"))
			} else {
				for _, n := range []string{"x32/A.dll", "x32/a.dll"} {
					w, e := z.Create(n)
					if e != nil {
						t.Fatal(e)
					}
					w.Write([]byte("bad"))
				}
			}
			z.Close()
			f.Close()
			if _, e := preparePackage(p, filepath.Join(r, "resources"), "dxvk"); e == nil {
				t.Fatal("link/duplicate accepted")
			}
		})
	}
}
func TestCorruptArchiveAndLimits(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "bad.zip")
	os.WriteFile(p, []byte("broken"), 0644)
	if _, e := preparePackage(p, filepath.Join(r, "resources"), "dxvk"); e == nil {
		t.Fatal("corrupt archive accepted")
	}
	b := extractionBudget{seen: map[string]bool{}}
	if e := b.write(r, "large", maxPackageBytes+1, false, strings.NewReader("")); e == nil {
		t.Fatal("size limit ignored")
	}
	b.files = maxPackageFiles
	if e := b.write(r, "many", 0, false, strings.NewReader("")); e == nil {
		t.Fatal("count limit ignored")
	}
}
func TestDiscoverArchivesAndL4NVersions(t *testing.T) {
	r := t.TempDir()
	res := filepath.Join(r, "resources")
	testZip(t, filepath.Join(res, dxvkVersionsDirName, "dxvk-new.zip"), dxvkFixture("x32/"))
	testZip(t, filepath.Join(res, l4nVersionsDirName, "L4N-new.zip"), l4nFixture())
	testFiles(t, filepath.Join(res, l4nVersionsDirName, "old-folder"), l4nFixture())
	testZip(t, filepath.Join(res, l4nVersionsDirName, "release.zip"), l4nFixture())
	// Legacy locations and root-level packages must not be mixed into the lists.
	testZip(t, filepath.Join(res, "L4N-root.zip"), l4nFixture())
	testZip(t, filepath.Join(res, "L4N其他版本", "legacy.zip"), l4nFixture())
	testZip(t, filepath.Join(res, "dxvk其他版本", "legacy.zip"), dxvkFixture("x32/"))
	dx := discoverDxvkOptions(r, res)
	if len(dx) != 1 || dx[0].Name != "dxvk-new.zip" {
		t.Fatalf("DXVK options %v", dx)
	}
	l4 := discoverL4nOptions(r, res)
	if len(l4) != 3 {
		t.Fatalf("L4N options %v", l4)
	}
	if exists(filepath.Join(res, ".package_tmp")) {
		t.Fatal("discovery should not extract archives")
	}
}

func TestArchiveInstallManifestAndRollback(t *testing.T) {
	r := t.TempDir()
	res := filepath.Join(r, "resources")
	lp := filepath.Join(res, l4nVersionsDirName, "L4N-new.zip")
	dp := filepath.Join(res, dxvkVersionsDirName, "dxvk-new.zip")
	testZip(t, lp, l4nFixture())
	testZip(t, dp, dxvkFixture("nested/x32/"))
	base, e := preparePackage(lp, res, "l4n")
	if e != nil {
		t.Fatal(e)
	}
	dx, e := preparePackage(dp, res, "dxvk")
	if e != nil {
		t.Fatal(e)
	}
	files, e := buildPatchFileList(base.NormalizedDir, dxvkOption{Dir: dx.NormalizedDir})
	if e != nil {
		t.Fatal(e)
	}
	game := filepath.Join(r, "game")
	backup := filepath.Join(r, ".l4n_auto_backup")
	testFiles(t, game, map[string][]byte{"dxgi.dll": []byte("ORIGINAL-DXGI"), "bin/left4neko.dll": []byte("ORIGINAL-L4N"), "unrelated.txt": []byte("KEEP")})
	man := loadManifest(backup, game)
	man.PackageSources = []packageSource{base, dx}
	if e = saveManifest(man, backup); e != nil {
		t.Fatal(e)
	}
	if e = copyPatchFiles(man, backup, game, files); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(backup, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var recorded manifest
	if e = json.Unmarshal(data, &recorded); e != nil {
		t.Fatal(e)
	}
	if len(recorded.PackageSources) != 2 || len(recorded.Files) != len(files) {
		t.Fatal("manifest missing mapping")
	}
	for _, entry := range recorded.Files {
		h, e := fileHash(entry.Target)
		if e != nil || h != entry.SourceSHA256 || entry.Source == "" {
			t.Fatal("target/source hash or position missing")
		}
	}
	t.Logf("INSTALL PASS: %d normalized files; 2 archive sources and every game target/source/hash recorded", len(files))
	if e = copyPatchFiles(man, backup, game, files); e != nil {
		t.Fatal(e)
	}
	if e = restoreFromBackup(backup); e != nil {
		t.Fatal(e)
	}
	for p, want := range map[string]string{"dxgi.dll": "ORIGINAL-DXGI", "bin/left4neko.dll": "ORIGINAL-L4N", "unrelated.txt": "KEEP"} {
		b, e := os.ReadFile(filepath.Join(game, p))
		if e != nil || string(b) != want {
			t.Fatal("rollback mismatch", p, e)
		}
	}
	for _, p := range []string{"dxvk_d3d9.dll", "bin/dxvk_d3d9.dll", "left4dead2/neko/config.vdf", "left4dead2/shaders/test.vcs", "readme_l4n.txt"} {
		if exists(filepath.Join(game, p)) {
			t.Fatal("new file remains", p)
		}
	}
	if exists(backup) {
		t.Fatal("backup should be removed after successful restore")
	}
	if !exists(lp) || !exists(dp) {
		t.Fatal("archives should remain")
	}
	t.Log("ROLLBACK PASS: original files restored, added files removed, unrelated file and archives preserved; reinstall keeps original baseline")
}

func TestBundledRealResources(t *testing.T) {
	root := os.Getenv("L4N_TEST_RESOURCE_ROOT")
	if root == "" {
		t.Skip("set L4N_TEST_RESOURCE_ROOT to validate bundled DLLs")
	}
	r := t.TempDir()
	base, e := preparePackage(filepath.Join(root, l4nVersionsDirName, genericPatchDirName), r, "l4n")
	if e != nil {
		t.Fatal(e)
	}
	files, e := collectBasePatchFiles(base.NormalizedDir)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("bundled L4N: %d normalized base files", len(files))
	opts := discoverDxvkOptions(filepath.Dir(root), root)
	if len(opts) != 12 {
		t.Fatalf("bundled versions=%d", len(opts))
	}
	for _, opt := range opts {
		info, e := preparePackage(opt.Dir, r, "dxvk")
		if e != nil {
			t.Fatal(opt.Name, e)
		}
		files, e := collectDxvkPatchFiles(info.NormalizedDir)
		if e != nil || len(files) != 3 {
			t.Fatal(opt.Name, e)
		}
		t.Logf("bundled %s: 3 verified x86 mappings", opt.Name)
	}
}

func TestTarRejectLinkAndGzipTruncation(t *testing.T) {
	for _, kind := range []string{"link", "truncatedGzip"} {
		t.Run(kind, func(t *testing.T) {
			r := t.TempDir()
			var buf bytes.Buffer
			if kind == "link" {
				tw := tar.NewWriter(&buf)
				if e := tw.WriteHeader(&tar.Header{Name: "link", Linkname: "../outside", Typeflag: tar.TypeSymlink}); e != nil {
					t.Fatal(e)
				}
				tw.Close()
				p := filepath.Join(r, "bad.tar")
				os.WriteFile(p, buf.Bytes(), 0644)
				if _, e := preparePackage(p, r, "dxvk"); e == nil {
					t.Fatal("tar link accepted")
				}
			} else {
				gz := gzip.NewWriter(&buf)
				tw := tar.NewWriter(gz)
				for n, b := range dxvkFixture("x32/") {
					tw.WriteHeader(&tar.Header{Name: n, Mode: 0644, Size: int64(len(b))})
					tw.Write(b)
				}
				tw.Close()
				gz.Close()
				data := buf.Bytes()
				p := filepath.Join(r, "bad.tgz")
				os.WriteFile(p, data[:len(data)-4], 0644)
				if _, e := preparePackage(p, r, "dxvk"); e == nil {
					t.Fatal("truncated gzip accepted")
				}
			}
		})
	}
}

func TestLegacyManifestRestore(t *testing.T) {
	r := t.TempDir()
	game := filepath.Join(r, "game")
	backup := filepath.Join(r, "backup")
	testFiles(t, game, map[string][]byte{"dxgi.dll": []byte("patched"), "added.dll": []byte("added")})
	testFiles(t, backup, map[string][]byte{"files/dxgi.dll": []byte("original")})
	legacy := map[string]any{"createdAt": "legacy", "gameRoot": game, "files": []map[string]any{{"target": filepath.Join(game, "dxgi.dll"), "relative": "dxgi.dll", "existed": true, "backup": filepath.Join(backup, "files/dxgi.dll")}, {"target": filepath.Join(game, "added.dll"), "relative": "added.dll", "existed": false}}}
	data, e := json.Marshal(legacy)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(backup, "manifest.json"), data, 0644)
	if e = restoreFromBackup(backup); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(game, "dxgi.dll"))
	if e != nil || string(b) != "original" || exists(filepath.Join(game, "added.dll")) {
		t.Fatal("legacy manifest restoration mismatch")
	}
}
