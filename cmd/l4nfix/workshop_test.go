//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type workshopTransport func(*http.Request) (*http.Response, error)

func (f workshopTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func workshopFixture(kind string) map[string][]byte {
	switch kind {
	case "l4n":
		return map[string][]byte{"bin/left4neko.dll": testDLL(0x14c), "left4dead2/neko/config_template.vdf": []byte(`"config" { "lock_datacache_size" "1" }`), "readme_l4n.txt": []byte("L4N 2.51.0")}
	case "skeeto":
		return map[string][]byte{"skeeto.dll": testDLL(0x14c), "bin/wininet.dll": testDLL(0x14c), "Skeeto/Manifest.json": []byte(`{"install_layout":2,"requires_manual_migrate":false,"semver":"2.2.3"}`), "VERSION.txt": []byte("2.2.3"), "Skeeto/Commands.txt": []byte("skeeto_nav [0|1]"), "Skeeto/README.txt": []byte("fixture"), "skeeto_apply_update.bat": []byte("fixture, never executed")}
	default:
		return map[string][]byte{"bin/neko/plugins/L4D2Evolved.dll": testDLL(0x14c), "bin/neko/plugins/L4d2evomodel.dll": testDLL(0x14c), "Evolved/config.sample": []byte("#NoHmdMinimapEnabled=true\n#NoHmdWorldNavigationEnabled=true"), "Evolved/version.txt": []byte("1.2.5"), "Evolved/d3d9.dll": testDLL(0x14c), "winmm.dll": testDLL(0x14c), "bin/winmm.dll": testDLL(0x14c), "left4dead2/addons/evo.vpk": []byte("resource")}
	}
}
func workshopZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, data := range files {
		w, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func workshopTestClient(t *testing.T, mutate func([]workshopDetail), bad string) (*http.Client, *int) {
	t.Helper()
	count := new(int)
	payload := map[string][]byte{}
	var details []workshopDetail
	for i, p := range workshopPlatforms {
		b := workshopZIP(t, workshopFixture(p.Kind))
		payload["/"+p.Kind] = b
		details = append(details, workshopDetail{ID: p.ID, Result: 1, AppID: 550, Content: fmt.Sprint(100 + i), Updated: int64(1000 + i), Size: steamInt(len(b)), URL: "https://cdn.steamusercontent.com/" + p.Kind})
	}
	if mutate != nil {
		mutate(details)
	}
	return &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
		var b []byte
		if req.URL.Host == "api.steampowered.com" {
			if req.Method != "POST" {
				t.Fatal("API must be POST")
			}
			req.ParseForm()
			if req.PostForm.Get("itemcount") != "3" {
				t.Fatal("missing platform IDs")
			}
			b, _ = json.Marshal(map[string]any{"response": map[string]any{"publishedfiledetails": details}})
		} else {
			*count++
			b = payload[req.URL.Path]
			if req.URL.Path == bad {
				b = []byte("truncated")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
	})}, count
}
func TestWorkshopUpdateDownloadCacheAndRepair(t *testing.T) {
	res := t.TempDir()
	client, count := workshopTestClient(t, nil, "")
	var ready []workshopResource
	run := func() error {
		return checkWorkshopUpdates(context.Background(), client, workshopAPI, res, func(r workshopResource) { ready = append(ready, r) })
	}
	if e := run(); e != nil {
		t.Fatal(e)
	}
	if *count != 3 || len(ready) != 3 {
		t.Fatalf("downloads=%d choices=%d", *count, len(ready))
	}
	for _, r := range ready {
		p, e := verifiedWorkshopArchive(res, r)
		if e != nil {
			t.Fatal(e)
		}
		if filepath.Ext(p) != ".zip" {
			t.Fatal("not ZIP")
		}
		if r.Platform == "l4n" {
			s, e := preparePackage(p, res, "l4n")
			if e != nil {
				t.Fatal(e)
			}
			if !exists(filepath.Join(s.NormalizedDir, filepath.FromSlash(configRelativePath))) {
				t.Fatal("template not normalized")
			}
		}
	}
	ready = nil
	if e := run(); e != nil {
		t.Fatal(e)
	}
	if *count != 3 || len(ready) != 3 {
		t.Fatal("unchanged metadata should reuse verified archive")
	}
	_, p, e := selectWorkshopResource(res, "skeeto", "")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("corrupt"), 0644)
	if e = run(); e != nil {
		t.Fatal(e)
	}
	if *count != 4 {
		t.Fatal("corrupt cache not repaired")
	}
	entries, _ := filepath.Glob(filepath.Join(res, "skeeto", "*.vpk"))
	if len(entries) != 0 {
		t.Fatal("unrenamed VPK remains")
	}
	entries, _ = filepath.Glob(filepath.Join(res, "skeeto", ".pending-*"))
	if len(entries) != 0 {
		t.Fatal("temporary download remains")
	}
}
func TestWorkshopPartialFailureNeverPublishesBadResource(t *testing.T) {
	res := t.TempDir()
	c, _ := workshopTestClient(t, nil, "/skeeto")
	var kinds []string
	e := checkWorkshopUpdates(context.Background(), c, workshopAPI, res, func(r workshopResource) { kinds = append(kinds, r.Platform) })
	if e == nil || len(kinds) != 2 {
		t.Fatalf("partial error=%v choices=%v", e, kinds)
	}
	if _, _, e = selectWorkshopResource(res, "skeeto", ""); e == nil {
		t.Fatal("bad download selectable")
	}
}
func TestWorkshopMetadataAndURLValidation(t *testing.T) {
	for _, raw := range []string{"http://cdn.steamusercontent.com/a", "https://evil.test/a", "https://cdn.steamusercontent.com.evil.test/a", "https://user@cdn.steamusercontent.com/a", "https://cdn.steamusercontent.com:123/a", "file:///C:/a"} {
		if steamDownloadURL(raw) == nil {
			t.Fatal(raw)
		}
	}
	var size steamInt
	for _, raw := range []string{`"123"`, `123`} {
		if e := json.Unmarshal([]byte(raw), &size); e != nil || size != 123 {
			t.Fatal(raw, e)
		}
	}
	for _, change := range []func(*workshopDetail){func(d *workshopDetail) { d.AppID = 1 }, func(d *workshopDetail) { d.ID = "1" }, func(d *workshopDetail) { d.Result = 9 }, func(d *workshopDetail) { d.Content = "../x" }, func(d *workshopDetail) { d.Size = 0 }, func(d *workshopDetail) { d.URL = "" }} {
		p := workshopPlatforms[0]
		d := workshopDetail{ID: p.ID, Result: 1, AppID: 550, Content: "123", Updated: 1, Size: 2, URL: "https://cdn.steamusercontent.com/a"}
		change(&d)
		if validateWorkshopDetail(p, d) == nil {
			t.Fatal(d)
		}
	}
}
func TestWorkshopRejectsTraversalAndInvalidZIP(t *testing.T) {
	for _, files := range []map[string][]byte{{"../evil.dll": []byte("x")}, {"Skeeto/Manifest.json": []byte("bad")}} {
		b := workshopZIP(t, files)
		p := workshopPlatforms[1]
		d := workshopDetail{ID: p.ID, Result: 1, AppID: 550, Content: "123", Updated: 1, Size: steamInt(len(b)), URL: "https://cdn.steamusercontent.com/a"}
		c := &http.Client{Transport: workshopTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
		})}
		if _, e := downloadWorkshopResource(context.Background(), c, t.TempDir(), p, d); e == nil {
			t.Fatal("invalid ZIP published")
		}
	}
}
func TestWorkshopArchiveScope(t *testing.T) {
	for _, name := range []string{"../outside.zip", "evolved/../l4n/a.zip", "skeeto/a.vpk"} {
		r := workshopResource{Platform: "skeeto", Archive: name}
		if _, e := verifiedWorkshopArchive(t.TempDir(), r); e == nil {
			t.Fatal(name)
		}
	}
}
func TestWorkshopLatestUsesAPIPointerNotHistoryTimestamp(t *testing.T) {
	_, res, _ := platformTestEnvironment(t)
	idx, e := readWorkshopIndex(res)
	if e != nil {
		t.Fatal(e)
	}
	var current workshopResource
	for _, r := range idx.Resources {
		if r.Platform == "skeeto" {
			current = r
		}
	}
	history := current
	history.Detail.Updated += 100
	history.Detail.Content = "999"
	history.Version = "old-history"
	history.Archive = filepath.Join("skeeto", "history.zip")
	if e = copyFile(filepath.Join(res, current.Archive), filepath.Join(res, history.Archive)); e != nil {
		t.Fatal(e)
	}
	idx.Resources = append(idx.Resources, history)
	idx.Latest = map[string]string{"skeeto": current.Archive}
	b, _ := json.Marshal(idx)
	if e = os.WriteFile(filepath.Join(res, "workshop-index.json"), b, 0644); e != nil {
		t.Fatal(e)
	}
	r, _, e := selectWorkshopResource(res, "skeeto", "")
	if e != nil || r.Archive != current.Archive {
		t.Fatal("latest pointer ignored", e)
	}
	r, _, e = selectWorkshopResource(res, "skeeto", "old-history")
	if e != nil || r.Archive != history.Archive {
		t.Fatal("reserved version selector broken", e)
	}
}
func TestWorkshopCancellationDoesNotPublish(t *testing.T) {
	res := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	if e := checkWorkshopUpdates(ctx, c, workshopAPI, res, nil); e == nil {
		t.Fatal("cancelled check succeeded")
	}
	if exists(filepath.Join(res, "workshop-index.json")) {
		t.Fatal("cancelled metadata check published index")
	}
}
func TestLiveWorkshopUpdate(t *testing.T) {
	if os.Getenv("L4N_LIVE_UPDATE_TEST") != "1" {
		t.Skip("opt-in network test")
	}
	res := t.TempDir()
	if e := checkWorkshopUpdates(context.Background(), workshopClient(), workshopAPI, res, func(r workshopResource) { t.Log(r.Platform, r.Version, r.SHA256) }); e != nil {
		t.Fatal(e)
	}
}
func TestRecordedWorkshopArchives(t *testing.T) {
	root := os.Getenv("L4N_RECORDED_ARCHIVES")
	if root == "" {
		t.Skip("opt-in real archive validation")
	}
	for _, p := range workshopPlatforms {
		t.Run(p.Kind, func(t *testing.T) {
			res := t.TempDir()
			src := filepath.Join(res, "recorded.zip")
			if e := copyFile(filepath.Join(root, p.ID+".vpk"), src); e != nil {
				t.Fatal(e)
			}
			s, e := preparePackage(src, res, p.Kind)
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(s.TemporaryDir)
			t.Log("version", packageVersion(p.Kind, filepath.Join(s.TemporaryDir, "extracted")), "files", len(s.Files))
			if p.Kind == "evolved" {
				for _, f := range s.Files {
					if strings.Contains(strings.ToLower(f.Relative), "winmm.dll") || strings.EqualFold(filepath.ToSlash(f.Relative), "Evolved/d3d9.dll") {
						t.Fatal("alternate loader deployed")
					}
				}
			}
		})
	}
}
