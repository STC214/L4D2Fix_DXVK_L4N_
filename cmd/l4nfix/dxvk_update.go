//go:build windows

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"l4nfix/internal/pathguard"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const dxvkReleaseAPI = "https://api.github.com/repos/doitsujin/dxvk/releases/latest"
const maxDXVKDownload int64 = 256 << 20

type dxvkReleaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	State  string `json:"state"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}
type dxvkRelease struct {
	ID         int64              `json:"id"`
	Tag        string             `json:"tag_name"`
	Page       string             `json:"html_url"`
	Draft      bool               `json:"draft"`
	Prerelease bool               `json:"prerelease"`
	Assets     []dxvkReleaseAsset `json:"assets"`
}
type dxvkDownloadRecord struct {
	ReleaseID int64            `json:"release_id"`
	Tag       string           `json:"tag"`
	Asset     dxvkReleaseAsset `json:"asset"`
	Archive   string           `json:"archive"`
	SHA256    string           `json:"sha256"`
	Checked   string           `json:"checked"`
}

func githubReleaseURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("DXVK 下载地址必须为 GitHub HTTPS 地址")
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	}
	return fmt.Errorf("DXVK 下载地址主机无效: %s", u.Hostname())
}
func dxvkReleaseClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("GitHub 重定向次数过多")
		}
		return githubReleaseURL(r.URL.String())
	}}
}

// Retry transient connection/5xx failures without mirrors or changing the
// source URL. API rate limiting remains an explicit error, not a retry loop.
func dxvkGET(ctx context.Context, c *http.Client, raw string, metadata bool) (*http.Response, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("User-Agent", "L4NFix-Portable-Updater")
		if metadata {
			req.Header.Set("Accept", "application/vnd.github+json")
		}
		resp, e := c.Do(req)
		if ctx.Err() != nil {
			if resp != nil {
				resp.Body.Close()
			}
			return nil, ctx.Err()
		}
		if attempt == 2 || (e == nil && resp.StatusCode < 500) {
			return resp, e
		}
		if resp != nil {
			resp.Body.Close()
		}
		appendLog(fmt.Sprintf("[update] GitHub 连接或服务暂时异常，重试 %d/2", attempt+1))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("GitHub 请求重试结束")
}
func fetchDXVKRelease(ctx context.Context, c *http.Client, endpoint string) (dxvkRelease, error) {
	var release dxvkRelease
	resp, e := dxvkGET(ctx, c, endpoint, true)
	if e != nil {
		return release, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			return release, fmt.Errorf("GitHub API HTTP %d，可能触发限流，请稍后重试（额度重置时间: %s）", resp.StatusCode, resp.Header.Get("X-RateLimit-Reset"))
		}
		return release, fmt.Errorf("GitHub API HTTP %d", resp.StatusCode)
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release)
	return release, e
}
func selectDXVKAsset(r dxvkRelease) (dxvkReleaseAsset, error) {
	var selected dxvkReleaseAsset
	if r.ID <= 0 || r.Draft || r.Prerelease || !regexp.MustCompile(`^v[0-9]+\.[0-9]+(?:\.[0-9]+)?$`).MatchString(r.Tag) {
		return selected, fmt.Errorf("DXVK 最新发布不是受支持的稳定版")
	}
	if r.Page != "https://github.com/doitsujin/dxvk/releases/tag/"+r.Tag {
		return selected, fmt.Errorf("DXVK 发布仓库身份不符")
	}
	expected := "dxvk-" + strings.TrimPrefix(r.Tag, "v") + ".tar.gz"
	count := 0
	for _, a := range r.Assets {
		if a.Name == expected {
			selected = a
			count++
		}
	}
	if count != 1 || selected.ID <= 0 || selected.State != "uploaded" || selected.Size <= 0 || selected.Size > maxDXVKDownload {
		return selected, fmt.Errorf("DXVK 缺少唯一有效 Windows 安装包 %s", expected)
	}
	if selected.URL != "https://github.com/doitsujin/dxvk/releases/download/"+r.Tag+"/"+expected {
		return selected, fmt.Errorf("DXVK 资源下载路径与发布身份不符")
	}
	if selected.Digest != "" {
		if !strings.HasPrefix(selected.Digest, "sha256:") {
			return selected, fmt.Errorf("DXVK 校验算法待支持")
		}
		b, e := hex.DecodeString(strings.TrimPrefix(selected.Digest, "sha256:"))
		if e != nil || len(b) != 32 {
			return selected, fmt.Errorf("DXVK 发布校验值无效")
		}
	}
	return selected, nil
}
func dxvkCachedArchive(res string, r dxvkDownloadRecord) (string, error) {
	rel, e := packageRelative(r.Archive)
	if e != nil || rel == "" || !strings.EqualFold(filepath.Dir(rel), "dxvk") || filepath.Base(rel) != r.Asset.Name {
		return "", fmt.Errorf("DXVK 缓存路径无效")
	}
	p := filepath.Join(res, rel)
	if e = pathguard.Within(filepath.Dir(res), p); e != nil {
		return "", e
	}
	st, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	if st.Size() != r.Asset.Size {
		return "", fmt.Errorf("DXVK 缓存大小变化")
	}
	hash, e := fileHash(p)
	if e != nil {
		return "", e
	}
	if hash != r.SHA256 {
		return "", fmt.Errorf("DXVK 缓存哈希变化")
	}
	if r.Asset.Digest != "" && !strings.EqualFold(r.Asset.Digest, "sha256:"+hash) {
		return "", fmt.Errorf("DXVK 缓存与官方 SHA256 不符")
	}
	return p, nil
}
func checkDXVKUpdate(ctx context.Context, c *http.Client, endpoint, res string) (dxvkDownloadRecord, error) {
	var record dxvkDownloadRecord
	release, e := fetchDXVKRelease(ctx, c, endpoint)
	if e != nil {
		return record, e
	}
	asset, e := selectDXVKAsset(release)
	if e != nil {
		return record, e
	}
	index := filepath.Join(res, "dxvk-index.json")
	if e = pathguard.Within(filepath.Dir(res), index); e != nil {
		return record, e
	}
	b, e := os.ReadFile(index)
	if e == nil {
		if e = json.Unmarshal(b, &record); e != nil {
			return record, fmt.Errorf("DXVK 更新记录损坏: %w", e)
		}
		if record.ReleaseID == release.ID && record.Tag == release.Tag && record.Asset.ID == asset.ID && record.Asset.Size == asset.Size {
			record.Asset = asset
			if _, e = dxvkCachedArchive(res, record); e == nil {
				record.Checked = time.Now().UTC().Format(time.RFC3339)
				b, _ = json.MarshalIndent(record, "", "  ")
				if e = writeFileAtomic(index, b, 0644); e != nil {
					return record, e
				}
				appendLog("[update] DXVK " + release.Tag + " 已是最新，本地缓存校验通过")
				return record, nil
			}
		}
	} else if !os.IsNotExist(e) {
		return record, e
	}
	// A shipped/manual archive may be complete even when no updater index has
	// been written yet. Reuse it against the fresh official digest rather than
	// downloading the bundled latest release again on the first online check.
	if asset.Digest != "" {
		local := dxvkDownloadRecord{ReleaseID: release.ID, Tag: release.Tag, Asset: asset, Archive: filepath.Join("dxvk", asset.Name), SHA256: strings.TrimPrefix(asset.Digest, "sha256:"), Checked: time.Now().UTC().Format(time.RFC3339)}
		if path, err := dxvkCachedArchive(res, local); err == nil {
			prepared, err := preparePackage(path, res, "dxvk")
			if err != nil {
				return record, fmt.Errorf("本地 DXVK 安装包校验失败: %w", err)
			}
			os.RemoveAll(prepared.TemporaryDir)
			if prepared.SHA256 != local.SHA256 {
				return record, fmt.Errorf("本地 DXVK 校验期间变化")
			}
			if err := ctx.Err(); err != nil {
				return record, err
			}
			data, err := json.MarshalIndent(local, "", "  ")
			if err != nil {
				return record, err
			}
			if err := writeFileAtomic(index, data, 0644); err != nil {
				return record, err
			}
			appendLog("[update] DXVK " + release.Tag + " 本地包已通过官方 SHA256，无需重复下载")
			return local, nil
		}
	}
	appendLog("[update] 发现 DXVK 更新或本地缓存缺失/损坏，下载 " + release.Tag)
	dir := filepath.Join(res, "dxvk")
	target := filepath.Join(dir, asset.Name)
	if e = pathguard.Within(filepath.Dir(res), target); e != nil {
		return record, e
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		return record, e
	}
	// Non-archive temporary suffix keeps partial data out of resource discovery.
	f, e := os.CreateTemp(dir, ".pending-dxvk-*.part")
	if e != nil {
		return record, e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	resp, e := dxvkGET(ctx, c, asset.URL, false)
	if e != nil {
		f.Close()
		return record, e
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		f.Close()
		return record, fmt.Errorf("DXVK 下载 HTTP %d", resp.StatusCode)
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, asset.Size+1))
	resp.Body.Close()
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return record, copyErr
	}
	if syncErr != nil {
		return record, syncErr
	}
	if closeErr != nil {
		return record, closeErr
	}
	if n != asset.Size {
		return record, fmt.Errorf("DXVK 下载长度不符: %d / %d", n, asset.Size)
	}
	hash, e := fileHash(tmp)
	if e != nil {
		return record, e
	}
	if asset.Digest != "" && !strings.EqualFold(asset.Digest, "sha256:"+hash) {
		return record, fmt.Errorf("DXVK 官方 SHA256 校验失败")
	}
	probe, e := os.MkdirTemp(dir, ".pending-dxvk-check-")
	if e != nil {
		return record, e
	}
	defer os.RemoveAll(probe)
	archive := filepath.Join(probe, asset.Name)
	if e = renameFileRetry(tmp, archive); e != nil {
		return record, e
	}
	// Existing package validation verifies gzip CRC, size/path limits and the
	// unique x86 DLL pair; x64, native Linux, source and ambiguous layouts fail.
	prepared, e := preparePackage(archive, res, "dxvk")
	if e != nil {
		return record, fmt.Errorf("DXVK 安装包校验失败: %w", e)
	}
	defer os.RemoveAll(prepared.TemporaryDir)
	if prepared.SHA256 != hash {
		return record, fmt.Errorf("DXVK 校验期间安装包发生变化")
	}
	if e = renameFileRetry(archive, target); e != nil {
		return record, e
	}
	record = dxvkDownloadRecord{ReleaseID: release.ID, Tag: release.Tag, Asset: asset, Archive: filepath.Join("dxvk", asset.Name), SHA256: hash, Checked: time.Now().UTC().Format(time.RFC3339)}
	b, e = json.MarshalIndent(record, "", "  ")
	if e != nil {
		return record, e
	}
	if e = writeFileAtomic(index, b, 0644); e != nil {
		return record, e
	}
	appendLog("[update] DXVK " + release.Tag + " 已就绪（长度、SHA256、压缩包与 x86 DLL 已校验）")
	return record, nil
}

// Independent sources always run, even if Steam or GitHub fails first.
func checkUpdateSources(workshop, dxvk func() error) error {
	var failures []string
	if e := workshop(); e != nil {
		failures = append(failures, "Steam: "+e.Error())
	}
	if e := dxvk(); e != nil {
		failures = append(failures, "DXVK: "+e.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("部分更新检查失败；成功项已保留: %s", strings.Join(failures, "; "))
	}
	return nil
}
