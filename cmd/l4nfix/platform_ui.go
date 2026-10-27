//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	idCheckUpdates = 1010 + iota
	idDeployPlatform
	idCleanPlatform
	idHelp
)
const usageGuideURL = "https://github.com/STC214/L4D2Fix_DXVK_L4N_/blob/HEAD/docs/%E4%BD%BF%E7%94%A8%E6%95%99%E7%A8%8B.md"

var btnCheckUpdates, btnDeployPlatform, btnCleanPlatform, btnHelp, comboPlatform, platformStatusCtl uintptr
var installedPlatformCtl uintptr
var platformReadyMu sync.Mutex
var platformReady = map[string]workshopResource{}
var platformChoices []string

func createPlatformControls(hwnd uintptr) {
	groupBox(hwnd, "4  Skeeto / EVO · 可选增强", 508, 230, 480, 290)
	platformLabel := label(hwnd, "增强平台（二选一）", 524, 256, 448, 22)
	procSendMessageW.Call(platformLabel, wmSetFont, textFont, 1)
	comboPlatform = create("COMBOBOX", "", wsChild|wsVisible|wsTabStop|wsVScroll|cbsDropList|cbsHasStrings, 524, 280, 448, 180, hwnd, 0)
	platformStatusCtl = label(hwnd, "尚未检查更新", 524, 320, 448, 34)
	btnDeployPlatform = button(hwnd, "部署所选平台", idDeployPlatform, 524, 372, 216, 34)
	btnCleanPlatform = button(hwnd, "清理已部署平台", idCleanPlatform, 756, 372, 216, 34)
	installedPlatformCtl = label(hwnd, "增强平台：正在检测…", 524, 420, 448, 78)
	procSendMessageW.Call(installedPlatformCtl, wmSetFont, textFont, 1)
	for _, h := range []uintptr{btnCheckUpdates, comboPlatform, platformStatusCtl, btnDeployPlatform, btnCleanPlatform, btnHelp} {
		procSendMessageW.Call(h, wmSetFont, textFont, 1)
	}
	refreshPlatformChoices()
}
func refreshPlatformChoices() {
	procSendMessageW.Call(comboPlatform, 0x014B, 0, 0)
	platformChoices = nil
	platformReadyMu.Lock()
	for _, kind := range []string{"evolved", "skeeto"} {
		if r, ok := platformReady[kind]; ok {
			platformChoices = append(platformChoices, kind)
			name := "Skeeto"
			if kind == "evolved" {
				name = "L4D2Evolved"
			}
			text := name + " " + r.Version
			procSendMessageW.Call(comboPlatform, cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(text))))
		}
	}
	platformReadyMu.Unlock()
	if len(platformChoices) > 0 {
		procSendMessageW.Call(comboPlatform, cbSetCurSel, 0, 0)
	}
	refreshWorkflowControls()
}
func runCheckUpdates() (result error) {
	changeWorkflow(func(s *workflowState) {
		s.Updates = checkRunning
	})
	defer func() {
		invokeUI(loadLocalResourceChoices)
		changeWorkflow(func(s *workflowState) {
			if result == nil {
				s.Updates = checkPassed
			} else {
				s.Updates = checkFailed
			}
		})
		if result != nil {
			appendLog("[update] 建议检查网络环境，点击 1 检测资源网络，确认连通后再重试 2；已校验成功的资源保留")
		}
	}()
	root, e := packageRoot()
	if e != nil {
		return e
	}
	// Keep complete local versions usable even if a remote check fails.
	workshop := func() error {
		l4nCtx, l4nCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		l4nErr := checkL4NUpdates(l4nCtx, workshopClient(), dxvkReleaseClient(), workshopAPI, l4nReleaseAPI, resourceRoot(root))
		l4nCancel()
		// Independent budget: a slow L4N history batch must not expire the
		// original enhancement-platform check before it even starts.
		ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
		defer cancel()
		var modErrors []error
		client := workshopClient()
		err := checkWorkshopPlatforms(ctx, client, workshopAPI, resourceRoot(root), workshopPlatforms[1:], func(r workshopResource) {
			if r.Platform == "skeeto" {
				if err := prepareSkeetoModCache(ctx, client, workshopAPI, resourceRoot(root), r); err != nil {
					appendLog("[update] Skeeto 工坊 MOD 准备失败: " + err.Error())
					modErrors = append(modErrors, err)
					return
				}
			}
			platformReadyMu.Lock()
			platformReady[r.Platform] = r
			platformReadyMu.Unlock()
			invokeUI(func() {
				refreshPlatformChoices()
			})
		})
		return errors.Join(append(modErrors, err, l4nErr)...)
	}
	dxvk := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		r, err := checkDXVKUpdate(ctx, dxvkReleaseClient(), dxvkReleaseAPI, resourceRoot(root))
		if err != nil {
			return err
		}
		changeWorkflow(func(s *workflowState) { s.DXVKReady = true })
		invokeUI(func() {
			loadDxvkOptionsIntoCombo()
			for i, opt := range dxvkOptions {
				if strings.EqualFold(filepath.Clean(opt.Dir), filepath.Join(resourceRoot(root), r.Archive)) {
					procSendMessageW.Call(comboDxvk, cbSetCurSel, uintptr(i), 0)
				}
			}
		})
		return nil
	}
	var networkErr error
	if workflowSnapshot().Network != checkPassed {
		networkErr = runCheckNetwork()
	}
	updateErr := checkUpdateSources(workshop, dxvk)
	var historyErr error
	if workflowSnapshot().Network == checkPassed {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		var history []catalogResource
		for _, r := range embeddedResourceCatalog.History {
			if r.Kind == "dxvk" {
				history = append(history, r)
			}
		}
		historyErr = downloadEmbeddedHistory(ctx, dxvkReleaseClient(), resourceRoot(root), history)
		cancel()
	} else {
		appendLog("[update] 网络检测未通过，跳过历史版本批量下载；已有本地版本仍可部署")
	}
	return errors.Join(networkErr, updateErr, historyErr)
}
func runDeployPlatform() error {
	// Controls are frozen by guarded while the worker reads the selection.
	idx, _, _ := procSendMessageW.Call(comboPlatform, cbGetCurSel, 0, 0)
	i := int(int32(idx))
	if i < 0 || i >= len(platformChoices) {
		return fmt.Errorf("请先准备完整本地平台资源，再选择平台")
	}
	kind := platformChoices[i]
	platformReadyMu.Lock()
	r, ok := platformReady[kind]
	platformReadyMu.Unlock()
	if !ok {
		return fmt.Errorf("所选平台的本地资源未就绪")
	}
	root, e := packageRoot()
	if e != nil {
		return e
	}
	gameExe, e := resolveGameExe(root)
	if e != nil {
		return e
	}
	if e := checkGameStoppedFor("部署平台"); e != nil {
		return e
	}
	// Pin to the exact content verified in this check, even if another process
	// updates the on-disk index before deployment.
	selected, p, e := selectDeployablePlatformResource(resourceRoot(root), kind, "")
	if e != nil {
		return e
	}
	_ = p
	if selected.Detail.Content != r.Detail.Content || selected.SHA256 != r.SHA256 {
		return fmt.Errorf("资源记录已变化，请重新检查更新")
	}
	return withPlatformSubscriptions(kind, func() error {
		return deployPlatform(root, resourceRoot(root), filepath.Dir(gameExe), kind, "")
	}, func() { updateSkeetoSubscriptions(root, filepath.Dir(gameExe), true) })
}
func runCleanPlatform() error {
	root, e := packageRoot()
	if e != nil {
		return e
	}
	if e := checkCleanupGameStopped(); e != nil {
		return e
	}
	// Use the recorded game, not a newly auto-detected installation.
	data, e := readPlatformManifest(root)
	if e != nil {
		if os.IsNotExist(e) {
			return runManualCleanupUI(root, "platform")
		}
		return e
	}
	return withPlatformSubscriptions(data.Platform, func() error {
		return restorePlatform(root, data.GameRoot, true)
	}, func() { updateSkeetoSubscriptions(root, data.GameRoot, false) })
}
func openUsageGuide() {
	shell := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	r, _, _ := shell.Call(hWnd, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(usageGuideURL))), 0, 0, swShow)
	if r <= 32 {
		appendLog("[help] 请打开 " + usageGuideURL)
	}
}
