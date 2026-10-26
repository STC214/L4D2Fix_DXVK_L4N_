//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// Hidden window only: no buttons are clicked and no game files are changed.
func TestNativeHiddenUIReadOnly(t *testing.T) {
	if os.Getenv("L4N_NATIVE_UI_TEST") != "1" {
		t.Skip("native hidden UI check not enabled")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hInstance, _, _ = procGetModuleHandleW.Call(0)
	icc := initCommonControlsEx{dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})), dwICC: iccProgressClass}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	blackBr, _, _ = procCreateSolidBrush.Call(colorBlack)
	defer procDeleteObject.Call(blackBr)
	textFont = createFont(16, 400)
	titleFont = createFont(18, 600)
	buttonFont = createFont(16, 600)
	guideFont = createFont(14, 400)
	class := utf16Ptr(fmt.Sprintf("L4NAuditHidden_%d", os.Getpid()))
	wc := wndClassEx{cbSize: uint32(unsafe.Sizeof(wndClassEx{})), lpfnWndProc: syscall.NewCallback(wndProc), hInstance: hInstance, hbrBackground: blackBr, lpszClassName: class}
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		t.Fatalf("register hidden window: %v", err)
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(class)), hInstance)
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(utf16Ptr(windowTitle()))), cbs, cwUseDefault, cwUseDefault, windowWidth, windowHeight, 0, 0, hInstance, 0)
	if hwnd == 0 {
		t.Fatalf("create hidden window: %v", err)
	}
	hWnd = hwnd
	defer func() { procDestroyWindow.Call(hwnd); hWnd = 0 }()
	readText := func(handle uintptr) string {
		buf := make([]uint16, 4096)
		user32.NewProc("GetWindowTextW").Call(handle, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		return syscall.UTF16ToString(buf)
	}
	if readText(btnRefreshVersion) != "刷新版本" {
		t.Fatal("refresh caption missing")
	}
	for handle, want := range map[uintptr]string{btnCheckNetwork: "检测资源网络", btnCheckUpdates: "检查最新版并下载", btnDeployPlatform: "部署所选平台", btnCleanPlatform: "清理已部署平台", btnHelp: "使用说明（在线）"} {
		if handle == 0 || readText(handle) != want {
			t.Fatalf("platform control missing: %s", want)
		}
	}
	if !strings.Contains(usageGuideURL, "/docs/") {
		t.Fatal("repository usage link missing")
	}
	if installedPlatformCtl == 0 || readText(installedPlatformCtl) != "增强平台：正在检测…" {
		t.Fatal("installed platform status control missing")
	}
	if font, _, _ := procSendMessageW.Call(installedPlatformCtl, 0x0031, 0, 0); font != textFont {
		t.Fatal("platform version label uses inconsistent font")
	}
	if path := os.Getenv("L4N_UI_CAPTURE_PATH"); path != "" {
		captureNativeTestWindow(t, hwnd, path)
	}
	oldWorkflow := workflowSnapshot()
	oldChoices := platformChoices
	defer func() { workflowMu.Lock(); workflow = oldWorkflow; workflowMu.Unlock(); platformChoices = oldChoices }()
	platformReadyMu.Lock()
	oldPlatformReady := platformReady
	platformReady = map[string]workshopResource{"skeeto": {Version: "2.2.3"}, "evolved": {Version: "1.2.5"}}
	platformReadyMu.Unlock()
	defer func() { platformReadyMu.Lock(); platformReady = oldPlatformReady; platformReadyMu.Unlock() }()
	refreshPlatformChoices()
	selected, _, _ := procSendMessageW.Call(comboPlatform, cbGetCurSel, 0, 0)
	if len(platformChoices) != 2 || platformChoices[int(selected)] != "evolved" {
		t.Fatal("EVO not selected by default")
	}
	platformReadyMu.Lock()
	delete(platformReady, "evolved")
	platformReadyMu.Unlock()
	refreshPlatformChoices()
	selected, _, _ = procSendMessageW.Call(comboPlatform, cbGetCurSel, 0, 0)
	if len(platformChoices) != 1 || selected != 0 || platformChoices[0] != "skeeto" {
		t.Fatal("Skeeto fallback missing")
	}
	setState := func(s workflowState, working bool) {
		workflowMu.Lock()
		workflow = s
		workflowMu.Unlock()
		busyMu.Lock()
		busy = working
		busyMu.Unlock()
		refreshWorkflowControls()
	}
	enabled := func(h uintptr) bool { r, _, _ := user32.NewProc("IsWindowEnabled").Call(h); return r != 0 }
	platformChoices = []string{"skeeto"}
	setState(workflowState{}, false)
	if !enabled(btnCheckNetwork) || !enabled(btnCheckUpdates) || !enabled(btnClean) || !enabled(btnCleanPlatform) || enabled(btnRun) || enabled(btnDeployPlatform) {
		t.Fatal("initial prerequisites/independent cleanup incorrect")
	}
	ready := workflowState{Updates: checkPassed, L4NReady: true, DXVKReady: true}
	setState(ready, false)
	if !enabled(btnRun) || enabled(btnDeployPlatform) {
		t.Fatal("base/enhancement order incorrect")
	}
	ready.BaseInstalled = true
	setState(ready, false)
	if !enabled(btnDeployPlatform) {
		t.Fatal("enhancement still locked after verified base")
	}
	setState(ready, true)
	for _, h := range []uintptr{btnRun, btnDeployPlatform, btnCheckUpdates, btnCheckNetwork, btnClean, btnCleanPlatform} {
		if enabled(h) {
			t.Fatal("task controls active while busy")
		}
	}
	setState(workflowState{Network: checkFailed, Updates: checkFailed}, false)
	if !enabled(btnClean) || !enabled(btnCleanPlatform) || !strings.Contains(readText(updateStatusCtl), "网络检测") || !strings.Contains(readText(networkStatusCtl), "不建议") {
		t.Fatal("failure guidance/cleanup incorrect")
	}
	// Check all five visible numbered section captions and child bounds.
	var client rect
	user32.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&client)))
	groups := 0
	for child, _, _ := user32.NewProc("GetWindow").Call(hwnd, 5); child != 0; child, _, _ = user32.NewProc("GetWindow").Call(child, 2) {
		caption := readText(child)
		if len(caption) > 2 && caption[0] >= '1' && caption[0] <= '5' && strings.HasPrefix(caption[1:], "  ") {
			groups++
		}
		var bounds rect
		user32.NewProc("GetWindowRect").Call(child, uintptr(unsafe.Pointer(&bounds)))
		user32.NewProc("MapWindowPoints").Call(0, hwnd, uintptr(unsafe.Pointer(&bounds)), 2)
		if bounds.left < 0 || bounds.top < 0 || bounds.right > client.right || bounds.bottom > client.bottom {
			t.Fatalf("control outside client bounds: %q %+v client=%+v", caption, bounds, client)
		}
		className := make([]uint16, 64)
		user32.NewProc("GetClassNameW").Call(child, uintptr(unsafe.Pointer(&className[0])), uintptr(len(className)))
		if syscall.UTF16ToString(className) == "STATIC" && caption != "" {
			dc, _, _ := user32.NewProc("GetDC").Call(child)
			font, _, _ := procSendMessageW.Call(child, 0x0031, 0, 0)
			oldFont, _, _ := procSelectObject.Call(dc, font)
			if id, _, _ := procGetDlgCtrlID.Call(child); id == idSectionHeading {
				procSendMessageW.Call(hwnd, wmCtlColorStatic, dc, child)
				textColor, _, _ := gdi32.NewProc("GetTextColor").Call(dc)
				backgroundColor, _, _ := gdi32.NewProc("GetBkColor").Call(dc)
				style, _, _ := user32.NewProc("GetWindowLongW").Call(child, ^uintptr(15)) // GWL_STYLE = -16
				if textColor != colorWhite || backgroundColor != colorBlack || style&0x0201 != 0x0201 {
					t.Fatalf("heading contrast/alignment incorrect: %s", caption)
				}
				if bounds.left != 26 && bounds.left != 518 {
					t.Fatalf("heading gap moved away from left inset: %s %+v", caption, bounds)
				}
			}
			measured := rect{right: bounds.right - bounds.left}
			procDrawTextW.Call(dc, uintptr(unsafe.Pointer(utf16Ptr(caption))), ^uintptr(0), uintptr(unsafe.Pointer(&measured)), 0x0400|0x0010) // DT_CALCRECT|DT_WORDBREAK
			procSelectObject.Call(dc, oldFont)
			user32.NewProc("ReleaseDC").Call(child, dc)
			if measured.bottom > bounds.bottom-bounds.top {
				t.Fatalf("label text clipped: %q needs %d px has %d", caption, measured.bottom, bounds.bottom-bounds.top)
			}
		}
	}
	if groups != 5 {
		t.Fatalf("numbered sections: %d", groups)
	}
	// Execute posted log updates on this UI thread without showing the window.
	for i := 0; i < 60; i++ {
		appendLog(fmt.Sprintf("[network] %d · %s", i, strings.Repeat("下载链路通过；不保证完整下载速度或后续稳定性。", i%4+1)))
		if i%10 == 0 {
			appendLog("")
		}
	}
	appendLog("native-log-old")
	appendLog("native-log-new")
	uiMu.Lock()
	ids := make([]uintptr, 0, len(uiWork))
	for id := range uiWork {
		ids = append(ids, id)
	}
	uiMu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		runUIWork(id)
	}
	if !strings.HasPrefix(readText(logCtl), "native-log-new\r\nnative-log-old\r\n") {
		t.Fatal("newest log is not at top")
	}
	first, _, _ := procSendMessageW.Call(logCtl, 0x00CE, 0, 0) // EM_GETFIRSTVISIBLELINE
	if first != 0 {
		t.Fatal("log not scrolled to top")
	}
	logDC, _, _ := user32.NewProc("GetDC").Call(logCtl)
	for _, message := range []uintptr{wmCtlColorStatic, wmCtlColorEdit} {
		procSetBkMode.Call(logDC, transparent)
		gdi32.NewProc("SetBkColor").Call(logDC, colorWhite)
		procSendMessageW.Call(hwnd, message, logDC, logCtl)
		mode, _, _ := gdi32.NewProc("GetBkMode").Call(logDC)
		bg, _, _ := gdi32.NewProc("GetBkColor").Call(logDC)
		fg, _, _ := gdi32.NewProc("GetTextColor").Call(logDC)
		if mode != 2 || bg != colorBlack || fg != colorWhite {
			t.Fatal("log drawing uses transparent or mismatched background")
		}
	}
	user32.NewProc("ReleaseDC").Call(logCtl, logDC)
	if path := os.Getenv("L4N_UI_LOG_CAPTURE_PATH"); path != "" {
		captureNativeTestWindow(t, hwnd, path)
	}
	setState(oldWorkflow, false)
	// Refreshing after a download must clear stale entries rather than append
	// duplicate version choices, including when no valid resources remain.
	procSendMessageW.Call(comboDxvk, cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr("stale-placeholder"))))
	loadDxvkOptionsIntoCombo()
	loadDxvkOptionsIntoCombo()
	count, _, _ := procSendMessageW.Call(comboDxvk, 0x0146, 0, 0) // CB_GETCOUNT
	if int(count) != len(dxvkOptions) {
		t.Fatalf("DXVK refresh left stale/duplicate choices: %d / %d", count, len(dxvkOptions))
	}
	// Exercise the real native LPARAM callback using Windows-owned memory.
	hdc, _, _ := user32.NewProc("GetDC").Call(btnRefreshVersion)
	defer user32.NewProc("ReleaseDC").Call(btnRefreshVersion, hdc)
	dis := drawItemStruct{ctrlID: idRefreshVersion, hwndItem: btnRefreshVersion, hdc: hdc, rcItem: rect{right: 106, bottom: 30}}
	mem, _, _ := kernel32.NewProc("LocalAlloc").Call(0, unsafe.Sizeof(dis))
	if mem == 0 {
		t.Fatal("native draw buffer allocation failed")
	}
	defer kernel32.NewProc("LocalFree").Call(mem)
	kernel32.NewProc("RtlMoveMemory").Call(mem, uintptr(unsafe.Pointer(&dis)), unsafe.Sizeof(dis))
	if result, _, _ := procSendMessageW.Call(hwnd, wmDrawItem, idRefreshVersion, mem); result != 1 {
		t.Fatal("owner draw callback not handled")
	}
	t.Log("hidden native UI: controls/captions/DXVK refresh/owner draw callback PASS; deployment buttons not clicked")
}
