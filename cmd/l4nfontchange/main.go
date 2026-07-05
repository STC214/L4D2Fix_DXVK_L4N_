//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	appTitle           = "L4N 字体切换"
	resourceDirName    = "resources"
	fontChangeDirName  = "Font_change"
	configRelativePath = "left4dead2/neko/config.vdf"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procSetWindowTextW   = user32.NewProc("SetWindowTextW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procShowWindow       = user32.NewProc("ShowWindow")
	procEnableWindow     = user32.NewProc("EnableWindow")
	procGetDlgCtrlID     = user32.NewProc("GetDlgCtrlID")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procCreateSolidBrush    = gdi32.NewProc("CreateSolidBrush")
	procCreateFontW         = gdi32.NewProc("CreateFontW")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procGetStockObject      = gdi32.NewProc("GetStockObject")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procSetBkMode           = gdi32.NewProc("SetBkMode")
	procSetTextColor        = gdi32.NewProc("SetTextColor")
	procEnumFontFamiliesExW = gdi32.NewProc("EnumFontFamiliesExW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procAddFontResourceExW  = gdi32.NewProc("AddFontResourceExW")

	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	procDrawTextW            = user32.NewProc("DrawTextW")
	procFillRect             = user32.NewProc("FillRect")

	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	procCommDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")

	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegCreateKeyExW  = advapi32.NewProc("RegCreateKeyExW")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

	hInstance    uintptr
	hWnd         uintptr
	btnApply     uintptr
	btnRestore   uintptr
	btnBrowse    uintptr
	fontCombo    uintptr
	selectedCtl  uintptr
	progress     uintptr
	statusCtl    uintptr
	logCtl       uintptr
	blackBr      uintptr
	textFont     uintptr
	titleFont    uintptr
	buttonFont   uintptr
	previewFont  uintptr
	busyMu       sync.Mutex
	busy         bool
	progressMu   sync.Mutex
	lastProgress int
	uiMu         sync.Mutex
	uiNext       uintptr
	uiWork       = map[uintptr]func(){}
	fontsMu      sync.Mutex
	fontNames    []string
	fontSet      = map[string]string{}
	inputMu      sync.Mutex
	fontInput    string
)

const (
	cwUseDefault = uintptr(0x80000000)
	swShow       = 5

	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmDrawItem       = 0x002B
	wmCommand        = 0x0111
	wmCtlColorEdit   = 0x0133
	wmCtlColorStatic = 0x0138
	wmSetFont        = 0x0030
	wmSetIcon        = 0x0080
	wmSetRedraw      = 0x000B
	wmFontChange     = 0x001D
	wmAppInvoke      = 0x8001

	bnClicked     = 0
	cbnSelChange  = 1
	cbnEditChange = 5
	cbnEditUpdate = 6

	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsVisible      = 0x10000000
	wsChild        = 0x40000000
	wsTabStop      = 0x00010000
	wsBorder       = 0x00800000
	bsOwnerDraw    = 0x0000000B
	cbsDropdown    = 0x0002
	cbsAutoHScroll = 0x0040
	cbsHasStrings  = 0x0200
	esMultiline    = 0x0004
	esAutovScroll  = 0x0040
	esReadOnly     = 0x0800
	wsVScroll      = 0x00200000

	cbs = wsCaption | wsSysMenu | wsMinimizeBox

	idToggle    = 1001
	idRestore   = 1002
	idBrowse    = 1003
	idFontCombo = 1004

	pbmSetRange32  = 0x0400 + 6
	pbmSetPos      = 0x0400 + 2
	emSetSel       = 0x00B1
	emReplaceSel   = 0x00C2
	cbAddString    = 0x0143
	cbResetContent = 0x014B
	cbSetCurSel    = 0x014E
	cbGetCurSel    = 0x0147
	cbGetLBText    = 0x0148
	cbGetLBTextLen = 0x0149
	cbInitStorage  = 0x0161
	cbLimitText    = 0x0141

	colorBlack         = 0x0f0f0f
	colorWhite         = 0x00ffffff
	colorButton        = 0x24201c
	colorButtonPressed = 0x3b332c
	colorAccent        = 0x00d38a1f

	defaultGuiFont = 17
	transparent    = 1
	iccProgress    = 0x00000020
	imageIcon      = 1
	iconSmall      = 0
	iconBig        = 1
	lrDefaultColor = 0

	dtCenter     = 0x00000001
	dtVCenter    = 0x00000004
	dtSingleLine = 0x00000020
	odsSelected  = 0x0001

	dwmwaUseImmersiveDarkMode       = 20
	dwmwaUseImmersiveDarkModeBefore = 19

	hkeyCurrentUser         = 0x80000001
	hkeyLocalMachine        = 0x80000002
	keyRead                 = 0x20019
	regSz                   = 1
	hwndBroadcast           = 0xffff
	coinitApartmentThreaded = 0x2
)

type point struct {
	x, y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type initCommonControlsEx struct {
	dwSize uint32
	dwICC  uint32
}

type rect struct {
	left, top, right, bottom int32
}

type drawItemStruct struct {
	ctrlType   uint32
	ctrlID     uint32
	itemID     uint32
	itemAction uint32
	itemState  uint32
	hwndItem   uintptr
	hdc        uintptr
	rcItem     rect
	itemData   uintptr
}

type logFont struct {
	height         int32
	width          int32
	escapement     int32
	orientation    int32
	weight         int32
	italic         byte
	underline      byte
	strikeOut      byte
	charSet        byte
	outPrecision   byte
	clipPrecision  byte
	quality        byte
	pitchAndFamily byte
	faceName       [32]uint16
}

type openFileName struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

func main() {
	runtime.LockOSThread()
	hInstance, _, _ = procGetModuleHandleW.Call(0)
	icc := initCommonControlsEx{dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})), dwICC: iccProgress}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	blackBr, _, _ = procCreateSolidBrush.Call(colorBlack)
	textFont = createFontFace(20, 400, "Segoe UI")
	titleFont = createFontFace(24, 600, "Segoe UI")
	buttonFont = createFontFace(19, 600, "Segoe UI")

	className := utf16Ptr("L4NFontChangeWindow")
	iconBigHandle := loadAppIcon(32)
	iconSmallHandle := loadAppIcon(16)
	if iconBigHandle == 0 {
		iconBigHandle, _, _ = procLoadIconW.Call(0, 32512)
	}
	if iconSmallHandle == 0 {
		iconSmallHandle = iconBigHandle
	}
	cursor, _, _ := procLoadCursorW.Call(0, 32512)
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInstance,
		hIcon:         iconBigHandle,
		hCursor:       cursor,
		hbrBackground: blackBr,
		lpszClassName: className,
		hIconSm:       iconSmallHandle,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hWnd, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16Ptr(appTitle))),
		cbs|wsVisible,
		cwUseDefault, cwUseDefault,
		920, 520,
		0, 0, hInstance, 0,
	)
	procSendMessageW.Call(hWnd, wmSetIcon, iconBig, iconBigHandle)
	procSendMessageW.Call(hWnd, wmSetIcon, iconSmall, iconSmallHandle)
	enableDarkTitleBar(hWnd)
	procShowWindow.Call(hWnd, swShow)
	procUpdateWindow.Call(hWnd)
	go func() {
		appendLog("[font] loading installed fonts...")
		refreshFontCombo("")
		setStatus("就绪 - 先确认字体，再点击更换字体")
		appendLog(fmt.Sprintf("[font] loaded %d fonts", len(fontNamesSnapshot())))
	}()

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func enableDarkTitleBar(hwnd uintptr) {
	enabled := int32(1)
	size := unsafe.Sizeof(enabled)
	ret, _, _ := procDwmSetWindowAttribute.Call(
		hwnd,
		dwmwaUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&enabled)),
		size,
	)
	if ret != 0 {
		procDwmSetWindowAttribute.Call(
			hwnd,
			dwmwaUseImmersiveDarkModeBefore,
			uintptr(unsafe.Pointer(&enabled)),
			size,
		)
	}
}

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		createControls(hwnd)
		return 0
	case wmAppInvoke:
		runUIWork(wParam)
		return 0
	case wmCommand:
		id, _, _ := procGetDlgCtrlID.Call(lParam)
		code := uint16(wParam >> 16)
		if code == bnClicked {
			switch id {
			case idToggle:
				go guarded("更换字体", runApplyFontChange)
			case idRestore:
				go guarded("恢复默认字体", runRestoreDefaultFont)
			case idBrowse:
				busyMu.Lock()
				isBusy := busy
				busyMu.Unlock()
				if isBusy {
					return 0
				}
				procEnableWindow.Call(btnBrowse, 0)
				path, err := browseFontFile()
				procEnableWindow.Call(btnBrowse, 1)
				if err != nil {
					appendLog("[error] " + err.Error())
					setStatus("失败 - 查看日志")
				} else if path != "" {
					go guarded("安装字体", func() error {
						return runInstallFontFile(path)
					})
				}
			}
		}
		if id == idFontCombo {
			switch code {
			case cbnSelChange:
				updateFontInputFromComboSelection()
			case cbnEditChange, cbnEditUpdate:
				updateFontInputFromCombo()
			}
		}
		return 0
	case wmDrawItem:
		drawButton((*drawItemStruct)(unsafe.Pointer(lParam)))
		return 1
	case wmCtlColorStatic, wmCtlColorEdit:
		hdc := wParam
		procSetBkMode.Call(hdc, transparent)
		procSetTextColor.Call(hdc, colorWhite)
		return blackBr
	case wmDestroy:
		for _, font := range []uintptr{textFont, titleFont, buttonFont, previewFont} {
			if font != 0 {
				procDeleteObject.Call(font)
			}
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func createControls(hwnd uintptr) {
	title := label(hwnd, "L4N 平台字体切换工具", 24, 24, 560, 34)
	procSendMessageW.Call(title, wmSetFont, titleFont, 1)
	desc := label(hwnd, "当前字体更换程序仅在游戏使用 L4N 平台时生效；会修改 left4dead2\\neko\\config.vdf 中 Tahoma 替换行。\r\n浏览字体文件只负责自动安装字体；最终仍需在下拉框确认字体后点击更换字体。", 24, 68, 860, 54)
	procSendMessageW.Call(desc, wmSetFont, textFont, 1)

	fontLabel := label(hwnd, "目标字体", 24, 144, 96, 28)
	procSendMessageW.Call(fontLabel, wmSetFont, textFont, 1)
	fontCombo = create("COMBOBOX", "", wsChild|wsVisible|wsTabStop|wsVScroll|cbsDropdown|cbsAutoHScroll|cbsHasStrings, 128, 138, 380, 320, hwnd, idFontCombo)
	procSendMessageW.Call(fontCombo, wmSetFont, textFont, 1)
	procSendMessageW.Call(fontCombo, cbLimitText, 128, 0)
	selectedCtl = label(hwnd, "字体预览", 540, 132, 340, 86)
	procSendMessageW.Call(selectedCtl, wmSetFont, titleFont, 1)

	btnBrowse = button(hwnd, "浏览字体文件", idBrowse, 24, 218, 190, 50)
	btnApply = button(hwnd, "更换字体", idToggle, 238, 218, 190, 50)
	btnRestore = button(hwnd, "恢复默认字体", idRestore, 452, 218, 190, 50)
	for _, h := range []uintptr{btnBrowse, btnApply, btnRestore} {
		procSendMessageW.Call(h, wmSetFont, buttonFont, 1)
	}

	progress = create("msctls_progress32", "", wsChild|wsVisible, 24, 300, 860, 18, hwnd, 0)
	procSendMessageW.Call(progress, pbmSetRange32, 0, 100)
	procSendMessageW.Call(progress, pbmSetPos, 0, 0)

	statusCtl = label(hwnd, "就绪 - 先确认字体，再点击更换字体", 24, 334, 860, 30)
	procSendMessageW.Call(statusCtl, wmSetFont, textFont, 1)
	logTitle := label(hwnd, "日志", 24, 372, 80, 26)
	procSendMessageW.Call(logTitle, wmSetFont, textFont, 1)
	logCtl = create("EDIT", "", wsChild|wsVisible|wsBorder|esMultiline|esAutovScroll|esReadOnly|wsVScroll, 24, 402, 860, 66, hwnd, 0)
	procSendMessageW.Call(logCtl, wmSetFont, textFont, 1)

	appendLog("[prepare] ready; target: left4dead2\\neko\\config.vdf")
	setStatus("正在加载系统字体...")
}

func label(hwnd uintptr, text string, x, y, w, h int32) uintptr {
	return create("STATIC", text, wsChild|wsVisible, x, y, w, h, hwnd, 0)
}

func button(hwnd uintptr, text string, id uintptr, x, y, w, h int32) uintptr {
	return create("BUTTON", text, wsChild|wsVisible|wsTabStop|bsOwnerDraw, x, y, w, h, hwnd, id)
}

func create(class, text string, style uintptr, x, y, w, h int32, parent, id uintptr) uintptr {
	child, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(class))),
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, hInstance, 0,
	)
	return child
}

