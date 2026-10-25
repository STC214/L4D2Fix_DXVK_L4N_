//go:build windows

package main

import (
	"context"
	"fmt"
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
var platformReadyMu sync.Mutex
var platformReady = map[string]workshopResource{}
var platformChoices []string

func createPlatformControls(hwnd uintptr) {
	groupBox(hwnd, "平台资源与部署", 630, 22, 300, 310)
	btnCheckUpdates = button(hwnd, "检查最新版并下载", idCheckUpdates, 646, 50, 268, 36)
	label(hwnd, "增强平台（二选一）", 646, 102, 268, 22)
	comboPlatform = create("COMBOBOX", "", wsChild|wsVisible|wsTabStop|wsVScroll|cbsDropList|cbsHasStrings, 646, 130, 268, 180, hwnd, 0)
	platformStatusCtl = label(hwnd, "尚未检查更新", 646, 164, 268, 42)
	btnDeployPlatform = button(hwnd, "部署所选平台", idDeployPlatform, 646, 220, 268, 36)
	btnCleanPlatform = button(hwnd, "清理已部署平台", idCleanPlatform, 646, 272, 268, 36)
	btnHelp = button(hwnd, "使用说明（在线）", idHelp, 646, 356, 268, 36)
	for _, h := range []uintptr{btnCheckUpdates, comboPlatform, platformStatusCtl, btnDeployPlatform, btnCleanPlatform, btnHelp} {
		procSendMessageW.Call(h, wmSetFont, textFont, 1)
	}
	refreshPlatformChoices()
}
func refreshPlatformChoices() {
	procSendMessageW.Call(comboPlatform, 0x014B, 0, 0)
	platformChoices = nil
	platformReadyMu.Lock()
	for _, kind := range []string{"skeeto", "evolved"} {
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
	text := "尚未检查更新"
	if len(platformChoices) > 0 {
		text = fmt.Sprintf("%d 个平台资源就绪", len(platformChoices))
	}
	procSetWindowTextW.Call(platformStatusCtl, uintptr(unsafe.Pointer(utf16Ptr(text))))
	busyMu.Lock()
	working := busy
	busyMu.Unlock()
	if working || len(platformChoices) == 0 {
		procEnableWindow.Call(btnDeployPlatform, 0)
	} else {
		procEnableWindow.Call(btnDeployPlatform, 1)
	}
}
func runCheckUpdates() error {
	root, e := packageRoot()
	if e != nil {
		return e
	}
	// Revoke stale choices first. A partial check publishes successful items only.
	platformReadyMu.Lock()
	platformReady = map[string]workshopResource{}
	platformReadyMu.Unlock()
	invokeUI(refreshPlatformChoices)
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
	defer cancel()
	return checkWorkshopUpdates(ctx, workshopClient(), workshopAPI, resourceRoot(root), func(r workshopResource) {
		platformReadyMu.Lock()
		platformReady[r.Platform] = r
		platformReadyMu.Unlock()
		invokeUI(func() {
			if r.Platform == "l4n" {
				loadL4nOptionsIntoCombo()
				for i, opt := range l4nOptions {
					if strings.EqualFold(filepath.Clean(opt.Dir), filepath.Join(resourceRoot(root), r.Archive)) {
						procSendMessageW.Call(comboL4n, cbSetCurSel, uintptr(i), 0)
					}
				}
			}
			refreshPlatformChoices()
		})
	})
}
func runDeployPlatform() error {
	// Controls are frozen by guarded while the worker reads the selection.
	idx, _, _ := procSendMessageW.Call(comboPlatform, cbGetCurSel, 0, 0)
	i := int(int32(idx))
	if i < 0 || i >= len(platformChoices) {
		return fmt.Errorf("请先检查更新，下载完成后选择平台")
	}
	kind := platformChoices[i]
	platformReadyMu.Lock()
	r, ok := platformReady[kind]
	platformReadyMu.Unlock()
	if !ok {
		return fmt.Errorf("请先检查所选平台更新")
	}
	root, e := packageRoot()
	if e != nil {
		return e
	}
	gameExe, e := resolveGameExe(root)
	if e != nil {
		return e
	}
	if processExists("left4dead2.exe") {
		return fmt.Errorf("请先退出游戏再部署平台")
	}
	// Pin to the exact content verified in this check, even if another process
	// updates the on-disk index before deployment.
	selected, p, e := selectWorkshopResource(resourceRoot(root), kind, "")
	if e != nil {
		return e
	}
	_ = p
	if selected.Detail.Content != r.Detail.Content || selected.SHA256 != r.SHA256 {
		return fmt.Errorf("资源记录已变化，请重新检查更新")
	}
	return deployPlatform(root, resourceRoot(root), filepath.Dir(gameExe), kind, "")
}
func runCleanPlatform() error {
	root, e := packageRoot()
	if e != nil {
		return e
	}
	if processExists("left4dead2.exe") {
		return fmt.Errorf("请先退出游戏再清理平台")
	}
	// Use the recorded game, not a newly auto-detected installation.
	data, e := readPlatformManifest(root)
	if e != nil {
		return e
	}
	return restorePlatform(root, data.GameRoot, true)
}
func openUsageGuide() {
	shell := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	r, _, _ := shell.Call(hWnd, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(usageGuideURL))), 0, 0, swShow)
	if r <= 32 {
		appendLog("[help] 请打开 " + usageGuideURL)
	}
}
