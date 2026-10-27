//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Use the updater's HTTP clients and freshly resolved resource URLs, not ICMP
// or an unrelated homepage. No resource/index/Steam account state is changed.
func probeDownload(ctx context.Context, c *http.Client, raw string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "L4NFix-Portable-Updater")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载链路 HTTP %d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if contentType == "text/html" || contentType == "application/xhtml+xml" || contentType == "application/json" {
		return fmt.Errorf("下载链路返回了网页或错误信息（%s），不是资源响应", contentType)
	}
	// Servers may ignore Range. Read at most one byte and close rather than
	// buffering the full archive or leaving its body open.
	var b [1]byte
	if _, err := io.ReadFull(resp.Body, b[:]); err != nil {
		return fmt.Errorf("下载响应为空或中断: %w", err)
	}
	return nil
}

func checkResourceNetwork(ctx context.Context, steam, github *http.Client, steamAPI, githubAPI string, report func(string, error, time.Duration)) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	result := func(name string, started time.Time, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		if report != nil {
			report(name, err, time.Since(started))
		}
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		started := time.Now()
		platforms := append([]workshopPlatform(nil), workshopPlatforms...)
		platforms = append(platforms, workshopPlatform{"preset", "Skeeto 素材 MOD", skeetoModIDs[0]})
		details, err := fetchWorkshopDetailsFor(ctx, steam, steamAPI, platforms)
		result("Steam 工坊接口", started, err)
		if err != nil {
			return
		}
		byID := make(map[string]workshopDetail)
		for _, d := range details {
			byID[d.ID] = d
		}
		// Probe all current platform files plus the extra Skeeto preset. A CDN
		// failure must not be presented as a successful network check.
		var downloads sync.WaitGroup
		for _, p := range platforms {
			p := p
			downloads.Add(1)
			go func() {
				defer downloads.Done()
				started := time.Now()
				d := byID[p.ID]
				err := validateWorkshopDetail(p, d)
				if err == nil {
					err = probeDownload(ctx, steam, d.URL)
				}
				result(p.Name+" 下载链路", started, err)
			}()
		}
		downloads.Wait()
	}()
	go func() {
		defer wg.Done()
		started := time.Now()
		r, err := fetchDXVKRelease(ctx, github, githubAPI)
		result("GitHub 发布接口", started, err)
		if err != nil {
			return
		}
		started = time.Now()
		a, err := selectDXVKAsset(r)
		if err == nil {
			err = probeDownload(ctx, github, a.URL)
		}
		result("DXVK 下载链路", started, err)
	}()
	wg.Wait()
	return errors.Join(errs...)
}

func runCheckNetwork() error {
	changeWorkflow(func(s *workflowState) { s.Network = checkRunning })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	steam, github := workshopClient(), dxvkReleaseClient()
	steam.Timeout, github.Timeout = 12*time.Second, 12*time.Second
	err := checkResourceNetwork(ctx, steam, github, workshopAPI, dxvkReleaseAPI, func(name string, err error, elapsed time.Duration) {
		if err != nil {
			appendLog(fmt.Sprintf("[network] 失败 · %s · %d ms · %v", name, elapsed.Milliseconds(), err))
		} else {
			appendLog(fmt.Sprintf("[network] 通过 · %s · %d ms", name, elapsed.Milliseconds()))
		}
	})
	// The L4N backup is an independent source, not covered by the DXVK probe.
	started := time.Now()
	backupErr := checkL4NBackupNetwork(ctx, github, l4nReleaseAPI)
	if backupErr != nil {
		appendLog(fmt.Sprintf("[network] 失败 · L4N 备份仓库/下载链路 · %d ms · %v", time.Since(started).Milliseconds(), backupErr))
	} else {
		appendLog(fmt.Sprintf("[network] 通过 · L4N 备份仓库/下载链路 · %d ms", time.Since(started).Milliseconds()))
	}
	err = errors.Join(err, backupErr)
	changeWorkflow(func(s *workflowState) {
		if err == nil {
			s.Network = checkPassed
		} else {
			s.Network = checkFailed
		}
	})
	if err != nil {
		return fmt.Errorf("资源网络检测未通过；不建议进行 2 检测更新。请调整网络环境后重测（HTTP 状态/资源异常也会列为失败）: %w", err)
	}
	appendLog("[network] 所有资源接口与下载链路通过；仅抽样连通性，不保证完整下载速度或后续稳定性")
	return nil
}
