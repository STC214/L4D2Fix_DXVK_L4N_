//go:build windows

package main

import (
	"context"
	"encoding/json"
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
	appTitle                     = "L4N"
	defaultLaunchOptions         = "-heapsize 2097152 -processheap -high -novid -nojoy -steam -lv -vulkan"
	resourceDirName              = "resources"
	genericPatchDirName          = "L4N_dxvk2.7.1"
	dxvkVersionsDirName          = "dxvk其他版本"
	modBackupDirName             = "addons_backup"
	displaySettingsBackupDirName = "display_settings_backup"
	videoSettingsRelativePath    = "left4dead2/cfg/video.txt"
	configRelativePath           = "left4dead2/neko/config.vdf"
	usageInstructions            = "使用步骤\r\n1. 先关闭 Steam 和游戏\r\n2. 在 DXVK版本 中选择要安装的版本\r\n3. 点击 一键处理\r\n4. 备份MOD：保存 addons 和显示设置\r\n5. 恢复MOD：还原 MOD 和显示设置\r\n6. 系统字体/游戏默认：切换 config.vdf 中 font 配置块\r\n7. 一键清理：按 .l4n_auto_backup 还原补丁和 Steam 配置\r\n\r\nSteam 启动项\r\n-heapsize 2097152 -processheap -high -novid -nojoy -steam -lv -vulkan\r\n\r\n验证\r\nmat_info -> ShaderAPI: shaderapivk\r\nmem_dump -> 2,048.00MB"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procLoadIconW        = user32.NewProc("LoadIconW")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procSendMessageW     = user32.NewProc("SendMessageW")
	procSetWindowTextW   = user32.NewProc("SetWindowTextW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procUpdateWindow     = user32.NewProc("UpdateWindow")
	procShowWindow       = user32.NewProc("ShowWindow")
	procEnableWindow     = user32.NewProc("EnableWindow")
	procGetDlgCtrlID     = user32.NewProc("GetDlgCtrlID")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procGetStockObject   = gdi32.NewProc("GetStockObject")
	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procCreateFontW      = gdi32.NewProc("CreateFontW")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
	procSelectObject     = gdi32.NewProc("SelectObject")
	procSetBkMode        = gdi32.NewProc("SetBkMode")
	procSetTextColor     = gdi32.NewProc("SetTextColor")

	procInitCommonControls   = comctl32.NewProc("InitCommonControls")
	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	procDrawTextW            = user32.NewProc("DrawTextW")
	procFillRect             = user32.NewProc("FillRect")

	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")

	hInstance     uintptr
	hWnd          uintptr
	btnRun        uintptr
	comboDxvk     uintptr
	btnBackupMod  uintptr
	btnRestoreMod uintptr
	btnClean      uintptr
	btnClose      uintptr
	progress      uintptr
	statusCtl     uintptr
	logCtl        uintptr
	blackBr       uintptr
	textFont      uintptr
	titleFont     uintptr
	buttonFont    uintptr
	guideFont     uintptr
	busyMu        sync.Mutex
	busy          bool
	progressMu    sync.Mutex
	lastProgress  int
	uiMu          sync.Mutex
	uiNext        uintptr
	uiWork        = map[uintptr]func(){}
	dxvkOptions   []dxvkOption
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
	wmAppInvoke      = 0x8001
	wmSetIcon        = 0x0080

	bnClicked = 0

	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVisible     = 0x10000000
	wsChild       = 0x40000000
	wsTabStop     = 0x00010000
	wsBorder      = 0x00800000
	bsOwnerDraw   = 0x0000000B
	cbsDropList   = 0x0003
	cbsHasStrings = 0x0200
	esMultiline   = 0x0004
	esAutovScroll = 0x0040
	esReadOnly    = 0x0800
	wsVScroll     = 0x00200000

	cbs = wsCaption | wsSysMenu | wsMinimizeBox

	idRun        = 1001
	idClean      = 1002
	idClose      = 1003
	idBackupMod  = 1005
	idRestoreMod = 1006

	cbAddString   = 0x0143
	cbGetCurSel   = 0x0147
	cbSetCurSel   = 0x014E
	pbmSetRange32 = 0x0400 + 6
	pbmSetPos     = 0x0400 + 2
	emSetSel      = 0x00B1
	emReplaceSel  = 0x00C2

	colorBlack         = 0x0f0f0f
	colorWhite         = 0x00ffffff
	colorButton        = 0x24201c
	colorButtonPressed = 0x3b332c
	colorAccent        = 0x00d38a1f

	defaultGuiFont   = 17
	transparent      = 1
	imageIcon        = 1
	iconSmall        = 0
	iconBig          = 1
	lrDefaultColor   = 0
	iccProgressClass = 0x00000020

	dtCenter     = 0x00000001
	dtVCenter    = 0x00000004
	dtSingleLine = 0x00000020

	odsSelected = 0x0001

	hkeyCurrentUser  = 0x80000001
	hkeyLocalMachine = 0x80000002
	keyRead          = 0x20019
)

type wchar uint16

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

type manifest struct {
	CreatedAt     string       `json:"createdAt"`
	GameRoot      string       `json:"gameRoot"`
	Files         []fileEntry  `json:"files"`
	SteamConfigs  []steamEntry `json:"steamConfigs"`
	LaunchOptions string       `json:"launchOptions"`
}

type fileEntry struct {
	Target  string `json:"target"`
	Rel     string `json:"relative"`
	Existed bool   `json:"existed"`
	Backup  string `json:"backup,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	ModTime string `json:"modTime,omitempty"`
}

type steamEntry struct {
	Target  string `json:"target"`
	Existed bool   `json:"existed"`
	Backup  string `json:"backup"`
}

type dxvkOption struct {
	Name string
	Dir  string
}

type patchFile struct {
	Src string
	Rel string
}

func main() {
	runtime.LockOSThread()
	hInstance, _, _ = procGetModuleHandleW.Call(0)
	procInitCommonControls.Call()
	icc := initCommonControlsEx{
		dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		dwICC:  iccProgressClass,
	}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	blackBr, _, _ = procCreateSolidBrush.Call(colorBlack)
	textFont = createFont(16, 400)
	titleFont = createFont(18, 600)
	buttonFont = createFont(16, 600)
	guideFont = createFont(14, 400)

	className := utf16Ptr("L4NFixWindow")
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
		960, 560,
		0, 0, hInstance, 0,
	)
	procSendMessageW.Call(hWnd, wmSetIcon, iconBig, iconBigHandle)
	procSendMessageW.Call(hWnd, wmSetIcon, iconSmall, iconSmallHandle)
	procShowWindow.Call(hWnd, swShow)
	procUpdateWindow.Call(hWnd)

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
			case idRun:
				go guarded("一键处理", runInstall)
			case idBackupMod:
				go guarded("备份MOD", runBackupMods)
			case idRestoreMod:
				go guarded("恢复MOD", runRestoreMods)
			case idClean:
				go guarded("一键清理", runRestore)
			case idClose:
				go guarded("切换字体", runToggleFont)
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
		for _, font := range []uintptr{textFont, titleFont, buttonFont, guideFont} {
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
	title := label(hwnd, "DXVK + L4N 一键处理工具", 22, 28, 300, 26)
	procSendMessageW.Call(title, wmSetFont, titleFont, 1)
	desc := label(hwnd, "自动识别游戏目录，安装运行库，备份原文件，\r\n并用所选补丁目录覆盖游戏源文件。", 22, 62, 594, 48)
	procSendMessageW.Call(desc, wmSetFont, textFont, 1)

	versionLabel := label(hwnd, "DXVK版本", 22, 116, 174, 20)
	procSendMessageW.Call(versionLabel, wmSetFont, textFont, 1)
	comboDxvk = create("COMBOBOX", "", wsChild|wsVisible|wsTabStop|wsVScroll|cbsDropList|cbsHasStrings, 22, 140, 174, 220, hwnd, 0)
	procSendMessageW.Call(comboDxvk, wmSetFont, textFont, 1)
	btnRun = button(hwnd, "一键处理", idRun, 22, 184, 174, 42)
	btnBackupMod = button(hwnd, "备份MOD", idBackupMod, 232, 122, 174, 42)
	btnRestoreMod = button(hwnd, "恢复MOD", idRestoreMod, 232, 174, 174, 42)
	btnClean = button(hwnd, "一键清理", idClean, 442, 122, 174, 42)
	btnClose = button(hwnd, "系统字体/游戏默认", idClose, 442, 174, 174, 42)
	for _, h := range []uintptr{btnRun, btnBackupMod, btnRestoreMod, btnClean, btnClose} {
		procSendMessageW.Call(h, wmSetFont, buttonFont, 1)
	}
	loadDxvkOptionsIntoCombo()

	progress = create("msctls_progress32", "", wsChild|wsVisible, 22, 242, 594, 17, hwnd, 0)
	procSendMessageW.Call(progress, pbmSetRange32, 0, 100)
	procSendMessageW.Call(progress, pbmSetPos, 0, 0)

	statusCtl = label(hwnd, "就绪 - 请选择 DXVK 版本后一键处理", 22, 273, 594, 24)
	procSendMessageW.Call(statusCtl, wmSetFont, textFont, 1)
	logTitle := label(hwnd, "日志", 22, 303, 80, 20)
	procSendMessageW.Call(logTitle, wmSetFont, textFont, 1)
	logCtl = create("EDIT", "", wsChild|wsVisible|wsBorder|esMultiline|esAutovScroll|esReadOnly|wsVScroll, 22, 322, 594, 194, hwnd, 0)
	procSendMessageW.Call(logCtl, wmSetFont, textFont, 1)

	guideTitle := label(hwnd, "使用说明", 630, 22, 280, 24)
	procSendMessageW.Call(guideTitle, wmSetFont, titleFont, 1)
	guideCtl := create("EDIT", usageInstructions, wsChild|wsVisible|wsBorder|esMultiline|esReadOnly, 630, 50, 300, 466, hwnd, 0)
	procSendMessageW.Call(guideCtl, wmSetFont, guideFont, 1)

	appendLog("[prepare] ready; resources directory: " + resourceDirName)
	appendLog("[prepare] L4N base patch: resources\\" + genericPatchDirName)
	appendLog("[prepare] DXVK versions directory: " + dxvkVersionsDirName)
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

func createFont(height int32, weight int32) uintptr {
	font, _, _ := procCreateFontW.Call(
		uintptr(^uint32(uint32(height-1))),
		0, 0, 0,
		uintptr(weight),
		0, 0, 0,
		1,
		0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16Ptr("Segoe UI"))),
	)
	if font == 0 {
		font, _, _ = procGetStockObject.Call(defaultGuiFont)
	}
	return font
}

func drawButton(dis *drawItemStruct) {
	if dis == nil {
		return
	}
	bg := uintptr(colorButton)
	if dis.itemState&odsSelected != 0 {
		bg = colorButtonPressed
	}
	brush, _, _ := procCreateSolidBrush.Call(bg)
	procFillRect.Call(dis.hdc, uintptr(unsafe.Pointer(&dis.rcItem)), brush)
	procDeleteObject.Call(brush)

	accentBrush, _, _ := procCreateSolidBrush.Call(colorAccent)
	accent := dis.rcItem
	accent.bottom = accent.top + 3
	procFillRect.Call(dis.hdc, uintptr(unsafe.Pointer(&accent)), accentBrush)
	procDeleteObject.Call(accentBrush)

	oldFont, _, _ := procSelectObject.Call(dis.hdc, buttonFont)
	procSetBkMode.Call(dis.hdc, transparent)
	procSetTextColor.Call(dis.hdc, colorWhite)
	text := buttonText(dis.ctrlID)
	rc := dis.rcItem
	rc.top += 3
	procDrawTextW.Call(dis.hdc, uintptr(unsafe.Pointer(utf16Ptr(text))), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), dtCenter|dtVCenter|dtSingleLine)
	if oldFont != 0 {
		procSelectObject.Call(dis.hdc, oldFont)
	}
}

func buttonText(id uint32) string {
	switch id {
	case idRun:
		return "一键处理"
	case idBackupMod:
		return "备份MOD"
	case idRestoreMod:
		return "恢复MOD"
	case idClean:
		return "一键清理"
	case idClose:
		return "系统字体/游戏默认"
	default:
		return ""
	}
}

func loadDxvkOptionsIntoCombo() {
	root, err := packageRoot()
	if err != nil {
		appendLog("[prepare] " + err.Error())
		return
	}
	resRoot := resourceRoot(root)
	dxvkOptions = discoverDxvkOptions(root, resRoot)
	if len(dxvkOptions) == 0 {
		appendLog("[prepare] no DXVK versions found under " + dxvkVersionsDirName)
		return
	}
	for _, opt := range dxvkOptions {
		procSendMessageW.Call(comboDxvk, cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(opt.Name))))
	}
	procSendMessageW.Call(comboDxvk, cbSetCurSel, uintptr(len(dxvkOptions)-1), 0)
	appendLog(fmt.Sprintf("[prepare] loaded %d DXVK versions", len(dxvkOptions)))
}

func selectedDxvkOption() (dxvkOption, error) {
	if len(dxvkOptions) == 0 {
		return dxvkOption{}, errors.New("未找到可用 DXVK 版本")
	}
	ret, _, _ := procSendMessageW.Call(comboDxvk, cbGetCurSel, 0, 0)
	idx := int(ret)
	if idx < 0 || idx >= len(dxvkOptions) {
		return dxvkOption{}, errors.New("请选择 DXVK 版本")
	}
	return dxvkOptions[idx], nil
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
		procEnableWindow.Call(btnRun, en)
		procEnableWindow.Call(comboDxvk, en)
		procEnableWindow.Call(btnBackupMod, en)
		procEnableWindow.Call(btnRestoreMod, en)
		procEnableWindow.Call(btnClean, en)
		procEnableWindow.Call(btnClose, en)
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

func runInstall() error {
	opt, err := selectedDxvkOption()
	if err != nil {
		return err
	}
	return runInstallWithDxvk(opt)
}

func runBackupMods() error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	resRoot := resourceRoot(root)
	gameExe, err := resolveGameExe(root)
	if err != nil {
		return err
	}
	addonsDir := filepath.Join(filepath.Dir(gameExe), "left4dead2", "addons")
	if !exists(addonsDir) {
		return fmt.Errorf("未找到 addons 目录: %s", addonsDir)
	}
	backupDir := filepath.Join(resRoot, modBackupDirName)
	tmpBackupDir := backupDir + ".tmp"
	appendLog("[mod] source addons: " + addonsDir)
	appendLog("[mod] backup target: " + backupDir)
	setProgress(10)
	if err := os.RemoveAll(tmpBackupDir); err != nil {
		return err
	}
	if err := copyDirContents(addonsDir, tmpBackupDir, 10, 85); err != nil {
		_ = os.RemoveAll(tmpBackupDir)
		return err
	}
	if err := replaceDir(tmpBackupDir, backupDir); err != nil {
		_ = os.RemoveAll(tmpBackupDir)
		return err
	}
	setProgress(88)
	if err := backupDisplaySettings(filepath.Dir(gameExe), resRoot); err != nil {
		appendLog("[display] " + err.Error())
	}
	setProgress(95)
	appendLog("[mod] addons backup complete")
	return nil
}

func runRestoreMods() error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	resRoot := resourceRoot(root)
	backupDir := filepath.Join(resRoot, modBackupDirName)
	if !exists(backupDir) {
		return fmt.Errorf("未找到 MOD 备份目录: %s", backupDir)
	}
	gameExe, err := resolveGameExe(root)
	if err != nil {
		return err
	}
	addonsDir := filepath.Join(filepath.Dir(gameExe), "left4dead2", "addons")
	appendLog("[mod] backup source: " + backupDir)
	appendLog("[mod] restore target addons: " + addonsDir)
	appendLog("[mod] existing files with the same name will be overwritten")
	setProgress(10)
	if err := copyDirContents(backupDir, addonsDir, 10, 85); err != nil {
		return err
	}
	setProgress(88)
	if err := restoreDisplaySettings(filepath.Dir(gameExe), resRoot); err != nil {
		appendLog("[display] " + err.Error())
	}
	setProgress(95)
	appendLog("[mod] addons restore complete")
	return nil
}

func runToggleFont() error {
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
		return errors.New("没有进行一键处理，一键处理后再次点击本按钮")
	}

	systemFont := systemDefaultFont()
	appendLog("[font] config: " + configPath)
	appendLog("[font] system default font: " + systemFont)
	setProgress(10)
	mode, err := toggleConfigFont(configPath, systemFont)
	if err != nil {
		return err
	}
	setProgress(95)
	if mode == "system" {
		appendLog("[font] switched to system font")
	} else {
		appendLog("[font] switched to game default")
	}
	return nil
}

func runInstallWithDxvk(opt dxvkOption) error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	resRoot := resourceRoot(root)
	appendLog("[prepare] exe root: " + root)
	appendLog("[prepare] resource root: " + resRoot)
	appendLog("[prepare] selected DXVK: " + opt.Name)
	runtimeDir := resRoot
	patchDir, err := findNamedPackageDir(resRoot, genericPatchDirName)
	if err != nil {
		return err
	}
	patchFiles, err := buildPatchFileList(patchDir, opt)
	if err != nil {
		return err
	}
	launchOptions := readLaunchOptions(resRoot)

	setProgress(5)
	if err := installRuntimes(runtimeDir); err != nil {
		appendLog("[runtime] " + err.Error())
	}

	setProgress(25)
	gameExe, err := resolveGameExe(root)
	if err != nil {
		return err
	}
	gameRoot := filepath.Dir(gameExe)
	appendLog("[steam] game: " + gameExe)

	backupRoot := filepath.Join(root, ".l4n_auto_backup")
	man := loadManifest(backupRoot, gameRoot)
	man.LaunchOptions = launchOptions
	appendLog("[backup] manifest: " + filepath.Join(backupRoot, "manifest.json"))

	setProgress(40)
	if err := copyPatchFiles(man, backupRoot, gameRoot, patchFiles); err != nil {
		return err
	}

	setProgress(78)
	if err := setSteamLaunchOptions(man, backupRoot, launchOptions); err != nil {
		appendLog("[steam] " + err.Error())
	}

	setProgress(92)
	if err := saveManifest(man, backupRoot); err != nil {
		return err
	}
	appendLog("[verify] 游戏内可用 mat_info / mem_dump 验证")
	return nil
}

func runRestore() error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	backupRoot := filepath.Join(root, ".l4n_auto_backup")
	manifestPath := filepath.Join(backupRoot, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("未找到备份清单: %s", manifestPath)
	}
	var man manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return err
	}
	var restoreErrs []string
	setProgress(15)
	for i, e := range man.Files {
		if e.Existed {
			if e.Backup == "" {
				msg := "missing backup path: " + e.Target
				restoreErrs = append(restoreErrs, msg)
				appendLog("[restore] " + msg)
				continue
			}
			if err := copyFile(e.Backup, e.Target); err != nil {
				restoreErrs = append(restoreErrs, err.Error())
				appendLog("[restore] " + err.Error())
			} else {
				restoreFileMetadata(e)
				appendLog("[restore] " + e.Rel)
			}
		} else if exists(e.Target) {
			if err := os.Remove(e.Target); err != nil {
				restoreErrs = append(restoreErrs, err.Error())
				appendLog("[remove] " + err.Error())
			} else {
				removeEmptyParents(filepath.Dir(e.Target), man.GameRoot)
				appendLog("[remove] " + e.Rel)
			}
		}
		setProgress(15 + i*55/max(1, len(man.Files)))
	}
	for _, e := range man.SteamConfigs {
		if e.Existed && exists(e.Backup) {
			if err := copyFile(e.Backup, e.Target); err != nil {
				restoreErrs = append(restoreErrs, err.Error())
				appendLog("[steam] " + err.Error())
			} else {
				appendLog("[steam] restored: " + e.Target)
			}
		} else if e.Existed {
			msg := "missing Steam config backup: " + e.Target
			restoreErrs = append(restoreErrs, msg)
			appendLog("[steam] " + msg)
		}
	}
	setProgress(85)
	if len(restoreErrs) > 0 {
		appendLog("[clean] backup directory kept because restore had errors")
		return fmt.Errorf("restore incomplete; kept backup directory: %s", backupRoot)
	}
	if err := os.RemoveAll(backupRoot); err != nil {
		return err
	}
	appendLog("[clean] removed backup directory")
	return nil
}

func installRuntimes(root string) error {
	appendLog("[runtime] installing VC++ runtimes")
	for _, name := range []string{"VC_redist.x86.exe", "VC_redist.x64.exe"} {
		path := filepath.Join(root, name)
		if !exists(path) {
			appendLog("[runtime] skipped missing: " + name)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		cmd := exec.CommandContext(ctx, path, "/install", "/quiet", "/norestart")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		err := cmd.Run()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			appendLog("[runtime] timeout; continuing after: " + name)
			continue
		}
		code := exitCode(err)
		switch {
		case err == nil:
			appendLog("[runtime] ok: " + name)
		case code == 1638 || code == 3010 || code == 5100:
			appendLog(fmt.Sprintf("[runtime] skipped %s, exit code %d", name, code))
		default:
			appendLog(fmt.Sprintf("[runtime] continue after %s, exit code %d", name, code))
		}
	}
	return nil
}

func copyPatchFiles(man *manifest, backupRoot, gameRoot string, files []patchFile) error {
	appendLog("[copy] target game directory: " + gameRoot)
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Rel) < strings.ToLower(files[j].Rel)
	})
	for i, file := range files {
		dst := filepath.Join(gameRoot, file.Rel)
		if err := copyWithBackup(man, backupRoot, gameRoot, file.Src, dst); err != nil {
			return err
		}
		if i%5 == 0 {
			setProgress(40 + i*35/max(1, len(files)))
		}
	}
	appendLog(fmt.Sprintf("[copy] %d patch files copied over the game files with backup", len(files)))
	return nil
}

func buildPatchFileList(basePatchDir string, opt dxvkOption) ([]patchFile, error) {
	appendLog("[copy] source L4N base directory: " + basePatchDir)
	appendLog("[copy] source DXVK directory: " + opt.Dir)
	files, err := collectBasePatchFiles(basePatchDir)
	if err != nil {
		return nil, err
	}
	dxvkFiles, err := collectDxvkPatchFiles(opt.Dir)
	if err != nil {
		return nil, err
	}
	files = append(files, dxvkFiles...)
	return files, nil
}

func collectBasePatchFiles(basePatchDir string) ([]patchFile, error) {
	var files []patchFile
	err := filepath.WalkDir(basePatchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(basePatchDir, path)
		if err != nil {
			return err
		}
		if isDxvkTargetRel(rel) {
			return nil
		}
		files = append(files, patchFile{Src: path, Rel: rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func collectDxvkPatchFiles(versionDir string) ([]patchFile, error) {
	x32, err := findDxvkX32Dir(versionDir)
	if err != nil {
		return nil, err
	}
	dxgi := filepath.Join(x32, "dxgi.dll")
	d3d9 := filepath.Join(x32, "dxvk_d3d9.dll")
	if !exists(dxgi) || !exists(d3d9) {
		return nil, fmt.Errorf("DXVK x32 directory is incomplete: %s", x32)
	}
	binD3D9 := filepath.Join(x32, "bin", "dxvk_d3d9.dll")
	if !exists(binD3D9) {
		binD3D9 = d3d9
	}
	return []patchFile{
		{Src: dxgi, Rel: "dxgi.dll"},
		{Src: d3d9, Rel: "dxvk_d3d9.dll"},
		{Src: binD3D9, Rel: filepath.Join("bin", "dxvk_d3d9.dll")},
	}, nil
}

func findDxvkX32Dir(versionDir string) (string, error) {
	var matches []string
	err := filepath.WalkDir(versionDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() || !strings.EqualFold(d.Name(), "x32") {
			return nil
		}
		if exists(filepath.Join(path, "dxgi.dll")) && exists(filepath.Join(path, "dxvk_d3d9.dll")) {
			matches = append(matches, path)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("未找到有效 DXVK x32 目录: %s", versionDir)
	}
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i]) < len(matches[j])
	})
	return matches[0], nil
}

func isDxvkTargetRel(rel string) bool {
	rel = filepath.ToSlash(strings.ToLower(filepath.Clean(rel)))
	switch rel {
	case "dxgi.dll", "dxvk_d3d9.dll", "bin/dxvk_d3d9.dll":
		return true
	default:
		return false
	}
}

func copyWithBackup(man *manifest, backupRoot, gameRoot, src, dst string) error {
	if !manifestHasFile(man, dst) {
		rel, _ := filepath.Rel(gameRoot, dst)
		entry := fileEntry{Target: dst, Rel: rel, Existed: exists(dst)}
		if entry.Existed {
			info, err := os.Stat(dst)
			if err != nil {
				return err
			}
			entry.Mode = uint32(info.Mode().Perm())
			entry.ModTime = info.ModTime().Format(time.RFC3339Nano)
			entry.Backup = filepath.Join(backupRoot, "files", rel)
			if err := copyFile(dst, entry.Backup); err != nil {
				return err
			}
			restoreBackupMetadata(entry)
		}
		man.Files = append(man.Files, entry)
		if err := saveManifest(man, backupRoot); err != nil {
			return err
		}
	}
	return copyFile(src, dst)
}

func manifestHasFile(man *manifest, target string) bool {
	target = clean(target)
	for _, e := range man.Files {
		if strings.EqualFold(clean(e.Target), target) {
			return true
		}
	}
	return false
}

func setSteamLaunchOptions(man *manifest, backupRoot, options string) error {
	appendLog("[steam] writing launch options")
	if processExists("steam.exe") {
		appendLog("[steam] Steam 正在运行；若退出时覆盖配置，请关闭 Steam 后重新执行")
	}
	roots := steamRoots("")
	updated := 0
	for _, root := range roots {
		userdata := filepath.Join(root, "userdata")
		if !exists(userdata) {
			continue
		}
		filepath.WalkDir(userdata, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(d.Name(), "localconfig.vdf") {
				return nil
			}
			textBytes, err := os.ReadFile(path)
			if err != nil {
				appendLog("[steam] read failed: " + path)
				return nil
			}
			old := string(textBytes)
			newText, err := setAppLaunchOptionsInText(old, options)
			if err != nil {
				appendLog("[steam] " + err.Error())
				return nil
			}
			if newText != old {
				if err := backupSteamConfig(man, backupRoot, path); err != nil {
					appendLog("[steam] backup failed: " + err.Error())
					return nil
				}
				if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
					appendLog("[steam] write failed: " + err.Error())
					return nil
				}
				updated++
				appendLog("[steam] updated: " + path)
			}
			return nil
		})
	}
	if updated == 0 {
		return fmt.Errorf("没有找到可写入的 Steam 配置；请手动设置启动项: %s", options)
	}
	return nil
}

func backupSteamConfig(man *manifest, backupRoot, path string) error {
	for _, e := range man.SteamConfigs {
		if strings.EqualFold(clean(e.Target), clean(path)) {
			return nil
		}
	}
	name := strings.NewReplacer(":", "_", "\\", "_", "/", "_").Replace(clean(path))
	backup := filepath.Join(backupRoot, "steam", name)
	if err := copyFile(path, backup); err != nil {
		return err
	}
	man.SteamConfigs = append(man.SteamConfigs, steamEntry{Target: path, Existed: true, Backup: backup})
	return saveManifest(man, backupRoot)
}

func setAppLaunchOptionsInText(text, options string) (string, error) {
	escaped := strings.ReplaceAll(strings.ReplaceAll(options, `\`, `\\`), `"`, `\"`)
	if loc := regexp.MustCompile(`"550"\s*\{`).FindStringIndex(text); loc != nil {
		open := strings.Index(text[loc[0]:], "{") + loc[0]
		close := matchingBrace(text, open)
		if close < 0 {
			return "", errors.New("localconfig.vdf 中 AppID 550 块无效")
		}
		block := text[open+1 : close]
		re := regexp.MustCompile(`(?m)^(\s*)"LaunchOptions"\s*"[^"]*"`)
		if re.MatchString(block) {
			block = re.ReplaceAllString(block, `${1}"LaunchOptions"`+"\t"+`"`+escaped+`"`)
		} else {
			block = strings.TrimRight(block, "\r\n\t ") + "\r\n\t\t\t\t\t\t\"LaunchOptions\"\t\"" + escaped + "\"\r\n"
		}
		return text[:open+1] + block + text[close:], nil
	}
	if loc := regexp.MustCompile(`"apps"\s*\{`).FindStringIndex(text); loc != nil {
		open := strings.Index(text[loc[0]:], "{") + loc[0]
		close := matchingBrace(text, open)
		if close < 0 {
			return "", errors.New("localconfig.vdf 中 apps 块无效")
		}
		insert := "\r\n\t\t\t\t\t\"550\"\r\n\t\t\t\t\t{\r\n\t\t\t\t\t\t\"LaunchOptions\"\t\"" + escaped + "\"\r\n\t\t\t\t\t}\r\n"
		return text[:close] + insert + text[close:], nil
	}
	return "", errors.New("localconfig.vdf 中未找到 apps 块")
}

func matchingBrace(s string, open int) int {
	depth := 0
	inString := false
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			if i == 0 || s[i-1] != '\\' {
				inString = !inString
			}
		case '{':
			if !inString {
				depth++
			}
		case '}':
			if !inString {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func resolveGameExe(packageRoot string) (string, error) {
	for _, root := range steamRoots("") {
		if found := findGameExeFromSteamRoot(root, packageRoot); found != "" {
			return found, nil
		}
	}
	if found := searchEverything(packageRoot); found != "" {
		return found, nil
	}
	if found := searchCommonDirs(packageRoot); found != "" {
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

func searchEverything(packageRoot string) string {
	resRoot := resourceRoot(packageRoot)
	exe := filepath.Join(resRoot, "tools", "Everything", "everything.exe")
	if exists(exe) {
		cmd := exec.Command(exe, "-startup")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Start()
		time.Sleep(2 * time.Second)
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
		appendLog("[search] Everything CLI: " + es)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		cmd := exec.CommandContext(ctx, es, "-n", "50", "left4dead2.exe")
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

func searchCommonDirs(packageRoot string) string {
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
		var found string
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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

func processExists(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq "+name)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(name))
}

func readLaunchOptions(root string) string {
	files, _ := filepath.Glob(filepath.Join(root, "*.txt"))
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f))
		if strings.Contains(base, "验证") || strings.Contains(base, "verify") || strings.Contains(base, "validate") {
			continue
		}
		data, err := os.ReadFile(f)
		if err == nil && strings.Contains(string(data), "-heapsize") && strings.Contains(string(data), "-vulkan") {
			return strings.TrimSpace(string(data))
		}
	}
	return defaultLaunchOptions
}

func findPackageDir(root string, required []string) (string, error) {
	var dirs []string
	entries, _ := os.ReadDir(root)
	dirs = append(dirs, root)
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	for _, dir := range dirs {
		ok := true
		for _, rel := range required {
			if !exists(filepath.Join(dir, rel)) {
				ok = false
				break
			}
		}
		if ok {
			return dir, nil
		}
	}
	return "", errors.New("未找到补丁目录")
}

func findNamedPackageDir(root, name string) (string, error) {
	for _, base := range packageSearchRoots(root) {
		dir := filepath.Join(base, name)
		if isNamedPackageDir(dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("未找到完整补丁目录: %s", name)
}

func isNamedPackageDir(dir string) bool {
	required := []string{"readme_l4n.txt", filepath.Join("bin", "left4neko.dll"), "dxgi.dll", "dxvk_d3d9.dll"}
	for _, rel := range required {
		if !exists(filepath.Join(dir, rel)) {
			return false
		}
	}
	return true
}

func discoverDxvkOptions(root, resRoot string) []dxvkOption {
	var options []dxvkOption
	seen := map[string]bool{}
	for _, base := range packageSearchRoots(root) {
		for _, versionsRoot := range []string{
			filepath.Join(base, dxvkVersionsDirName),
			filepath.Join(resRoot, dxvkVersionsDirName),
		} {
			entries, err := os.ReadDir(versionsRoot)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				dir := filepath.Join(versionsRoot, entry.Name())
				if _, err := findDxvkX32Dir(dir); err != nil {
					appendLog("[prepare] skipped DXVK version " + entry.Name() + ": " + err.Error())
					continue
				}
				key := strings.ToLower(entry.Name())
				if seen[key] {
					continue
				}
				seen[key] = true
				options = append(options, dxvkOption{Name: entry.Name(), Dir: dir})
			}
		}
	}
	sort.Slice(options, func(i, j int) bool {
		return strings.ToLower(options[i].Name) < strings.ToLower(options[j].Name)
	})
	return options
}

func packageSearchRoots(root string) []string {
	var roots []string
	add := func(path string) {
		if path != "" && exists(path) {
			roots = append(roots, path)
		}
	}
	add(resourceRoot(root))
	add(root)
	add(filepath.Join(root, "resources"))
	add(filepath.Join(root, "L4N_Go_Win32_Portable", "resources"))
	add(filepath.Join(filepath.Dir(root), "resources"))
	add(filepath.Join(filepath.Dir(root), "L4N_Go_Win32_Portable", "resources"))
	return uniqueExistingDirs(roots)
}

func loadManifest(root, gameRoot string) *manifest {
	path := filepath.Join(root, "manifest.json")
	if data, err := os.ReadFile(path); err == nil {
		var m manifest
		if json.Unmarshal(data, &m) == nil {
			return &m
		}
	}
	return &manifest{CreatedAt: time.Now().Format(time.RFC3339), GameRoot: gameRoot}
}

func saveManifest(m *manifest, root string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	path := filepath.Join(root, "manifest.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

func replaceDir(src, dst string) error {
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if exists(dst) {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(src, dst); err != nil {
		if exists(old) {
			_ = os.Rename(old, dst)
		}
		return err
	}
	return os.RemoveAll(old)
}

func videoSettingsPath(gameRoot string) string {
	return filepath.Join(gameRoot, filepath.FromSlash(videoSettingsRelativePath))
}

func displaySettingsBackupPath(resRoot string) string {
	return filepath.Join(resRoot, displaySettingsBackupDirName, "video.txt")
}

func backupDisplaySettings(gameRoot, resRoot string) error {
	src := videoSettingsPath(gameRoot)
	if !exists(src) {
		appendLog("[display] video settings not found; skipped: " + src)
		return nil
	}
	dst := displaySettingsBackupPath(resRoot)
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		_ = os.Chmod(dst, info.Mode().Perm())
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	appendLog("[display] backed up: " + src)
	return nil
}

func restoreDisplaySettings(gameRoot, resRoot string) error {
	src := displaySettingsBackupPath(resRoot)
	if !exists(src) {
		appendLog("[display] backup not found; skipped: " + src)
		return nil
	}
	dst := videoSettingsPath(gameRoot)
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		_ = os.Chmod(dst, info.Mode().Perm())
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	appendLog("[display] restored: " + dst)
	return nil
}

func restoreFileMetadata(entry fileEntry) {
	if entry.Mode != 0 {
		_ = os.Chmod(entry.Target, os.FileMode(entry.Mode))
	}
	if entry.ModTime != "" {
		if t, err := time.Parse(time.RFC3339Nano, entry.ModTime); err == nil {
			_ = os.Chtimes(entry.Target, t, t)
		}
	}
}

func restoreBackupMetadata(entry fileEntry) {
	if entry.Backup == "" {
		return
	}
	if entry.Mode != 0 {
		_ = os.Chmod(entry.Backup, os.FileMode(entry.Mode))
	}
	if entry.ModTime != "" {
		if t, err := time.Parse(time.RFC3339Nano, entry.ModTime); err == nil {
			_ = os.Chtimes(entry.Backup, t, t)
		}
	}
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

func toggleConfigFont(path, systemFont string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	updated, mode, err := toggleFontBlock(text, systemFont)
	if err != nil {
		return "", err
	}
	if err := backupConfigFile(path); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return "", err
	}
	return mode, nil
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

func toggleFontBlock(text, systemFont string) (string, string, error) {
	lines := strings.SplitAfter(text, "\n")
	start, end, err := findFontBlockLines(lines)
	if err != nil {
		return "", "", err
	}
	if fontBlockUsesSystem(lines[start:end]) {
		for i := start; i < end; i++ {
			lines[i] = commentConfigLine(lines[i])
		}
		return strings.Join(lines, ""), "game", nil
	}
	if fontBlockIsCommented(lines[start:end]) {
		for i := start; i < end; i++ {
			lines[i] = uncommentConfigLine(lines[i])
		}
	}
	if err := activateTahomaFontLine(lines[start:end], systemFont); err != nil {
		return "", "", err
	}
	return strings.Join(lines, ""), "system", nil
}

func findFontBlockLines(lines []string) (int, int, error) {
	start := -1
	for i, line := range lines {
		if regexp.MustCompile(`^\s*(//\s*)?"font"(\s|//|$)`).MatchString(lineWithoutLineBreak(line)) {
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

func fontBlockUsesSystem(lines []string) bool {
	for _, line := range lines {
		body := lineWithoutLineBreak(line)
		if regexp.MustCompile(`^\s*"Tahoma"\s+"[^"]+"`).MatchString(body) {
			return true
		}
	}
	return false
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

func activateTahomaFontLine(lines []string, systemFont string) error {
	re := regexp.MustCompile(`^(\s*)(//\s*)?"Tahoma"\s+"([^"]*)"([^\r\n]*)`)
	for i, line := range lines {
		body, br := splitLineBreak(line)
		m := re.FindStringSubmatch(body)
		if len(m) == 5 {
			lines[i] = fmt.Sprintf(`%s"Tahoma" "%s"%s%s`, m[1], systemFont, m[4], br)
			return nil
		}
	}
	return errors.New("config.vdf 中未找到 Tahoma 字体替换行")
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

func systemDefaultFont() string {
	return fallbackSystemFont()
}

func fallbackSystemFont() string {
	build := regString(hkeyLocalMachine, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "CurrentBuildNumber")
	if build == "" {
		build = regString(hkeyLocalMachine, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "CurrentBuild")
	}
	if build != "" {
		var n int
		if _, err := fmt.Sscanf(build, "%d", &n); err == nil && n >= 6000 {
			return "Segoe UI"
		}
	}
	product := strings.ToLower(regString(hkeyLocalMachine, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName"))
	if strings.Contains(product, "xp") || strings.Contains(product, "2003") {
		return "Tahoma"
	}
	return "Segoe UI"
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

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func uint16PtrFromID(id uint16) *uint16 {
	return (*uint16)(unsafe.Pointer(uintptr(id)))
}
