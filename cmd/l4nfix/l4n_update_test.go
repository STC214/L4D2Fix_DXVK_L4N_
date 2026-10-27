//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestL4NDownloadPlanning(t *testing.T) {
	rows := []catalogResource{}
	for _, v := range []string{"2.9.0", "2.56.0", "2.53.0", "2.56.2", "2.54.0", "2.56.1", "2.55.0", "2.52.0", "2.56.2"} {
		rows = append(rows, catalogResource{Version: v})
	}
	cases := []struct {
		local []string
		want  []string
	}{
		{nil, []string{"2.56.2", "2.56.1", "2.56.0", "2.55.0", "2.54.0"}},
		{[]string{"2.56.2", "2.56.1"}, []string{"2.56.0", "2.55.0", "2.54.0", "2.53.0", "2.52.0"}},
		{[]string{"2.53.0", "2.50.0", "2.49.0", "2.48.0", "2.47.0"}, []string{"2.56.2", "2.56.1", "2.56.0", "2.55.0", "2.54.0"}},
		{[]string{"2.56.2", "2.50.0", "2.49.0", "2.48.0", "2.47.0"}, nil},
		{[]string{"2.57.0", "2.56.0", "2.50.0", "2.49.0", "2.48.0", "2.47.0"}, nil},
	}
	for _, tc := range cases {
		local := map[string]bool{}
		for _, v := range tc.local {
			local[v] = true
		}
		var got []string
		for _, r := range planL4NDownloads(local, rows) {
			got = append(got, r.Version)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("local %v: %v != %v", tc.local, got, tc.want)
		}
	}
}
func l4nReleaseFixture(v string, payload []byte) dxvkRelease {
	sum := sha256.Sum256(payload)
	tag := "L4N_v" + v
	name := tag + ".zip"
	return dxvkRelease{ID: 1, Tag: tag, Page: l4nReleasePrefix + "tag/" + tag, Assets: []dxvkReleaseAsset{{ID: 1, State: "uploaded", Name: name, Size: int64(len(payload)), Digest: "sha256:" + hex.EncodeToString(sum[:]), URL: l4nReleasePrefix + "download/" + tag + "/" + name}}}
}
func l4nPayload(t *testing.T, v string) []byte {
	f := workshopFixture("l4n")
	f["readme_l4n.txt"] = []byte("L4N " + v)
	return workshopZIP(t, f)
}
func TestL4NDualSourceSteamFirstDedupeAndRestart(t *testing.T) {
	res := t.TempDir()
	payload := l4nPayload(t, "2.56.2")
	var sequence []string
	downloads := 0
	d := workshopDetail{ID: "3558049615", AppID: 550, Result: 1, Content: "123", Updated: 100, Size: steamInt(len(payload)), URL: "https://cdn.steamusercontent.com/l4n.vpk"}
	c := &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
		sequence = append(sequence, req.URL.Host)
		var b []byte
		switch req.URL.Host {
		case "api.steampowered.com":
			b, _ = json.Marshal(map[string]any{"response": map[string]any{"publishedfiledetails": []workshopDetail{d}}})
		case "api.github.com":
			b, _ = json.Marshal([]dxvkRelease{l4nReleaseFixture("2.56.2", payload), l4nReleaseFixture("2.56.1", l4nPayload(t, "2.56.1"))})
		case "cdn.steamusercontent.com":
			downloads++
			b = payload
		default:
			downloads++
			b = l4nPayload(t, "2.56.1")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	// ZIP fixture creation is map-order dependent; use a single stable body per version.
	older := l4nPayload(t, "2.56.1")
	base := c.Transport
	c.Transport = workshopTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "github.com" {
			downloads++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(older)), Header: make(http.Header)}, nil
		}
		if r.URL.Host == "api.github.com" {
			sequence = append(sequence, r.URL.Host)
			b, _ := json.Marshal([]dxvkRelease{l4nReleaseFixture("2.56.2", payload), l4nReleaseFixture("2.56.1", older)})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
		}
		return base.RoundTrip(r)
	})
	for i := 0; i < 2; i++ {
		if e := checkL4NUpdates(context.Background(), c, c, workshopAPI, l4nReleaseAPI, res); e != nil {
			t.Fatal(e)
		}
	}
	if downloads != 2 {
		t.Fatal("duplicate download", downloads)
	}
	if len(sequence) < 3 || sequence[0] != "api.steampowered.com" || sequence[1] != "cdn.steamusercontent.com" || sequence[2] != "api.github.com" {
		t.Fatal(sequence)
	}
	if len(localL4NVersions(res)) != 2 || len(discoverVersionOptions(res, "l4n", "l4n")) != 2 {
		t.Fatal("not published or duplicate")
	}
}
func TestL4NRepositoryPaginationAndIdentity(t *testing.T) {
	good := l4nReleaseFixture("2.56.2", []byte("fixture"))
	bad := good
	bad.Tag = "L4N_v9.0.0"
	bad.Page = "https://github.com/OTHER/releases/tag/" + bad.Tag
	calls := 0
	c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		rows := []dxvkRelease{good, bad}
		if r.URL.Query().Get("page") == "1" {
			rows = make([]dxvkRelease, 100)
		}
		b, _ := json.Marshal(rows)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	rows, e := fetchL4NReleases(context.Background(), c, l4nReleaseAPI)
	if e != nil || len(rows) != 1 || calls != 2 {
		t.Fatal(rows, calls, e)
	}
}
func TestL4NRejectMislabeledPayloadAndCorruptLocal(t *testing.T) {
	res := t.TempDir()
	payload := l4nPayload(t, "2.51.0")
	r := l4nReleaseFixture("2.56.2", payload).Assets[0]
	c := &http.Client{Transport: workshopTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload))}, nil
	})}
	row := catalogResource{Kind: "l4n", Version: "2.56.2", Archive: "l4n/" + r.Name, URL: r.URL, Size: r.Size, SHA256: strings.TrimPrefix(r.Digest, "sha256:")}
	if e := downloadCatalogResource(context.Background(), c, res, row); e == nil {
		t.Fatal("mislabeled version accepted")
	}
	if len(discoverVersionOptions(res, "l4n", "l4n")) != 0 {
		t.Fatal("invalid payload exposed")
	}
	os.WriteFile(filepath.Join(res, "l4n", "L4N_v99.0.0.zip"), []byte("broken"), 0600)
	if len(localL4NVersions(res)) != 0 {
		t.Fatal("corrupt file counted")
	}
}
func TestL4NRealBackupArchives(t *testing.T) {
	dir := os.Getenv("L4N_BACKUP_FIXTURES")
	if dir == "" {
		t.Skip("set L4N_BACKUP_FIXTURES for real release archives")
	}
	for _, v := range []string{"2.56.2", "2.56.1", "2.56.0", "2.55.0", "2.54.0"} {
		t.Run(v, func(t *testing.T) {
			p, e := preparePackage(filepath.Join(dir, "L4N_v"+v+".7z"), t.TempDir(), "l4n")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(p.TemporaryDir)
			if got := preparedL4NVersion(p); got != v {
				t.Fatal(fmt.Sprintf("%s != %s", got, v))
			}
		})
	}
}

