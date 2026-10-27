//go:build windows

package main

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"l4nfix/internal/pathguard"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type catalogResource struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Archive string `json:"archive"`
	URL     string `json:"url,omitempty"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}
type resourceCatalog struct {
	AsOf    string            `json:"asOf"`
	History []catalogResource `json:"history"`
	Bundled []catalogResource `json:"bundled"`
}

//go:embed resource_catalog.json
var resourceCatalogJSON []byte
var embeddedResourceCatalog = func() resourceCatalog {
	var c resourceCatalog
	if err := json.Unmarshal(resourceCatalogJSON, &c); err != nil {
		panic(err)
	}
	return c
}()

func validateCatalogResource(r catalogResource) error {
	if r.Kind != "l4n" && r.Kind != "dxvk" {
		return fmt.Errorf("历史资源类型无效")
	}
	rel, err := packageRelative(r.Archive)
	_, archive := archiveName(filepath.Base(rel))
	if err != nil || filepath.ToSlash(rel) != r.Archive || filepath.Dir(rel) != r.Kind || !archive {
		return fmt.Errorf("历史资源路径无效")
	}
	hash, err := hex.DecodeString(r.SHA256)
	if err != nil || len(hash) != 32 || r.Size <= 0 || r.Size > maxPackageBytes {
		return fmt.Errorf("历史资源校验信息无效")
	}
	if err := githubReleaseURL(r.URL); err != nil {
		return err
	}
	u, err := url.Parse(r.URL)
	if err != nil || u.Hostname() != "github.com" || u.RawQuery != "" {
		return fmt.Errorf("历史资源必须来自固定 GitHub 发布链接")
	}
	prefix := "/doitsujin/dxvk/releases/download/"
	if r.Kind == "l4n" {
		prefix = "/STC214/L4D2Fix_DXVK_L4N_/releases/download/"
	}
	if !strings.HasPrefix(u.Path, prefix) {
		return fmt.Errorf("历史资源仓库身份不符")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[0] == "." || parts[0] == ".." || parts[1] != filepath.Base(rel) {
		return fmt.Errorf("历史资源发布路径与文件名不符")
	}
	return nil
}

func catalogFileMatches(path string, r catalogResource) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() != r.Size {
		return false
	}
	h, err := fileHash(path)
	return err == nil && strings.EqualFold(h, r.SHA256)
}

func hasLocalCatalogVersion(res string, r catalogResource) bool {
	target := filepath.Join(res, filepath.FromSlash(r.Archive))
	if pathguard.Within(res, target) != nil {
		return false
	}
	if catalogFileMatches(target, r) {
		return true
	}
	// A valid manually packaged copy of the same version also counts as local;
	// do not download the same release merely to change its archive format.
	version := optionVersionParts(r.Kind, filepath.Base(r.Archive))
	if len(version) == 0 {
		return false
	}
	for _, option := range discoverVersionOptions(res, r.Kind, r.Kind) {
		parts := optionVersionParts(r.Kind, option.Name)
		if len(parts) == 0 || compareOptionVersions(parts, version) != 0 {
			continue
		}
		prepared, err := preparePackage(option.Dir, res, r.Kind)
		if err == nil {
			os.RemoveAll(prepared.TemporaryDir)
			return true
		}
	}
	return false
}

func downloadCatalogResource(ctx context.Context, client *http.Client, res string, r catalogResource) error {
	if err := validateCatalogResource(r); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hasLocalCatalogVersion(res, r) {
		if err := ctx.Err(); err != nil {
			return err
		}
		appendLog("[history] 本地已有 " + r.Kind + " " + r.Version + "，跳过下载")
		return nil
	}
	target := filepath.Join(res, filepath.FromSlash(r.Archive))
	if err := pathguard.Within(res, target); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".pending-history-*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	resp, err := dxvkGET(ctx, client, r.URL, false)
	if err != nil {
		f.Close()
		return err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		f.Close()
		return fmt.Errorf("历史资源 HTTP %d", resp.StatusCode)
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, r.Size+1))
	resp.Body.Close()
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if n != r.Size || !catalogFileMatches(tmp, r) {
		return fmt.Errorf("历史资源长度或 SHA256 不符")
	}
	probe, err := os.MkdirTemp(filepath.Dir(target), ".pending-history-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probe)
	archive := filepath.Join(probe, filepath.Base(target))
	if err := renameFileRetry(tmp, archive); err != nil {
		return err
	}
	prepared, err := preparePackage(archive, res, r.Kind)
	if err != nil {
		return err
	}
	defer os.RemoveAll(prepared.TemporaryDir)
	if prepared.SHA256 != r.SHA256 {
		return fmt.Errorf("历史资源校验期间变化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameFileRetry(archive, target); err != nil {
		return err
	}
	appendLog("[history] " + r.Kind + " " + r.Version + " 下载校验完成，可离线使用")
	return nil
}

func downloadEmbeddedHistory(ctx context.Context, client *http.Client, res string, rows []catalogResource) error {
	var failures []error
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := downloadCatalogResource(ctx, client, res, r); err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", r.Kind, r.Version, err))
			appendLog("[history] 下载失败，保留已有版本: " + err.Error())
		}
	}
	return errors.Join(failures...)
}
