//go:build windows

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"l4nfix/internal/pathguard"
)

const l4nReleaseAPI = "https://api.github.com/repos/STC214/l4n-releases/releases"
const l4nReleasePrefix = "https://github.com/STC214/l4n-releases/releases/"

var l4nReleaseTag = regexp.MustCompile(`^L4N_v(\d+\.\d+\.\d+)$`)
var l4nReleaseFile = regexp.MustCompile(`^L4N_v(\d+\.\d+\.\d+)\.(?:zip|7z)$`)

func checkL4NBackupNetwork(ctx context.Context, c *http.Client, endpoint string) error {
	rows, err := fetchL4NReleases(ctx, c, endpoint)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("L4N 备份仓库未找到有效发布附件")
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return compareOptionVersions(strings.Split(rows[i].Version, "."), strings.Split(rows[j].Version, ".")) > 0
	})
	return probeDownload(ctx, c, rows[0].URL)
}

// Versions come from tags and payloads, never Release publication order.
func fetchL4NReleases(ctx context.Context, c *http.Client, endpoint string) ([]catalogResource, error) {
	var rows []catalogResource
	for page := 1; page <= 100; page++ {
		resp, err := dxvkGET(ctx, c, fmt.Sprintf("%s?per_page=100&page=%d", endpoint, page), true)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("L4N GitHub API HTTP %d", resp.StatusCode)
		}
		var releases []dxvkRelease
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&releases)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range releases {
			m := l4nReleaseTag.FindStringSubmatch(r.Tag)
			if r.Draft || r.Prerelease || r.ID <= 0 || len(m) != 2 || r.Page != l4nReleasePrefix+"tag/"+r.Tag {
				continue
			}
			for _, a := range r.Assets {
				// Exactly named ZIP/7z assets; ignore unrelated files and source archives.
				fileVersion := l4nReleaseFile.FindStringSubmatch(a.Name)
				if a.ID <= 0 || a.State != "uploaded" || len(fileVersion) != 2 || compareOptionVersions(strings.Split(fileVersion[1], "."), strings.Split(m[1], ".")) != 0 || a.URL != l4nReleasePrefix+"download/"+r.Tag+"/"+a.Name {
					continue
				}
				digest := strings.TrimPrefix(a.Digest, "sha256:")
				h, e := hex.DecodeString(digest)
				if !strings.HasPrefix(a.Digest, "sha256:") || e != nil || len(h) != 32 {
					continue
				}
				row := catalogResource{Kind: "l4n", Version: l4nVersionKey(m[1]), Archive: "l4n/" + a.Name, URL: a.URL, Size: a.Size, SHA256: strings.ToLower(digest)}
				if validateCatalogResource(row) == nil {
					rows = append(rows, row)
				}
			}
		}
		if len(releases) < 100 {
			return rows, nil
		}
	}
	return nil, fmt.Errorf("L4N Release 分页超限，停止以避免使用不完整版本列表")
}

func preparedL4NVersion(p packageSource) string {
	for _, root := range []string{p.NormalizedDir, filepath.Join(p.TemporaryDir, "extracted"), p.Source} {
		if v := packageVersion("l4n", root); v != "" {
			return v
		}
	}
	return ""
}
func l4nVersionKey(v string) string {
	parts := strings.Split(v, ".")
	for i := range parts {
		parts[i] = strings.TrimLeft(parts[i], "0")
		if parts[i] == "" {
			parts[i] = "0"
		}
	}
	return strings.Join(parts, ".")
}

// Count distinct, fully validated local payload versions, not filenames or ZIPs
// that merely exist. Corrupt/incomplete imports cannot suppress updates.
func localL4NVersions(res string) map[string]bool {
	out := map[string]bool{}
	for _, o := range discoverVersionOptions(res, "l4n", "l4n") {
		p, e := preparePackage(o.Dir, res, "l4n")
		if e != nil {
			continue
		}
		v := preparedL4NVersion(p)
		os.RemoveAll(p.TemporaryDir)
		if l4nReleaseTag.MatchString("L4N_v" + v) {
			out[l4nVersionKey(v)] = true
		}
	}
	return out
}
func planL4NDownloads(local map[string]bool, candidates []catalogResource) []catalogResource {
	var newest []string
	for v := range local {
		p := strings.Split(v, ".")
		if compareOptionVersions(p, newest) > 0 {
			newest = p
		}
	}
	rows := append([]catalogResource(nil), candidates...)
	sort.SliceStable(rows, func(i, j int) bool {
		return compareOptionVersions(strings.Split(rows[i].Version, "."), strings.Split(rows[j].Version, ".")) > 0
	})
	seen := map[string]bool{}
	var out []catalogResource
	for _, r := range rows {
		v := l4nVersionKey(r.Version)
		if seen[v] || local[v] {
			continue
		}
		seen[v] = true
		if len(local) >= 5 && compareOptionVersions(strings.Split(v, "."), newest) <= 0 {
			continue
		}
		out = append(out, r)
		if len(local) < 5 && len(out) == 5 {
			break
		}
	}
	return out
}