func createFont(height int32, weight int32) uintptr {
	return createFontFace(height, weight, "Segoe UI")
}

func createFontFace(height int32, weight int32, faceName string) uintptr {
	font, _, _ := procCreateFontW.Call(
		uintptr(height),
		0, 0, 0,
		uintptr(weight),
		0, 0, 0,
		1,
		0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16Ptr(faceName))),
	)
	if font == 0 {
		font, _, _ = procGetStockObject.Call(defaultGuiFont)
	}
	return font
}

func loadAppIcon(size int32) uintptr {
	for id := uint16(1); id <= 32; id++ {
		icon, _, _ := procLoadImageW.Call(
			hInstance,
			uintptr(unsafe.Pointer(uint16PtrFromID(id))),
			imageIcon,
			uintptr(size),
			uintptr(size),
			lrDefaultColor,
		)
		if icon != 0 {
			return icon
		}
	}
	return 0
}

func drawButton(dis *drawItemStruct) {
	bg := uintptr(colorButton)
	if dis.itemState&odsSelected != 0 {
		bg = colorButtonPressed
	}
	brush, _, _ := procCreateSolidBrush.Call(bg)
	procFillRect.Call(dis.hdc, uintptr(unsafe.Pointer(&dis.rcItem)), brush)
	procDeleteObject.Call(brush)
	accent := rect{left: dis.rcItem.left, top: dis.rcItem.bottom - 4, right: dis.rcItem.right, bottom: dis.rcItem.bottom}
	accentBrush, _, _ := procCreateSolidBrush.Call(colorAccent)
	procFillRect.Call(dis.hdc, uintptr(unsafe.Pointer(&accent)), accentBrush)
	procDeleteObject.Call(accentBrush)
	oldFont, _, _ := procSelectObject.Call(dis.hdc, buttonFont)
	procSetBkMode.Call(dis.hdc, transparent)
	procSetTextColor.Call(dis.hdc, colorWhite)
	rc := dis.rcItem
	rc.top += 3
	procDrawTextW.Call(dis.hdc, uintptr(unsafe.Pointer(utf16Ptr(buttonText(dis.ctrlID)))), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), dtCenter|dtVCenter|dtSingleLine)
	if oldFont != 0 {
		procSelectObject.Call(dis.hdc, oldFont)
	}
}