func TestL4NLiveDualSource(t *testing.T) {
	res := os.Getenv("L4N_LIVE_UPDATE_ROOT")
	if res == "" {
		t.Skip("explicit isolated live cache required")
	}
	if e := os.MkdirAll(res, 0755); e != nil {
		t.Fatal(e)
	}
	downloads := 0
	wrap := func(c *http.Client) *http.Client {
		c.Transport = workshopTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method == "GET" && r.URL.Host != "api.github.com" {
				downloads++
			}
			return http.DefaultTransport.RoundTrip(r)
		})
		return c
	}
	if e := checkL4NUpdates(context.Background(), wrap(workshopClient()), wrap(dxvkReleaseClient()), workshopAPI, l4nReleaseAPI, res); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("L4N_LIVE_REUSE_ONLY") == "1" && downloads != 0 {
		t.Fatal("cache redownloaded", downloads)
	}
	t.Log("payload GET requests", downloads)
	versions := localL4NVersions(res)
	if len(versions) < 5 {
		t.Fatal(versions)
	}
	t.Log("validated local versions", versions)
}

func TestL4NLocalCountUsesPayloadAndAtomicProbe(t *testing.T) {
	res := t.TempDir()
	dir := filepath.Join(res, "l4n")
	os.MkdirAll(dir, 0755)
	payload := l4nPayload(t, "2.51.0")
	for _, name := range []string{"L4N_v2.51.0.zip", "L4N_v99.0.0.zip"} {
		if e := os.WriteFile(filepath.Join(dir, name), payload, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if local := localL4NVersions(res); len(local) != 1 || !local["2.51.0"] {
		t.Fatal("count based on filenames", local)
	}
	src := filepath.Join(dir, "L4N_v2.51.0.zip")
	sum := sha256.Sum256(payload)
	row := catalogResource{Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
	dst := filepath.Join(dir, "L4N_v3.0.0.zip")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := publishL4NProbe(ctx, src, dst, row); e == nil {
		t.Fatal("cancel ignored")
	}
	if _, e := os.Stat(dst); !os.IsNotExist(e) {
		t.Fatal("cancelled probe published")
	}
	row.SHA256 = strings.Repeat("0", 64)
	if e := publishL4NProbe(context.Background(), src, dst, row); e == nil {
		t.Fatal("bad hash accepted")
	}
	if _, e := os.Stat(dst); !os.IsNotExist(e) {
		t.Fatal("bad probe published")
	}
}

func TestL4NIndependentSourceFailure(t *testing.T) {
	for _, failed := range []string{"steam", "github"} {
		t.Run(failed, func(t *testing.T) {
			payload := l4nPayload(t, "2.56.2")
			res := t.TempDir()
			steamChecked, gitChecked := false, false
			c := &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
				b := payload
				status := 200
				switch req.URL.Host {
				case "api.steampowered.com":
					steamChecked = true
					if failed == "steam" {
						status = 403
					} else {
						d := workshopDetail{ID: "3558049615", AppID: 550, Result: 1, Content: "123", Updated: 100, Size: steamInt(len(payload)), URL: "https://cdn.steamusercontent.com/l4n.vpk"}
						b, _ = json.Marshal(map[string]any{"response": map[string]any{"publishedfiledetails": []workshopDetail{d}}})
					}
				case "api.github.com":
					gitChecked = true
					if !steamChecked {
						t.Fatal("source order")
					}
					if failed == "github" {
						status = 403
					} else {
						b, _ = json.Marshal([]dxvkRelease{l4nReleaseFixture("2.56.2", payload)})
					}
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
			})}
			if e := checkL4NUpdates(context.Background(), c, c, workshopAPI, l4nReleaseAPI, res); e == nil {
				t.Fatal("partial failure hidden")
			}
			if !gitChecked || !localL4NVersions(res)["2.56.2"] {
				t.Fatal("healthy source suppressed")
			}
		})
	}
}

func TestL4NProbeRechecksCachedVersion(t *testing.T) {
	res := t.TempDir()
	payload := l4nPayload(t, "2.56.2")
	downloads := 0
	d := workshopDetail{ID: "3558049615", Result: 1, AppID: 550, Content: "123", Updated: 100, Size: steamInt(len(payload)), URL: "https://cdn.steamusercontent.com/l4n.vpk"}
	c := &http.Client{Transport: workshopTransport(func(r *http.Request) (*http.Response, error) {
		b := payload
		if r.URL.Host == "api.steampowered.com" {
			b, _ = json.Marshal(map[string]any{"response": map[string]any{"publishedfiledetails": []workshopDetail{d}}})
		} else {
			downloads++
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	if _, _, e := probeL4NWorkshop(context.Background(), c, workshopAPI, res); e != nil {
		t.Fatal(e)
	}
	cache := filepath.Join(res, ".l4n-workshop-cache")
	idx, e := readWorkshopIndex(cache)
	if e != nil {
		t.Fatal(e)
	}
	idx.Resources[0].Version = "99.0.0"
	b, _ := json.Marshal(idx)
	if e = writeFileAtomic(filepath.Join(cache, "workshop-index.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	r, _, e := probeL4NWorkshop(context.Background(), c, workshopAPI, res)
	if e != nil || r.Version != "2.56.2" || downloads != 1 {
		t.Fatal(r.Version, downloads, e)
	}
}

func TestL4NPaddedTagAndUnlimitedNewerVersions(t *testing.T) {
	r := l4nReleaseFixture("2.07.4", []byte("fixture"))
	r.Assets[0].Name = "L4N_v2.7.4.zip"
	r.Assets[0].URL = l4nReleasePrefix + "download/" + r.Tag + "/" + r.Assets[0].Name
	c := &http.Client{Transport: workshopTransport(func(*http.Request) (*http.Response, error) {
		b, _ := json.Marshal([]dxvkRelease{r})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b))}, nil
	})}
	rows, e := fetchL4NReleases(context.Background(), c, l4nReleaseAPI)
	if e != nil || len(rows) != 1 || rows[0].Version != "2.7.4" {
		t.Fatal(rows, e)
	}
	local := map[string]bool{"2.0.0": true, "2.1.0": true, "2.2.0": true, "2.3.0": true, "2.4.0": true}
	rows = nil
	for i := 5; i <= 12; i++ {
		rows = append(rows, catalogResource{Version: fmt.Sprintf("2.%d.0", i)})
	}
	if got := len(planL4NDownloads(local, rows)); got != 8 {
		t.Fatal("new versions incorrectly capped", got)
	}
}
