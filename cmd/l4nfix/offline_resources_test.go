//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func catalogFixture(t *testing.T) (catalogResource, []byte) {
	t.Helper()
	payload := workshopZIP(t, workshopFixture("l4n"))
	sum := sha256.Sum256(payload)
	return catalogResource{Kind: "l4n", Version: "2.51.0", Archive: "l4n/L4N_v2.51.0.zip", URL: "https://github.com/STC214/L4D2Fix_DXVK_L4N_/releases/download/V8.15/L4N_v2.51.0.zip", Size: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}, payload
}

func TestHistoryDownloadAtomicRestartReuseAndManualCopy(t *testing.T) {
	r, payload := catalogFixture(t)
	res := t.TempDir()
	calls := 0
	c := &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload))}, nil
	})}
	for i := 0; i < 2; i++ {
		if err := downloadCatalogResource(context.Background(), c, res, r); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || len(discoverL4nOptions("", res)) != 1 {
		t.Fatal("cache missing or redownloaded", calls)
	}
	// Emulate restart: read only disk, not session readiness or network status.
	if !hasLocalCatalogVersion(res, r) {
		t.Fatal("download not reusable after restart")
	}
	if err := os.WriteFile(filepath.Join(res, filepath.FromSlash(r.Archive)), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := downloadCatalogResource(context.Background(), c, res, r); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("corrupt cache not repaired")
	}
	manual := t.TempDir()
	file := filepath.Join(manual, "l4n", "L4N_v2.51.0-repacked.zip")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := downloadCatalogResource(context.Background(), c, manual, r); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("equivalent manually imported version downloaded again")
	}
}

func TestHistoryFailuresNeverPublishVersion(t *testing.T) {
	for _, failure := range []string{"short", "hash", "archive", "http", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			r, payload := catalogFixture(t)
			res := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			status := 200
			switch failure {
			case "short":
				payload = payload[:8]
			case "hash":
				payload[0] ^= 0xff
			case "archive":
				payload = []byte("not an archive")
				r.Size = int64(len(payload))
				sum := sha256.Sum256(payload)
				r.SHA256 = hex.EncodeToString(sum[:])
			case "http":
				status = 403
			case "cancel":
				cancel()
			}
			c := &http.Client{Transport: workshopTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload))}, nil
			})}
			if err := downloadCatalogResource(ctx, c, res, r); err == nil {
				t.Fatal("failed download accepted")
			}
			if len(discoverL4nOptions("", res)) != 0 {
				t.Fatal("partial/bad version exposed")
			}
		})
	}
}

func TestHistoryManifestAndPathValidation(t *testing.T) {
	counts := map[string]int{}
	for _, r := range embeddedResourceCatalog.History {
		if err := validateCatalogResource(r); err != nil {
			t.Fatal(err)
		}
		counts[r.Kind]++
	}
	if counts["l4n"] != 5 || counts["dxvk"] != 10 {
		t.Fatal(counts)
	}
	bundle := map[string]int{}
	for _, r := range embeddedResourceCatalog.Bundled {
		bundle[r.Kind]++
	}
	if bundle["l4n"] != 2 || bundle["dxvk"] != 4 || bundle["skeeto"] != 1 || bundle["evolved"] != 1 {
		t.Fatal(bundle)
	}
	for _, bad := range []string{"../outside.zip", "l4n/../../outside.zip", "dxvk/wrong.zip", "l4n/.pending.part"} {
		r, _ := catalogFixture(t)
		r.Archive = bad
		if validateCatalogResource(r) == nil {
			t.Fatal("bad path accepted", bad)
		}
	}
	r, _ := catalogFixture(t)
	r.URL = "https://github.com/another/repository/releases/download/v1/file.zip"
	if validateCatalogResource(r) == nil {
		t.Fatal("untrusted resource repository accepted")
	}
}

func TestLocalPlatformsOfflineAndIncompleteNewerFallback(t *testing.T) {
	_, res, _ := platformTestEnvironment(t)
	if got := localPlatformResources(res); len(got) != 2 {
		t.Fatal("complete local platforms require online check", got)
	}
	idx, err := readWorkshopIndex(res)
	if err != nil {
		t.Fatal(err)
	}
	var old workshopResource
	for _, r := range idx.Resources {
		if r.Platform == "skeeto" {
			old = r
		}
	}
	newer := old
	newer.Detail.Content = "456"
	newer.Detail.Updated += 100
	newer.Version = "next"
	newer.Archive = "skeeto/newer.zip"
	// Valid new platform core but missing matching MOD downloads must not
	// replace the fully deployable local version or require networking.
	data, err := os.ReadFile(filepath.Join(res, old.Archive))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res, newer.Archive), data, 0600); err != nil {
		t.Fatal(err)
	}
	idx.Resources = append(idx.Resources, newer)
	idx.Latest = map[string]string{"skeeto": newer.Archive}
	b, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(res, "workshop-index.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	got, _, err := selectDeployablePlatformResource(res, "skeeto", "")
	if err != nil || got.Detail.Content != old.Detail.Content {
		t.Fatal("incomplete new download displaced ready local version", got, err)
	}
	mods, err := readSkeetoModIndex(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(res, mods.Mods[0].Archive)); err != nil {
		t.Fatal(err)
	}
	if _, ok := localPlatformResources(res)["skeeto"]; ok {
		t.Fatal("missing mandatory MOD offered for deployment")
	}
}

func TestPreparedOfflineBundleSnapshot(t *testing.T) {
	stage := os.Getenv("L4N_OFFLINE_STAGE")
	if stage == "" {
		t.Skip("set L4N_OFFLINE_STAGE to inspect real prepared resources")
	}
	res := t.TempDir()
	for _, r := range append(append([]catalogResource{}, embeddedResourceCatalog.Bundled...), embeddedResourceCatalog.History...) {
		file := filepath.Join(stage, filepath.Base(r.Archive))
		if _, err := os.Stat(file); os.IsNotExist(err) {
			continue
		}
		if !catalogFileMatches(file, r) {
			t.Fatal("snapshot hash mismatch", r.Archive)
		}
		prepared, err := preparePackage(file, res, r.Kind)
		if err != nil {
			t.Fatal(r.Archive, err)
		}
		actual := packageVersion(r.Kind, prepared.NormalizedDir)
		if r.Kind == "l4n" && actual != "" && actual != r.Version {
			t.Fatal("L4N archive label/version mismatch", r.Version, actual)
		}
		os.RemoveAll(prepared.TemporaryDir)
		t.Logf("verified %s %s", r.Kind, r.Version)
	}
}

func TestRealOfflineResourceDirectory(t *testing.T) {
	res := os.Getenv("L4N_OFFLINE_RESOURCE_ROOT")
	if res == "" {
		t.Skip("set L4N_OFFLINE_RESOURCE_ROOT for shipped resource validation")
	}
	if got := discoverL4nOptions("", res); len(got) != 2 {
		t.Fatalf("L4N bundled count %d", len(got))
	}
	if got := discoverDxvkOptions("", res); len(got) != 4 {
		t.Fatalf("DXVK bundled count %d", len(got))
	}
	ready := localPlatformResources(res)
	if len(ready) != 2 {
		t.Fatalf("offline platform payloads incomplete: %v", ready)
	}
	if ready["skeeto"].Version != "2.2.4" || ready["evolved"].Version != "1.2.5" {
		t.Fatal("wrong bundled platform versions", ready)
	}
	t.Log("Real local resources ready without HTTP: 2 L4N, 4 DXVK, Skeeto and EVO, including both Skeeto MOD payloads")
}