func buttonText(id uint32) string {
	switch id {
	case idToggle:
		return "更换字体"
	case idRestore:
		return "恢复默认字体"
	case idBrowse:
		return "浏览字体文件"
	default:
		return ""
	}
}

func guarded(name string, fn func() error) {
	busyMu.Lock()
	if busy {
		busyMu.Unlock()
		return
	}
	busy = true
	progressMu.Lock()
	lastProgress = -1
	progressMu.Unlock()
	busyMu.Unlock()
	setBusy(true)
	setProgress(0)
	setStatus(name + "中...")
	appendLog("")
	appendLog("[start] " + name)
	err := fn()
	if err != nil {
		appendLog("[error] " + err.Error())
		setStatus("失败 - 查看日志")
	} else {
		setProgress(100)
		appendLog("[done] " + name + "完成")
		setStatus("完成")
	}
	setBusy(false)
	busyMu.Lock()
	busy = false
	busyMu.Unlock()
}

func setBusy(v bool) {
	invokeUI(func() {
		en := uintptr(1)
		if v {
			en = 0
		}
		procEnableWindow.Call(btnApply, en)
		procEnableWindow.Call(btnRestore, en)
		procEnableWindow.Call(btnBrowse, en)
		procEnableWindow.Call(fontCombo, en)
	})
}

