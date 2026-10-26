//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testModVPK() []byte {
	data := []byte("preset addoninfo fixture")
	tree := []byte("txt\x00 \x00addoninfo\x00")
	entry := make([]byte, 18)
	binary.LittleEndian.PutUint32(entry, crc32.ChecksumIEEE(data))
	binary.LittleEndian.PutUint16(entry[6:], 0x7fff)
	binary.LittleEndian.PutUint32(entry[12:], uint32(len(data)))
	binary.LittleEndian.PutUint16(entry[16:], 0xffff)
	tree = append(tree, entry...)
	tree = append(tree, 0, 0, 0)
	header := make([]byte, 12)
	binary.LittleEndian.PutUint32(header, 0x55aa1234)
	binary.LittleEndian.PutUint32(header[4:], 1)
	binary.LittleEndian.PutUint32(header[8:], uint32(len(tree)))
	return append(append(header, tree...), data...)
}

func cacheSkeetoFixtureMods(t *testing.T, res string, idx workshopIndex) {
	t.Helper()
	cache := skeetoModIndex{}
	for _, r := range idx.Resources {
		if r.Platform == "skeeto" {
			cache.SkeetoContent = r.Detail.Content
			data, e := os.ReadFile(filepath.Join(res, r.Archive))
			if e != nil {
				t.Fatal(e)
			}
			for _, id := range skeetoModIDs {
				d := r.Detail
				d.ID = id
				content := data
				if id == "3782512354" {
					content = testModVPK()
					d.Content = "456"
					d.Size = steamInt(len(content))
				}
				path := filepath.Join(skeetoModCacheDir, id+"-"+d.Content+".vpk")
				testFiles(t, res, map[string][]byte{path: content})
				cache.Mods = append(cache.Mods, skeetoModResource{Detail: d, Archive: path, SHA256: cleanupHash(content)})
			}
		}
	}
	b, _ := json.Marshal(cache)
	testFiles(t, res, map[string][]byte{"skeeto-mods.json": b})
}

func TestSkeetoModCacheUpdateReusesPlatformAndRepairsAssets(t *testing.T) {
	_, res, _ := platformTestEnvironment(t)
	idx, e := readWorkshopIndex(res)
	if e != nil {
		t.Fatal(e)
	}
	var skeeto workshopResource
	for _, r := range idx.Resources {
		if r.Platform == "skeeto" {
			skeeto = r
		}
	}
	asset := testModVPK()
	downloads := 0
	d := workshopDetail{ID: "3782512354", Result: 1, AppID: 550, Content: "999", Updated: 999, Size: steamInt(len(asset)), URL: "https://cdn.steamusercontent.com/preset"}
	client := &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
		var data []byte
		if req.Method == "POST" {
			req.ParseForm()
			if req.PostForm.Get("itemcount") != "1" || req.PostForm.Get("publishedfileids[0]") != "3782512354" {
				t.Fatal("wrong extra metadata request")
			}
			data, _ = json.Marshal(map[string]any{"response": map[string]any{"publishedfiledetails": []workshopDetail{d}}})
		} else {
			downloads++
			data = asset
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
	for i := 0; i < 2; i++ {
		if e := prepareSkeetoModCache(context.Background(), client, workshopAPI, res, skeeto); e != nil {
			t.Fatal(e)
		}
	}
	if downloads != 1 {
		t.Fatal("downloaded unchanged content again", downloads)
	}
	mods, e := preparedSkeetoMods(res, skeeto.Detail.Content)
	if e != nil || len(mods.Mods) != 2 {
		t.Fatal(e, mods)
	}
	for _, r := range mods.Mods {
		if r.Detail.ID == "3787239937" && r.SHA256 != skeeto.SHA256 {
			t.Fatal("platform ZIP was not reused byte-for-byte")
		}
		if r.Detail.ID == "3782512354" {
			os.WriteFile(filepath.Join(res, r.Archive), []byte("bad"), 0644)
		}
	}
	if e := prepareSkeetoModCache(context.Background(), client, workshopAPI, res, skeeto); e != nil {
		t.Fatal(e)
	}
	if downloads != 2 {
		t.Fatal("corrupted cache not repaired", downloads)
	}
	if _, e := preparedSkeetoMods(res, "different-content"); e == nil {
		t.Fatal("mismatched platform accepted")
	}
}

func TestSkeetoVPKRejectsCorruptionAndExternalChunks(t *testing.T) {
	r := t.TempDir()
	p := filepath.Join(r, "asset.vpk")
	valid := testModVPK()
	for _, bad := range []bool{false, true} {
		b := append([]byte(nil), valid...)
		if bad {
			b[len(b)-1] ^= 1
		}
		os.WriteFile(p, b, 0644)
		e := validateSkeetoModFile("3782512354", p, int64(len(b)))
		if (e != nil) != bad {
			t.Fatal("CRC validation", e)
		}
	}
	b := append([]byte(nil), valid...)
	entryOffset := 12 + len("txt\x00 \x00addoninfo\x00")
	binary.LittleEndian.PutUint16(b[entryOffset+6:], 0)
	os.WriteFile(p, b, 0644)
	if e := validateSkeetoModFile("3782512354", p, int64(len(b))); e == nil {
		t.Fatal("external chunk accepted")
	}
}

func TestSkeetoAddonListEditAndCleanupPreserveOtherMods(t *testing.T) {
	original := "\"AddonList\"\n{\n// keep comment\n\"workshop\\3782512354.vpk\" \"0\"\n\"user.vpk\" \"0\"\n}\n"
	enabled, e := editSkeetoAddonList(original, nil)
	if e != nil {
		t.Fatal(e)
	}
	root, e := addonListRoot(enabled)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, n := range root.children {
		if skeetoAddonKey(n.key.value) {
			if n.value.value != "1" {
				t.Fatal(n)
			}
			count++
		}
	}
	if count != 2 || !strings.Contains(enabled, "\"user.vpk\" \"0\"") || !strings.Contains(enabled, "// keep comment") {
		t.Fatal(enabled)
	}
	changed := strings.Replace(enabled, "\"user.vpk\" \"0\"", "\"user.vpk\" \"1\"", 1)
	restored, e := editSkeetoAddonList(changed, &original)
	if e != nil || !strings.Contains(restored, "\"user.vpk\" \"1\"") || strings.Contains(restored, "3787239937") {
		t.Fatal(restored, e)
	}
	root, _ = addonListRoot(restored)
	for _, n := range root.children {
		if skeetoAddonKey(n.key.value) && n.value.value != "0" {
			t.Fatal("old flag not restored")
		}
	}
	none := ""
	untouched := "not a VDF file, no relevant mods"
	got, e := editSkeetoAddonList(untouched, &none)
	if e != nil || got != untouched {
		t.Fatal("unrelated file changed", got, e)
	}
}

func TestSkeetoDeploymentPlacesVPKsAndCleanupMergesAddonFlags(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	testFiles(t, game, map[string][]byte{skeetoAddonListRel: []byte("\"AddonList\" { \"other.vpk\" \"0\" }")})
	if e := deployPlatform(root, res, game, "skeeto", ""); e != nil {
		t.Fatal(e)
	}
	idx, e := readSkeetoModIndex(res)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range idx.Mods {
		h, e := fileHash(filepath.Join(game, filepath.FromSlash(skeetoModRelative(r.Detail.ID))))
		if e != nil || h != r.SHA256 {
			t.Fatal("game VPK mismatch", r.Detail.ID, e)
		}
	}
	p := filepath.Join(game, filepath.FromSlash(skeetoAddonListRel))
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "\"other.vpk\" \"0\"", "\"other.vpk\" \"1\"", 1)), 0644)
	if e := restorePlatform(root, game, true); e != nil {
		t.Fatal(e)
	}
	for _, id := range skeetoModIDs {
		if exists(filepath.Join(game, filepath.FromSlash(skeetoModRelative(id)))) {
			t.Fatal("tool-added MOD left behind")
		}
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "\"other.vpk\" \"1\"") || strings.Contains(string(b), "3787239937") {
		t.Fatal("unrelated addon flags lost", string(b))
	}
}