// Steam exposes no reliable semantic version in metadata. Cache its validated
// ZIP probe separately, then combine its payload version with all GitHub tags.
// A probe is never a deployment choice until the merged download plan selects it.
func probeL4NWorkshop(ctx context.Context, c *http.Client, endpoint, res string) (workshopResource, string, error) {
	p := workshopPlatforms[0]
	details, e := fetchWorkshopDetailsFor(ctx, c, endpoint, []workshopPlatform{p})
	if e != nil {
		return workshopResource{}, "", e
	}
	if len(details) != 1 {
		return workshopResource{}, "", fmt.Errorf("L4N 工坊元数据缺失或重复")
	}
	d := details[0]
	if e = validateWorkshopDetail(p, d); e != nil {
		return workshopResource{}, "", e
	}
	cache := filepath.Join(res, ".l4n-workshop-cache")
	if e = pathguard.Within(res, filepath.Join(cache, "workshop-index.json")); e != nil {
		return workshopResource{}, "", e
	}
	for _, base := range []string{res, cache} {
		idx, err := readWorkshopIndex(base)
		if err != nil {
			return workshopResource{}, "", err
		}
		for _, r := range idx.Resources {
			if r.Platform == "l4n" && r.Detail.Content == d.Content && r.Detail.Updated == d.Updated && r.Detail.Size == d.Size {
				if path, err := verifiedWorkshopArchive(base, r); err == nil {
					// The hash binds the bytes, not the mutable index's Version field.
					// Re-read the payload version even when no download is needed.
					prepared, err := preparePackage(path, res, "l4n")
					if err != nil {
						continue
					}
					v := preparedL4NVersion(prepared)
					os.RemoveAll(prepared.TemporaryDir)
					if !l4nReleaseTag.MatchString("L4N_v" + v) {
						continue
					}
					r.Version = v
					if err = ctx.Err(); err != nil {
						return r, "", err
					}
					return r, path, nil
				}
			}
		}
	}
	r, e := downloadWorkshopResource(ctx, c, cache, p, d)
	if e != nil {
		return r, "", e
	}
	if !l4nReleaseTag.MatchString("L4N_v" + r.Version) {
		return r, "", fmt.Errorf("工坊 ZIP 内未识别到 L4N 版本")
	}
	if e = ctx.Err(); e != nil {
		return r, "", e
	}
	// Reuse the successfully inspected probe on later checks; normal selectable
	// cached versions remain untouched.
	idx := workshopIndex{Resources: []workshopResource{r}, Latest: map[string]string{"l4n": r.Archive}}
	b, _ := json.MarshalIndent(idx, "", "  ")
	if e = writeFileAtomic(filepath.Join(cache, "workshop-index.json"), b, 0644); e != nil {
		return r, "", e
	}
	return r, filepath.Join(cache, r.Archive), nil
}

func checkL4NUpdates(ctx context.Context, steam, github *http.Client, steamAPI, githubAPI, res string) error {
	local := localL4NVersions(res) // Snapshot BEFORE downloading/probing either source.
	var failures []error
	appendLog("[l4n] 依次检查 Steam 工坊 → GitHub 备份仓库")
	sr, sp, se := probeL4NWorkshop(ctx, steam, steamAPI, res)
	if se != nil {
		failures = append(failures, fmt.Errorf("L4N 工坊: %w", se))
	}
	rows, ge := fetchL4NReleases(ctx, github, githubAPI)
	if ge == nil && len(rows) == 0 {
		ge = fmt.Errorf("L4N 备份仓库未找到有效发布附件")
	}
	if ge != nil {
		failures = append(failures, ge)
	}
	// A failed repository check must not suppress a valid Workshop update.
	if se == nil {
		rows = append([]catalogResource{{Kind: "l4n", Version: sr.Version, Archive: filepath.ToSlash(sr.Archive), Size: int64(sr.Detail.Size), SHA256: sr.SHA256}}, rows...)
	}
	plan := planL4NDownloads(local, rows)
	appendLog(fmt.Sprintf("[l4n] 本地有效版本 %d 个，本次待下载 %d 个（同版本去重）", len(local), len(plan)))
	for _, r := range plan {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		var err error
		if r.URL != "" {
			err = downloadCatalogResource(ctx, github, res, r)
		} else {
			target := filepath.Join(res, filepath.FromSlash(r.Archive))
			if err = pathguard.Within(res, target); err == nil {
				err = os.MkdirAll(filepath.Dir(target), 0755)
			}
			if err == nil && !strings.EqualFold(filepath.Clean(sp), filepath.Clean(target)) {
				err = publishL4NProbe(ctx, sp, target, r)
			}
			if err == nil {
				appendLog("[l4n] 工坊 VPK → ZIP，内部版本 " + r.Version + " 校验完成，已加入本地选择")
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("L4N %s: %w", r.Version, err))
		}
	}
	return errors.Join(failures...)
}

func publishL4NProbe(ctx context.Context, source, target string, r catalogResource) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".pending-l4n-publish-*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	if err = copyFile(source, tmp); err != nil {
		return err
	}
	if !catalogFileMatches(tmp, r) {
		return fmt.Errorf("L4N 工坊缓存复制校验失败")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return renameFileRetry(tmp, target)
}