func setStatus(s string) {
	invokeUI(func() {
		procSetWindowTextW.Call(statusCtl, uintptr(unsafe.Pointer(utf16Ptr(s))))
	})
}

func setProgress(v int) {
	if v < 0 {
		v = 0
	} else if v > 100 {
		v = 100
	}
	progressMu.Lock()
	if v == lastProgress {
		progressMu.Unlock()
		return
	}
	lastProgress = v
	progressMu.Unlock()
	invokeUI(func() {
		procSendMessageW.Call(progress, pbmSetPos, uintptr(v), 0)
	})
}

func appendLog(s string) {
	invokeUI(func() {
		line := s + "\r\n"
		procSendMessageW.Call(logCtl, emSetSel, ^uintptr(0), ^uintptr(0))
		procSendMessageW.Call(logCtl, emReplaceSel, 0, uintptr(unsafe.Pointer(utf16Ptr(line))))
	})
}

func invokeUI(fn func()) {
	if fn == nil {
		return
	}
	if hWnd == 0 {
		fn()
		return
	}
	uiMu.Lock()
	uiNext++
	id := uiNext
	uiWork[id] = fn
	uiMu.Unlock()
	ok, _, _ := procPostMessageW.Call(hWnd, wmAppInvoke, id, 0)
	if ok == 0 {
		uiMu.Lock()
		delete(uiWork, id)
		uiMu.Unlock()
	}
}

func runUIWork(id uintptr) {
	uiMu.Lock()
	fn := uiWork[id]
	delete(uiWork, id)
	uiMu.Unlock()
	if fn != nil {
		fn()
	}
}

func runApplyFontChange() error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	fontName, err := selectedFontName()
	if err != nil {
		return err
	}
	gameExe, err := resolveGameExe(root)
	if err != nil {
		return err
	}
	gameRoot := filepath.Dir(gameExe)
	configPath := filepath.Join(gameRoot, filepath.FromSlash(configRelativePath))
	if !exists(configPath) {
		return errors.New("没有进行通用处理，通用处理后再使用本工具")
	}
	if err := updateConfigTahomaFont(configPath, fontName); err != nil {
		return err
	}

	appendLog("[font] config: " + configPath)
	appendLog("[font] selected: " + fontName)
	setProgress(95)
	appendLog("[font] config.vdf Tahoma font updated")
	return nil
}

func runRestoreDefaultFont() error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	gameExe, err := resolveGameExe(root)
	if err != nil {
		return err
	}
	gameRoot := filepath.Dir(gameExe)
	configPath := filepath.Join(gameRoot, filepath.FromSlash(configRelativePath))
	if !exists(configPath) {
		return errors.New("没有进行通用处理，通用处理后再使用本工具")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	updated, err := commentConfigFontBlock(string(data))
	if err != nil {
		return err
	}
	if err := backupConfigFile(configPath); err != nil {
		return err
	}
	if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
		return err
	}
	appendLog("[font] config: " + configPath)
	setProgress(10)
	setProgress(95)
	appendLog("[font] font block commented")
	return nil
}

func runInstallFontFile(path string) error {
	before := fontSetSnapshot()
	appendLog("[font] selected file: " + path)
	if err := installFontFile(path); err != nil {
		return err
	}
	after := fontSetSnapshot()
	preferred := ""
	for _, name := range fontNamesSnapshot() {
		if _, ok := before[strings.ToLower(name)]; !ok {
			if _, ok := after[strings.ToLower(name)]; ok {
				preferred = name
				break
			}
		}
	}
	refreshFontCombo(preferred)
	if preferred != "" {
		appendLog("[font] new installed font: " + preferred)
	} else {
		appendLog("[font] font list refreshed; use the dropdown to confirm the target font")
	}
	return nil
}

func findFontChangeDir(root string) (string, error) {
	for _, dir := range []string{
		filepath.Join(root, resourceDirName, fontChangeDirName),
		filepath.Join(root, fontChangeDirName),
	} {
		if exists(dir) {
			return dir, nil
		}
	}
	return "", errors.New("未找到 Font_change 目录")
}

func refreshFontCombo(preferred string) {
	names := installedFontNames()
	fontsMu.Lock()
	fontNames = names
	fontSet = map[string]string{}
	for _, name := range names {
		fontSet[strings.ToLower(name)] = name
	}
	fontsMu.Unlock()

	populateFontCombo(names, preferred)
}