func TestSkeetoCancelledCacheNeverReportsReady(t *testing.T) {
	_, res, _ := platformTestEnvironment(t)
	idx, e := readSkeetoModIndex(res)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := cacheSkeetoMod(ctx, http.DefaultClient, res, idx.Mods[0].Detail, idx.Mods, ""); e == nil {
		t.Fatal("cancelled operation reused a successful result")
	}
}

func TestSkeetoMissingModStopsBeforeAnyGameWrite(t *testing.T) {
	root, res, game := platformTestEnvironment(t)
	idx, e := readSkeetoModIndex(res)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(game, "left4dead2/cfg/autoexec.cfg"))
	if e != nil {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(res, idx.Mods[0].Archive))
	if e := deployPlatform(root, res, game, "skeeto", ""); e == nil {
		t.Fatal("missing asset cache accepted")
	}
	after, _ := os.ReadFile(filepath.Join(game, "left4dead2/cfg/autoexec.cfg"))
	if string(before) != string(after) || exists(filepath.Join(game, "skeeto.dll")) || exists(filepath.Join(root, platformBackupName, "manifest.json")) {
		t.Fatal("preflight failure changed the game")
	}
}

func TestSkeetoVPKVersion2AndPreload(t *testing.T) {
	base := testModVPK()
	treeSize := int(binary.LittleEndian.Uint32(base[8:12]))
	tree := base[12 : 12+treeSize]
	payload := base[12+treeSize:]
	header := make([]byte, 28)
	copy(header, base[:12])
	binary.LittleEndian.PutUint32(header[4:], 2)
	binary.LittleEndian.PutUint32(header[12:], uint32(len(payload)))
	v2 := append(append(header, tree...), payload...)
	entry := len("txt\x00 \x00addoninfo\x00")
	preloadTree := append([]byte(nil), tree[:entry+18]...)
	binary.LittleEndian.PutUint16(preloadTree[entry+4:], uint16(len(payload)))
	binary.LittleEndian.PutUint32(preloadTree[entry+12:], 0)
	preloadTree = append(preloadTree, payload...)
	preloadTree = append(preloadTree, tree[entry+18:]...)
	preloadHeader := append([]byte(nil), base[:12]...)
	binary.LittleEndian.PutUint32(preloadHeader[8:], uint32(len(preloadTree)))
	preload := append(preloadHeader, preloadTree...)
	for name, data := range map[string][]byte{"version2": v2, "preload": preload} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "asset.vpk")
			if e := os.WriteFile(p, data, 0644); e != nil {
				t.Fatal(e)
			}
			if e := validateSkeetoModFile("3782512354", p, int64(len(data))); e != nil {
				t.Fatal(e)
			}
		})
	}
}
