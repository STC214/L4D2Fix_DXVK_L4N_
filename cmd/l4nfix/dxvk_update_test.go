//go:build windows

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dxvkUpdateFixture(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, b := range files {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(b))}); e != nil {
			t.Fatal(e)
		}
		if _, e := tw.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	return buf.Bytes()
}
func dxvkUpdateRelease(payload []byte) dxvkRelease {
	sum := sha256.Sum256(payload)
	return dxvkRelease{ID: 100, Tag: "v3.1.1", Page: "https://github.com/doitsujin/dxvk/releases/tag/v3.1.1", Assets: []dxvkReleaseAsset{{ID: 200, Name: "dxvk-3.1.1.tar.gz", State: "uploaded", Size: int64(len(payload)), Digest: "sha256:" + hex.EncodeToString(sum[:]), URL: "https://github.com/doitsujin/dxvk/releases/download/v3.1.1/dxvk-3.1.1.tar.gz"}}}
}
func dxvkTestClient(t *testing.T, r dxvkRelease, payload []byte, downloads *int) *http.Client {
	t.Helper()
	return &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
		if e := req.Context().Err(); e != nil {
			return nil, e
		}
		if req.Method != "GET" || req.Header.Get("User-Agent") == "" {
			t.Fatal("missing GET/user agent")
		}
		b := payload
		if req.URL.Host == "api.github.com" {
			if req.Header.Get("Accept") != "application/vnd.github+json" {
				t.Fatal("missing API accept")
			}
			b, _ = json.Marshal(r)
		} else {
			*downloads++
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
}
func TestDXVKUpdateDownloadCacheRepairAndHistoricalVersions(t *testing.T) {
	res := t.TempDir()
	payload := dxvkUpdateFixture(t, dxvkFixture("dxvk-3.1.1/x32/"))
	release := dxvkUpdateRelease(payload)
	downloads := 0
	c := dxvkTestClient(t, release, payload, &downloads)
	old := filepath.Join(res, "dxvk", "dxvk-2.7.1.zip")
	testZip(t, old, dxvkFixture("old/x32/"))
	oldHash, _ := fileHash(old)
	r, e := checkDXVKUpdate(context.Background(), c, dxvkReleaseAPI, res)
	if e != nil {
		t.Fatal(e)
	}
	if downloads != 1 || !strings.HasSuffix(r.Archive, ".tar.gz") {
		t.Fatal(downloads, r)
	}
	archive, e := dxvkCachedArchive(res, r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = checkDXVKUpdate(context.Background(), c, dxvkReleaseAPI, res); e != nil {
		t.Fatal(e)
	}
	if downloads != 1 {
		t.Fatal("valid cache downloaded again")
	}
	os.WriteFile(archive, []byte("broken"), 0644)
	if _, e = checkDXVKUpdate(context.Background(), c, dxvkReleaseAPI, res); e != nil {
		t.Fatal(e)
	}
	if downloads != 2 {
		t.Fatal("corrupt cache not repaired")
	}
	h, _ := fileHash(old)
	if h != oldHash {
		t.Fatal("historical package changed")
	}
	options := discoverDxvkOptions("", res)
	if len(options) != 2 {
		t.Fatalf("options=%v", options)
	}
	temp, _ := filepath.Glob(filepath.Join(res, "dxvk", ".pending-*"))
	if len(temp) > 0 {
		t.Fatal("download temporary files left", temp)
	}
}

func TestDXVKShippedArchiveNeedsNoFirstDownloadOrIndex(t *testing.T) {
	res := t.TempDir()
	payload := dxvkUpdateFixture(t, dxvkFixture("dxvk-3.1.1/x32/"))
	release := dxvkUpdateRelease(payload)
	downloads := 0
	client := dxvkTestClient(t, release, payload, &downloads)
	archive := filepath.Join(res, "dxvk", release.Assets[0].Name)
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, payload, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := checkDXVKUpdate(context.Background(), client, dxvkReleaseAPI, res); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(res, "dxvk-index.json")); err != nil {
			t.Fatal(err)
		}
	}
	if downloads != 0 {
		t.Fatal("complete shipped payload redownloaded because updater index was absent", downloads)
	}
}
func TestDXVKReleaseRejectsWrongAssets(t *testing.T) {
	payload := []byte("payload")
	changes := []func(*dxvkRelease){
		func(r *dxvkRelease) { r.Draft = true }, func(r *dxvkRelease) { r.Prerelease = true }, func(r *dxvkRelease) { r.Tag = "v3.1.1-rc1" },
		func(r *dxvkRelease) { r.Page = "https://github.com/other/dxvk/releases/tag/v3.1.1" },
		func(r *dxvkRelease) { r.Assets[0].Name = "dxvk-native-3.1.1.tar.gz" },
		func(r *dxvkRelease) { r.Assets[0].Name = "Source code.tar.gz" },
		func(r *dxvkRelease) { r.Assets = append(r.Assets, r.Assets[0]) },
		func(r *dxvkRelease) { r.Assets[0].URL = "https://evil.test/dxvk.tar.gz" },
		func(r *dxvkRelease) {
			r.Assets[0].URL = "https://github.com/other/dxvk/releases/download/v3.1.1/dxvk-3.1.1.tar.gz"
		},
		func(r *dxvkRelease) { r.Assets[0].Size = maxDXVKDownload + 1 },
		func(r *dxvkRelease) { r.Assets[0].Digest = "sha256:bad" },
	}
	for i, change := range changes {
		r := dxvkUpdateRelease(payload)
		change(&r)
		if _, e := selectDXVKAsset(r); e == nil {
			t.Fatal("invalid release accepted", i)
		}
	}
}
func TestDXVKUpdateBadDownloadsNeverPublished(t *testing.T) {
	good := dxvkUpdateFixture(t, dxvkFixture("x32/"))
	for _, kind := range []string{"truncated", "digest", "x64", "traversal", "not-archive", "gzip-crc"} {
		t.Run(kind, func(t *testing.T) {
			payload := append([]byte(nil), good...)
			switch kind {
			case "x64":
				payload = dxvkUpdateFixture(t, map[string][]byte{"x64/dxgi.dll": testDLL(0x8664), "x64/d3d9.dll": testDLL(0x8664)})
			case "traversal":
				payload = dxvkUpdateFixture(t, map[string][]byte{"../evil.dll": []byte("bad")})
			case "not-archive":
				payload = []byte("not a tar")
			case "gzip-crc":
				payload[len(payload)-8] ^= 0xff
			}
			r := dxvkUpdateRelease(payload)
			if kind == "truncated" {
				payload = payload[:len(payload)-1]
			}
			if kind == "digest" {
				r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			}
			res := t.TempDir()
			n := 0
			c := dxvkTestClient(t, r, payload, &n)
			if _, e := checkDXVKUpdate(context.Background(), c, dxvkReleaseAPI, res); e == nil {
				t.Fatal("bad download accepted")
			}
			if exists(filepath.Join(res, "dxvk-index.json")) {
				t.Fatal("bad download published index")
			}
			opts := discoverDxvkOptions("", res)
			if len(opts) != 0 {
				t.Fatal("bad download selectable", opts)
			}
			tmp, _ := filepath.Glob(filepath.Join(res, "dxvk", ".pending-*"))
			if len(tmp) > 0 {
				t.Fatal("temporary files left", tmp)
			}
		})
	}
}
func TestDXVKGitHubHostAndRateLimit(t *testing.T) {
	for _, raw := range []string{"http://github.com/a", "https://github.com.evil.test/a", "https://user@github.com/a", "https://github.com:444/a", "https://evil.test/a"} {
		if githubReleaseURL(raw) == nil {
			t.Fatal(raw)
		}
	}
	for _, host := range []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"} {
		if e := githubReleaseURL("https://" + host + "/a"); e != nil {
			t.Fatal(e)
		}
	}
	for _, status := range []int{403, 429, 404} {
		c := &http.Client{Transport: workshopTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("error"))}, nil
		})}
		if _, e := checkDXVKUpdate(context.Background(), c, dxvkReleaseAPI, t.TempDir()); e == nil {
			t.Fatal(status)
		}
	}
}
func TestUpdateSourcesAreIndependent(t *testing.T) {
	for _, bad := range []string{"steam", "dxvk", "both", "none"} {
		calls := 0
		e := checkUpdateSources(func() error {
			calls++
			if bad == "steam" || bad == "both" {
				return fmt.Errorf("steam failed")
			}
			return nil
		}, func() error {
			calls++
			if bad == "dxvk" || bad == "both" {
				return fmt.Errorf("dxvk failed")
			}
			return nil
		})
		if calls != 2 || (e == nil) != (bad == "none") {
			t.Fatal(calls, e)
		}
	}
}
func TestDXVKUpdateCancellationAndCachePath(t *testing.T) {
	payload := dxvkUpdateFixture(t, dxvkFixture("x32/"))
	r := dxvkUpdateRelease(payload)
	n := 0
	c := dxvkTestClient(t, r, payload, &n)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := t.TempDir()
	if _, e := checkDXVKUpdate(ctx, c, dxvkReleaseAPI, res); e == nil {
		t.Fatal("cancelled check succeeded")
	}
	if n != 0 || exists(filepath.Join(res, "dxvk-index.json")) {
		t.Fatal("cancelled check published resource")
	}
	if _, e := dxvkCachedArchive(res, dxvkDownloadRecord{Archive: "../evil.tar.gz", Asset: r.Assets[0]}); e == nil {
		t.Fatal("cache escaped root")
	}
}
func TestLiveDXVKUpdate(t *testing.T) {
	if os.Getenv("L4N_LIVE_DXVK_TEST") != "1" {
		t.Skip("opt-in GitHub network test")
	}
	res := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	r, e := checkDXVKUpdate(ctx, dxvkReleaseClient(), dxvkReleaseAPI, res)
	if e != nil {
		t.Fatal(e)
	}
	t.Log("DOWNLOADED", r.Tag, r.Asset.Size, r.SHA256)
	again, e := checkDXVKUpdate(ctx, dxvkReleaseClient(), dxvkReleaseAPI, res)
	if e != nil {
		t.Fatal(e)
	}
	if again.SHA256 != r.SHA256 {
		t.Fatal("cache changed")
	}
	t.Log("CACHE REUSE PASS; validated TAR.GZ and x86 DLLs; no game deployment")
}
func TestDXVKTransientRetryAndNoRateLimitLoop(t *testing.T) {
	for _, status := range []int{0, 503, 429} {
		calls := 0
		c := &http.Client{Transport: workshopTransport(func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				if status == 0 {
					return nil, fmt.Errorf("temporary dial failure")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("retry"))}, nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
		})}
		resp, e := dxvkGET(context.Background(), c, dxvkReleaseAPI, true)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if status == 429 {
			if calls != 1 || resp.StatusCode != 429 {
				t.Fatal("rate limit retried")
			}
		} else if calls != 2 || resp.StatusCode != 200 {
			t.Fatal("transient failure not retried")
		}
	}
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	c := &http.Client{Transport: workshopTransport(func(*http.Request) (*http.Response, error) { calls++; cancel(); return nil, context.Canceled })}
	if _, e := dxvkGET(ctx, c, dxvkReleaseAPI, true); e == nil || calls != 1 {
		t.Fatal("cancellation retried", calls, e)
	}
}