func populateFontCombo(names []string, preferred string) {
	if preferred == "" {
		preferred = readFontModName()
	}
	const batchSize = 80
	selectedIndex := -1
	if preferred != "" {
		for i, name := range names {
			if strings.EqualFold(name, preferred) {
				selectedIndex = i
				break
			}
		}
	}
	invokeUI(func() {
		procSendMessageW.Call(fontCombo, cbResetContent, 0, 0)
		procSendMessageW.Call(fontCombo, cbInitStorage, uintptr(len(names)), uintptr(max(1, len(names))*32))
	})
	var addBatch func(start int)
	addBatch = func(start int) {
		end := min(start+batchSize, len(names))
		invokeUI(func() {
			procSendMessageW.Call(fontCombo, wmSetRedraw, 0, 0)
			for _, name := range names[start:end] {
				procSendMessageW.Call(fontCombo, cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(name))))
			}
			procSendMessageW.Call(fontCombo, wmSetRedraw, 1, 0)
			if end < len(names) {
				go addBatch(end)
				return
			}
			if selectedIndex >= 0 {
				procSendMessageW.Call(fontCombo, cbSetCurSel, uintptr(selectedIndex), 0)
			}
			if preferred != "" {
				setComboText(preferred)
			}
			updateSelectedFontDisplay()
		})
	}
	if len(names) == 0 {
		invokeUI(updateSelectedFontDisplay)
		return
	}
	addBatch(0)
}

func installedFontNames() []string {
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil
	}
	defer procReleaseDC.Call(0, hdc)
	seen := map[string]bool{}
	var names []string
	cb := syscall.NewCallback(func(lplf uintptr, lpntme uintptr, fontType uintptr, lParam uintptr) uintptr {
		lf := (*logFont)(unsafe.Pointer(lplf))
		name := strings.TrimSpace(syscall.UTF16ToString(lf.faceName[:]))
		if name != "" && !strings.HasPrefix(name, "@") {
			key := strings.ToLower(name)
			if !seen[key] {
				seen[key] = true
				names = append(names, name)
			}
		}
		return 1
	})
	var lf logFont
	lf.charSet = 1
	procEnumFontFamiliesExW.Call(hdc, uintptr(unsafe.Pointer(&lf)), cb, 0, 0)
	sort.Strings(names)
	return names
}

func fontSetSnapshot() map[string]string {
	fontsMu.Lock()
	defer fontsMu.Unlock()
	out := map[string]string{}
	for k, v := range fontSet {
		out[k] = v
	}
	return out
}

func fontNamesSnapshot() []string {
	fontsMu.Lock()
	defer fontsMu.Unlock()
	out := make([]string, len(fontNames))
	copy(out, fontNames)
	return out
}

func selectedFontName() (string, error) {
	text := strings.TrimSpace(currentFontInput())
	if text == "" {
		return "", errors.New("请先选择或输入字体名")
	}
	fontsMu.Lock()
	defer fontsMu.Unlock()
	if len(fontNames) == 0 {
		return "", errors.New("系统字体列表还在加载，请稍后再试")
	}
	if exact, ok := fontSet[strings.ToLower(text)]; ok {
		return exact, nil
	}
	var matches []string
	for _, name := range fontNames {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(text)) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		invokeUI(func() {
			setComboText(matches[0])
			updateSelectedFontDisplay()
		})
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("字体名不完整，匹配到 %d 个字体，请从下拉框选择准确字体", len(matches))
	}
	return "", fmt.Errorf("系统中未找到字体: %s", text)
}

func updateSelectedFontDisplay() {
	text := strings.TrimSpace(currentFontInput())
	display := "字体预览"
	fontName := ""
	if text != "" {
		if exact, ok := fontSetSnapshot()[strings.ToLower(text)]; ok {
			fontName = exact
			display = exact + "\r\n中文字体预览 ABC 123"
		} else if match := firstFontPrefixMatch(text); match != "" {
			fontName = match
			display = match + "\r\n中文字体预览 ABC 123"
		} else {
			display = "未找到字体：\r\n" + text
		}
	}
	if fontName != "" {
		setPreviewFont(fontName)
	} else {
		setPreviewFont("Segoe UI")
	}
	procSetWindowTextW.Call(selectedCtl, uintptr(unsafe.Pointer(utf16Ptr(display))))
}

func updateFontInputFromCombo() {
	setFontInput(windowText(fontCombo))
	updateSelectedFontDisplay()
}

func updateFontInputFromComboSelection() {
	if name := comboSelectedText(); name != "" {
		setFontInput(name)
		updateSelectedFontDisplay()
		return
	}
	updateFontInputFromCombo()
}

func comboSelectedText() string {
	idx, _, _ := procSendMessageW.Call(fontCombo, cbGetCurSel, 0, 0)
	if int32(idx) < 0 {
		return ""
	}
	length, _, _ := procSendMessageW.Call(fontCombo, cbGetLBTextLen, idx, 0)
	if int32(length) < 0 {
		return ""
	}
	buf := make([]uint16, int(length)+1)
	procSendMessageW.Call(fontCombo, cbGetLBText, idx, uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func setFontInput(s string) {
	inputMu.Lock()
	fontInput = strings.TrimSpace(s)
	inputMu.Unlock()
}

func currentFontInput() string {
	inputMu.Lock()
	defer inputMu.Unlock()
	return fontInput
}

func setPreviewFont(fontName string) {
	newFont := createFontFace(24, 500, fontName)
	old := previewFont
	previewFont = newFont
	procSendMessageW.Call(selectedCtl, wmSetFont, previewFont, 1)
	if old != 0 {
		procDeleteObject.Call(old)
	}
}

func firstFontPrefixMatch(prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return ""
	}
	for _, name := range fontNamesSnapshot() {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			return name
		}
	}
	return ""
}

