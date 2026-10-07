//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
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
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(utf16Ptr(windowTitle()))), cbs, cwUseDefault, cwUseDefault, 960, 560, 0, 0, hInstance, 0)
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
	guide, _, _ := user32.NewProc("FindWindowExW").Call(hwnd, 0, uintptr(unsafe.Pointer(utf16Ptr("EDIT"))), uintptr(unsafe.Pointer(utf16Ptr(usageInstructions))))
	if guide == 0 {
		t.Fatal("usage guide missing")
	}
	style, _, _ := user32.NewProc("GetWindowLongW").Call(guide, ^uintptr(15))
	if style&wsVScroll == 0 {
		t.Fatal("usage guide is not scrollable")
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
	t.Log("hidden native UI: controls/captions/guide scroll/owner draw callback PASS; deployment buttons not clicked")
}
