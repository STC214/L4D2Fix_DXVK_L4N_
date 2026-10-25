//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"l4nfix/internal/pathguard"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const workshopAPI = "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/"

type workshopPlatform struct{ Kind, Name, ID string }

var workshopPlatforms = []workshopPlatform{{"l4n", "L4N", "3558049615"}, {"skeeto", "Skeeto", "3787239937"}, {"evolved", "L4D2Evolved", "3785109042"}}

// Steam sometimes encodes file_size as a quoted integer.
type steamInt int64

func (n *steamInt) UnmarshalJSON(b []byte) error {
	v, e := strconv.ParseInt(strings.Trim(string(b), `"`), 10, 64)
	*n = steamInt(v)
	return e
}

type workshopDetail struct {
	ID      string   `json:"publishedfileid"`
	Result  int      `json:"result"`
	AppID   int      `json:"consumer_app_id"`
	Content string   `json:"hcontent_file"`
	Updated int64    `json:"time_updated"`
	Size    steamInt `json:"file_size"`
	URL     string   `json:"file_url"`
	Title   string   `json:"title"`
}
type workshopResource struct {
	Platform string         `json:"platform"`
	Detail   workshopDetail `json:"detail"`
	Version  string         `json:"version"`
	Archive  string         `json:"archive"` // relative to resources, never a download URL
	SHA256   string         `json:"sha256"`
	Checked  string         `json:"checked"`
}
type workshopIndex struct {
	Resources []workshopResource `json:"resources"`
	Latest    map[string]string  `json:"latest,omitempty"`
}

func steamDownloadURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("下载地址必须是 Steam HTTPS 地址")
	}
	h := strings.ToLower(u.Hostname())
	if h != "cdn.steamusercontent.com" && h != "steamuserimages-a.akamaihd.net" && !strings.HasSuffix(h, ".steamusercontent.com") {
		return fmt.Errorf("下载地址主机不在 Steam 内容域内: %s", h)
	}
	return nil
}
func workshopClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向次数过多")
		}
		return steamDownloadURL(req.URL.String())
	}}
}
func fetchWorkshopDetails(ctx context.Context, c *http.Client, endpoint string) ([]workshopDetail, error) {
	form := url.Values{"itemcount": {"3"}}
	for i, p := range workshopPlatforms {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), p.ID)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam API HTTP %d", resp.StatusCode)
	}
	var body struct {
		Response struct {
			Details []workshopDetail `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); e != nil {
		return nil, e
	}
	return body.Response.Details, nil
}
func validateWorkshopDetail(p workshopPlatform, d workshopDetail) error {
	if d.ID != p.ID || d.Result != 1 || d.AppID != 550 || d.Updated <= 0 || d.Size <= 0 || int64(d.Size) > maxPackageBytes {
		return fmt.Errorf("%s 元数据身份/大小校验失败", p.Name)
	}
	if _, e := strconv.ParseUint(d.Content, 10, 64); e != nil {
		return fmt.Errorf("内容 ID 无效")
	}
	if d.URL == "" {
		return fmt.Errorf("%s 未公开下载直链，请使用 Steam 订阅或离线包", p.Name)
	}
	return steamDownloadURL(d.URL)
}
func readWorkshopIndex(res string) (workshopIndex, error) {
	var idx workshopIndex
	p := filepath.Join(res, "workshop-index.json")
	if e := pathguard.Within(filepath.Dir(res), p); e != nil {
		return idx, e
	}
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return idx, nil
	}
	if e != nil {
		return idx, e
	}
	e = json.Unmarshal(b, &idx)
	return idx, e
}
func verifiedWorkshopArchive(res string, r workshopResource) (string, error) {
	rel, e := packageRelative(r.Archive)
	if e != nil || rel == "" {
		return "", fmt.Errorf("缓存路径无效")
	}
	if !strings.EqualFold(filepath.Ext(rel), ".zip") || !strings.EqualFold(filepath.Dir(rel), r.Platform) {
		return "", fmt.Errorf("缓存目录无效")
	}
	p := filepath.Join(res, rel)
	if e = pathguard.Within(filepath.Dir(res), p); e != nil {
		return "", e
	}
	st, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	if st.Size() != int64(r.Detail.Size) {
		return "", fmt.Errorf("缓存大小变化")
	}
	h, e := fileHash(p)
	if e != nil {
		return "", e
	}
	if h != r.SHA256 {
		return "", fmt.Errorf("缓存校验失败")
	}
	return p, nil
}

// Version is reserved for a future history selector; empty always selects the
// latest successfully checked resource, never lexicographical directory order.
func selectWorkshopResource(res, kind, version string) (workshopResource, string, error) {
	idx, e := readWorkshopIndex(res)
	if e != nil {
		return workshopResource{}, "", e
	}
	var best *workshopResource
	for i := range idx.Resources {
		r := &idx.Resources[i]
		if version == "" && idx.Latest[kind] != "" && r.Archive != idx.Latest[kind] {
			continue
		}
		if r.Platform == kind && (version == "" || r.Version == version) && (best == nil || r.Detail.Updated > best.Detail.Updated) {
			best = r
		}
	}
	if best == nil {
		return workshopResource{}, "", fmt.Errorf("请先检查更新并完成 %s 下载", kind)
	}
	p, e := verifiedWorkshopArchive(res, *best)
	return *best, p, e
}
func packageVersion(kind, root string) string {
	if kind == "l4n" {
		if v := embeddedL4nVersion(filepath.Join(root, "bin", "left4neko.dll")); v != "" {
			return v
		}
	}
	var candidates []string
	switch kind {
	case "skeeto":
		candidates = []string{"VERSION.txt", "Skeeto/Manifest.json"}
	case "evolved":
		candidates = []string{"Evolved/version.txt", "manifest.json"}
	case "l4n":
		candidates = []string{"readme_l4n.txt"}
	}
	re := regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	for _, p := range candidates {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if e == nil {
			if v := re.FindString(string(b)); v != "" {
				return v
			}
		}
	}
	return ""
}
func downloadWorkshopResource(ctx context.Context, c *http.Client, res string, p workshopPlatform, d workshopDetail) (workshopResource, error) {
	var record workshopResource
	if e := validateWorkshopDetail(p, d); e != nil {
		return record, e
	}
	dir := filepath.Join(res, p.Kind)
	if e := pathguard.Within(filepath.Dir(res), filepath.Join(dir, "download.zip")); e != nil {
		return record, e
	}
	if e := os.MkdirAll(dir, 0755); e != nil {
		return record, e
	}
	f, e := os.CreateTemp(dir, ".pending-*.vpk")
	if e != nil {
		return record, e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if e != nil {
		f.Close()
		return record, e
	}
	resp, e := c.Do(req)
	if e != nil {
		f.Close()
		return record, e
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		f.Close()
		return record, fmt.Errorf("%s 下载 HTTP %d", p.Name, resp.StatusCode)
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, int64(d.Size)+1))
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
	if n != int64(d.Size) {
		return record, fmt.Errorf("%s 下载长度不符: %d / %d", p.Name, n, d.Size)
	}
	// Validate the full ZIP (including member CRC and path/size limits) before
	// changing its extension or publishing it to the interface.
	probe, e := os.MkdirTemp(dir, ".pending-inspect-")
	if e != nil {
		return record, e
	}
	defer os.RemoveAll(probe)
	zipTemp := tmp + ".zip"
	if e = renameFileRetry(tmp, zipTemp); e != nil {
		return record, e
	}
	defer os.Remove(zipTemp)
	if e = extractPackage(zipTemp, probe); e != nil {
		return record, e
	}
	if _, e = normalizedMappings(p.Kind, probe); e != nil {
		return record, e
	}
	v := packageVersion(p.Kind, probe)
	if v == "" {
		v = "content-" + d.Content
	}
	name := fmt.Sprintf("%s-%s-%d-%s.zip", p.Name, v, d.Updated, d.Content)
	target := filepath.Join(dir, name)
	if e = pathguard.Within(filepath.Dir(res), target); e != nil {
		return record, e
	}
	h, e := fileHash(zipTemp)
	if e != nil {
		return record, e
	}
	if e = renameFileRetry(zipTemp, target); e != nil {
		return record, e
	}
	record = workshopResource{Platform: p.Kind, Detail: d, Version: v, Archive: filepath.Join(p.Kind, name), SHA256: h, Checked: time.Now().UTC().Format(time.RFC3339)}
	return record, nil
}
func checkWorkshopUpdates(ctx context.Context, c *http.Client, endpoint, res string, onReady func(workshopResource)) error {
	details, e := fetchWorkshopDetails(ctx, c, endpoint)
	if e != nil {
		return e
	}
	idx, e := readWorkshopIndex(res)
	if e != nil {
		return fmt.Errorf("更新记录损坏: %w", e)
	}
	var failures []string
	for _, p := range workshopPlatforms {
		var d workshopDetail
		count := 0
		for _, item := range details {
			if item.ID == p.ID {
				d = item
				count++
			}
		}
		if count != 1 {
			failures = append(failures, p.Name+" 元数据缺失或重复")
			continue
		}
		if e = validateWorkshopDetail(p, d); e != nil {
			failures = append(failures, e.Error())
			continue
		}
		var ready workshopResource
		found := false
		for _, r := range idx.Resources {
			if r.Platform == p.Kind && r.Detail.Content == d.Content && r.Detail.Updated == d.Updated && r.Detail.Size == d.Size {
				if _, err := verifiedWorkshopArchive(res, r); err == nil {
					ready = r
					ready.Detail = d
					ready.Checked = time.Now().UTC().Format(time.RFC3339)
					found = true
					break
				}
			}
		}
		if !found {
			appendLog("[update] 发现更新或缺少有效本地资源，下载 " + p.Name)
			ready, e = downloadWorkshopResource(ctx, c, res, p, d)
			if e != nil {
				failures = append(failures, p.Name+": "+e.Error())
				continue
			}
		}
		rows := idx.Resources[:0]
		for _, r := range idx.Resources {
			if r.Platform != p.Kind || r.Detail.Content != d.Content {
				rows = append(rows, r)
			}
		}
		idx.Resources = append(rows, ready)
		if idx.Latest == nil {
			idx.Latest = map[string]string{}
		}
		idx.Latest[p.Kind] = ready.Archive
		b, err := json.MarshalIndent(idx, "", "  ")
		if err == nil {
			err = writeFileAtomic(filepath.Join(res, "workshop-index.json"), b, 0644)
		}
		if err != nil {
			return err
		}
		appendLog("[update] " + p.Name + " " + ready.Version + " 已就绪（ZIP/长度/SHA256 已校验）")
		if onReady != nil {
			onReady(ready)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("部分平台检查失败；成功项已保留: %s", strings.Join(failures, "; "))
	}
	return nil
}