func setComboText(s string) {
	procSetWindowTextW.Call(fontCombo, uintptr(unsafe.Pointer(utf16Ptr(s))))
	setFontInput(s)
}

func readFontModName() string {
	return ""
}

func updateFontModName(path, fontName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	inTahoma := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Tahoma:" {
			inTahoma = true
			continue
		}
		if !inTahoma {
			continue
		}
		if strings.HasPrefix(trimmed, "name:") {
			lineBreak := ""
			body := line
			if strings.HasSuffix(body, "\n") {
				lineBreak = "\n"
				body = strings.TrimSuffix(body, "\n")
			}
			if strings.HasSuffix(body, "\r") {
				lineBreak = "\r" + lineBreak
				body = strings.TrimSuffix(body, "\r")
			}
			indent := body[:strings.Index(body, "name:")]
			lines[i] = indent + "name: " + fontName + lineBreak
			if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0644); err != nil {
				return err
			}
			appendLog("[font] FontMod.yaml Tahoma.name = " + fontName)
			return nil
		}
		if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(line, "    ") {
			break
		}
	}
	return errors.New("FontMod.yaml 中未找到 fonts.Tahoma.name")
}

func updateConfigTahomaFont(path, fontName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(data), "\n")
	start, end, err := findFontBlockLines(lines)
	if err != nil {
		return err
	}
	if fontBlockIsCommented(lines[start:end]) {
		for i := start; i < end; i++ {
			lines[i] = uncommentConfigLine(lines[i])
		}
	}
	re := regexp.MustCompile(`^(\s*)(//\s*)?"Tahoma"\s+"([^"]*)"([^\r\n]*)`)
	for i := start; i < end; i++ {
		body, br := splitLineBreak(lines[i])
		if m := re.FindStringSubmatch(body); len(m) == 5 {
			lines[i] = fmt.Sprintf(`%s"Tahoma" "%s"%s%s`, m[1], fontName, m[4], br)
			if err := backupConfigFile(path); err != nil {
				return err
			}
			return os.WriteFile(path, []byte(strings.Join(lines, "")), 0644)
		}
	}
	return errors.New("config.vdf 中未找到 Tahoma 字体替换行")
}

func backupConfigFile(path string) error {
	backup := path + ".l4nfontchange.bak"
	if exists(backup) {
		return nil
	}
	if err := copyFile(path, backup); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(backup, info.Mode().Perm())
		_ = os.Chtimes(backup, info.ModTime(), info.ModTime())
	}
	appendLog("[font] backup config: " + backup)
	return nil
}

func commentConfigFontBlock(text string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	start, end, err := findFontBlockLines(lines)
	if err != nil {
		return "", err
	}
	for i := start; i < end; i++ {
		lines[i] = commentConfigLine(lines[i])
	}
	return strings.Join(lines, ""), nil
}

func findFontBlockLines(lines []string) (int, int, error) {
	start := -1
	re := regexp.MustCompile(`^\s*(//\s*)?"font"(\s|//|$)`)
	for i, line := range lines {
		if re.MatchString(lineWithoutLineBreak(line)) {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, errors.New("config.vdf 中未找到 font 配置块")
	}
	depth := 0
	seenOpen := false
	for i := start; i < len(lines); i++ {
		body := uncommentConfigLine(lineWithoutLineBreak(lines[i]))
		for _, r := range body {
			switch r {
			case '{':
				depth++
				seenOpen = true
			case '}':
				if seenOpen {
					depth--
					if depth == 0 {
						return start, i + 1, nil
					}
				}
			}
		}
	}
	return 0, 0, errors.New("config.vdf 中 font 配置块不完整")
}

func fontBlockIsCommented(lines []string) bool {
	for _, line := range lines {
		body := strings.TrimSpace(lineWithoutLineBreak(line))
		if body == "" {
			continue
		}
		return strings.HasPrefix(body, `// "font"`)
	}
	return false
}

func commentConfigLine(line string) string {
	body, br := splitLineBreak(line)
	if strings.TrimSpace(body) == "" {
		return line
	}
	indentLen := len(body) - len(strings.TrimLeft(body, " \t"))
	return body[:indentLen] + "// " + body[indentLen:] + br
}

func uncommentConfigLine(line string) string {
	body, br := splitLineBreak(line)
	re := regexp.MustCompile(`^([ \t]*)// ?(.*)$`)
	if m := re.FindStringSubmatch(body); len(m) == 3 {
		return m[1] + m[2] + br
	}
	return line
}

func lineWithoutLineBreak(line string) string {
	body, _ := splitLineBreak(line)
	return body
}

func splitLineBreak(line string) (string, string) {
	if strings.HasSuffix(line, "\r\n") {
		return strings.TrimSuffix(line, "\r\n"), "\r\n"
	}
	if strings.HasSuffix(line, "\n") {
		return strings.TrimSuffix(line, "\n"), "\n"
	}
	if strings.HasSuffix(line, "\r") {
		return strings.TrimSuffix(line, "\r"), "\r"
	}
	return line, ""
}

func browseFontFile() (string, error) {
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if hr == 0 || hr == 1 {
		defer procCoUninitialize.Call()
	}

	filterBuf := utf16DoubleNull([]string{
		"字体文件 (*.ttf;*.otf;*.ttc;*.fon)", "*.ttf;*.otf;*.ttc;*.fon",
		"所有文件 (*.*)", "*.*",
	})
	fileBuf := make([]uint16, 32768)
	ofn := openFileName{
		lStructSize: uint32(unsafe.Sizeof(openFileName{})),
		hwndOwner:   hWnd,
		lpstrFilter: &filterBuf[0],
		lpstrFile:   &fileBuf[0],
		nMaxFile:    uint32(len(fileBuf)),
		lpstrTitle:  utf16Ptr("选择要安装的字体文件"),
		flags:       0x00080000 | 0x00001000 | 0x00000800 | 0x00000008,
	}
	ok, _, err := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		code, _, _ := procCommDlgExtendedError.Call()
		if code != 0 {
			return "", fmt.Errorf("打开字体文件窗口失败，错误码: 0x%X", code)
		}
		if err != syscall.Errno(0) {
			appendLog("[font] file dialog cancelled or closed: " + err.Error())
		}
		return "", nil
	}
	return syscall.UTF16ToString(fileBuf), nil
}

func installFontFile(path string) error {
	if path == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".ttf" && ext != ".otf" && ext != ".ttc" && ext != ".fon" {
		return fmt.Errorf("不支持的字体文件类型: %s", ext)
	}
	fontDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")
	if err := os.MkdirAll(fontDir, 0755); err != nil {
		return err
	}
	dst := filepath.Join(fontDir, filepath.Base(path))
	if exists(dst) {
		appendLog("[font] font file already installed; skipped copy: " + dst)
	} else if err := copyFile(path, dst); err != nil {
		return err
	}
	if err := registerUserFont(dst); err != nil {
		appendLog("[font] registry skipped: " + err.Error())
	}
	added, _, _ := procAddFontResourceExW.Call(uintptr(unsafe.Pointer(utf16Ptr(dst))), 0, 0)
	if added == 0 && !exists(dst) {
		return errors.New("字体安装失败")
	}
	procPostMessageW.Call(hwndBroadcast, wmFontChange, 0, 0)
	appendLog("[font] font installed or already available: " + dst)
	return nil
}

func registerUserFont(path string) error {
	var key uintptr
	r, _, _ := procRegCreateKeyExW.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(utf16Ptr(`Software\Microsoft\Windows NT\CurrentVersion\Fonts`))),
		0, 0, 0, 0x20006, 0,
		uintptr(unsafe.Pointer(&key)),
		0,
	)
	if r != 0 {
		return fmt.Errorf("RegCreateKeyExW failed: %d", r)
	}
	defer procRegCloseKey.Call(key)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	valueName := base + " (TrueType)"
	data := append(syscall.StringToUTF16(path), 0)
	r, _, _ = procRegSetValueExW.Call(
		key,
		uintptr(unsafe.Pointer(utf16Ptr(valueName))),
		0,
		regSz,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)*2),
	)
	if r != 0 {
		return fmt.Errorf("RegSetValueExW failed: %d", r)
	}
	return nil
}

func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func relativeFiles(root string) ([]string, error) {
	root = clean(root)
	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func hasAnyRelativeFile(root string, rels []string) bool {
	for _, rel := range rels {
		if exists(filepath.Join(root, rel)) {
			return true
		}
	}
	return false
}

func resolveGameExe(packageRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return resolveGameExeContext(ctx, packageRoot)
}

func resolveGameExeContext(ctx context.Context, packageRoot string) (string, error) {
	for _, root := range steamRoots("") {
		if ctx.Err() != nil {
			return "", errors.New("定位游戏目录超时")
		}
		if found := findGameExeFromSteamRoot(root, packageRoot); found != "" {
			return found, nil
		}
	}
	if found := searchEverything(ctx, packageRoot); found != "" {
		return found, nil
	}
	if found := searchCommonDirs(ctx, packageRoot); found != "" {
		return found, nil
	}
	return "", errors.New("无法自动定位真实 left4dead2.exe")
}

func findGameExeFromSteamRoot(root, packageRoot string) string {
	for _, lib := range steamLibraries(root) {
		manifestPath := filepath.Join(lib, "steamapps", "appmanifest_550.acf")
		installDir := "Left 4 Dead 2"
		if data, err := os.ReadFile(manifestPath); err == nil {
			if m := regexp.MustCompile(`"installdir"\s+"([^"]+)"`).FindStringSubmatch(string(data)); len(m) == 2 {
				installDir = m[1]
			}
			candidate := filepath.Join(lib, "steamapps", "common", installDir, "left4dead2.exe")
			if isRealGameExe(candidate, packageRoot) {
				return candidate
			}
		}
		fallback := filepath.Join(lib, "steamapps", "common", "Left 4 Dead 2", "left4dead2.exe")
		if isRealGameExe(fallback, packageRoot) {
			return fallback
		}
	}
	return ""
}

func steamRoots(extra string) []string {
	var roots []string
	if extra != "" {
		roots = append(roots, extra)
	}
	for _, key := range []struct {
		root uintptr
		path string
	}{
		{hkeyCurrentUser, `Software\Valve\Steam`},
		{hkeyLocalMachine, `SOFTWARE\WOW6432Node\Valve\Steam`},
		{hkeyLocalMachine, `SOFTWARE\Valve\Steam`},
	} {
		for _, name := range []string{"SteamPath", "InstallPath"} {
			if v := regString(key.root, key.path, name); v != "" {
				roots = append(roots, v)
			}
		}
	}
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, filepath.Join(v, "Steam"))
		}
	}
	return uniqueExistingDirs(roots)
}

func steamLibraries(root string) []string {
	libs := []string{root}
	data, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
	if err == nil {
		for _, m := range regexp.MustCompile(`"path"\s+"([^"]+)"`).FindAllStringSubmatch(string(data), -1) {
			libs = append(libs, strings.ReplaceAll(m[1], `\\`, `\`))
		}
	}
	return uniqueExistingDirs(libs)
}

func searchEverything(ctx context.Context, packageRoot string) string {
	resRoot := resourceRoot(packageRoot)
	exe := filepath.Join(resRoot, "tools", "Everything", "everything.exe")
	if exists(exe) {
		cmd := exec.Command(exe, "-startup")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Start()
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return ""
		}
	}
	for _, es := range []string{
		filepath.Join(resRoot, "tools", "Everything", "es.exe"),
		filepath.Join(resRoot, "tools", "es.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Everything", "es.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Everything", "es.exe"),
	} {
		if !exists(es) {
			continue
		}
		if ctx.Err() != nil {
			return ""
		}
		appendLog("[search] Everything CLI: " + es)
		cmdCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		cmd := exec.CommandContext(cmdCtx, es, "-n", "50", "left4dead2.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			path := strings.TrimSpace(line)
			if isRealGameExe(path, packageRoot) {
				return path
			}
		}
	}
	return ""
}

func searchCommonDirs(ctx context.Context, packageRoot string) string {
	var roots []string
	for _, steam := range steamRoots("") {
		for _, lib := range steamLibraries(steam) {
			roots = append(roots, filepath.Join(lib, "steamapps", "common"))
		}
	}
	for c := 'A'; c <= 'Z'; c++ {
		drive := fmt.Sprintf("%c:\\", c)
		for _, rel := range []string{`SteamLibrary\steamapps\common`, `Program Files (x86)\Steam\steamapps\common`, `Program Files\Steam\steamapps\common`} {
			roots = append(roots, filepath.Join(drive, rel))
		}
	}
	for _, root := range uniqueExistingDirs(roots) {
		if ctx.Err() != nil {
			return ""
		}
		var found string
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if err != nil || d.IsDir() || !strings.EqualFold(d.Name(), "left4dead2.exe") {
				return nil
			}
			if isRealGameExe(path, packageRoot) {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

func isRealGameExe(path, packageRoot string) bool {
	if path == "" || !exists(path) {
		return false
	}
	full := clean(path)
	pkg := clean(packageRoot)
	if strings.HasPrefix(strings.ToLower(full), strings.ToLower(pkg)) {
		return false
	}
	parent := filepath.Dir(full)
	return exists(filepath.Join(parent, "left4dead2", "pak01_dir.vpk")) ||
		exists(filepath.Join(parent, "left4dead2", "gameinfo.txt")) ||
		strings.Contains(strings.ToLower(full), `\steamapps\common\left 4 dead 2\left4dead2.exe`)
}

func regString(root uintptr, path, name string) string {
	var key uintptr
	r, _, _ := procRegOpenKeyExW.Call(root, uintptr(unsafe.Pointer(utf16Ptr(path))), 0, keyRead, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return ""
	}
	defer procRegCloseKey.Call(key)
	var typ uint32
	var size uint32
	r, _, _ = procRegQueryValueExW.Call(key, uintptr(unsafe.Pointer(utf16Ptr(name))), 0, uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&size)))
	if r != 0 || size == 0 {
		return ""
	}
	buf := make([]uint16, size/2+1)
	r, _, _ = procRegQueryValueExW.Call(key, uintptr(unsafe.Pointer(utf16Ptr(name))), 0, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return ""
	}
	return strings.TrimRight(syscall.UTF16ToString(buf), "\x00")
}

func copyDirContents(srcRoot, dstRoot string, progressStart, progressSpan int) error {
	srcRoot = clean(srcRoot)
	dstRoot = clean(dstRoot)
	var files []string
	if err := filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil || rel == "." {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return os.MkdirAll(dstRoot, 0755)
	}
	for i, src := range files {
		rel, err := filepath.Rel(srcRoot, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if err := copyFile(src, dst); err != nil {
			return err
		}
		if info, err := os.Stat(src); err == nil {
			_ = os.Chmod(dst, info.Mode().Perm())
			_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
		}
		setProgress(progressStart + i*progressSpan/max(1, len(files)))
	}
	appendLog(fmt.Sprintf("[copy] %d files copied", len(files)))
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func removeEmptyParents(dir, stop string) {
	dir = clean(dir)
	stop = clean(stop)
	for dir != "" && !strings.EqualFold(dir, stop) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}

func packageRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func resourceRoot(exeRoot string) string {
	res := filepath.Join(exeRoot, resourceDirName)
	if exists(res) {
		return res
	}
	return exeRoot
}

func uniqueExistingDirs(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" || !exists(p) {
			continue
		}
		c := clean(p)
		k := strings.ToLower(c)
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func clean(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func utf16DoubleNull(parts []string) []uint16 {
	var out []uint16
	for _, part := range parts {
		out = append(out, syscall.StringToUTF16(part)...)
	}
	out = append(out, 0)
	return out
}

func uint16PtrFromID(id uint16) *uint16 {
	return (*uint16)(unsafe.Pointer(uintptr(id)))
}
