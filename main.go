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
	appTitle             = "L4N"
	defaultLaunchOptions = "-heapsize 2097152 -processheap -high -novid -nojoy -steam -lv -vulkan"
	resourceDirName      = "resources"
	genericPatchDirName  = "L4N_dxvk2.7.1"
	amdPatchDirName      = "L4N_dxvk2.3.1_AMD"
	usageInstructions    = "使用步骤\r\n\r\n1. 关闭 Steam 和游戏后运行本工具。\r\n\r\n2. 普通显卡点击“通用一键处理”。\r\n\r\n3. AMD 显卡点击“AMD 一键处理”。\r\n\r\n4. 工具会先备份原文件，再用 resources 中的补丁覆盖游戏源文件。\r\n\r\n5. 需要撤销时点击“一键清理”。备份记录保存在 exe 同级 .l4n_auto_backup。\r\n\r\nSteam 启动项\r\n-heapsize 2097152 -processheap -high -novid -nojoy -steam -lv -vulkan\r\n\r\n验证方式\r\nmat_info -> ShaderAPI: shaderapivk\r\nmem_dump -> 2,048.00MB"
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

	hInstance    uintptr
	hWnd         uintptr
	btnRun       uintptr
	btnAMD       uintptr
	btnClean     uintptr
	btnClose     uintptr
	progress     uintptr
	statusCtl    uintptr
	logCtl       uintptr
	blackBr      uintptr
	textFont     uintptr
	titleFont    uintptr
	buttonFont   uintptr
	guideFont    uintptr
	busyMu       sync.Mutex
	busy         bool
	progressMu   sync.Mutex
	lastProgress int
	uiMu         sync.Mutex
	uiNext       uintptr
	uiWork       = map[uintptr]func(){}
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
	esMultiline   = 0x0004
	esAutovScroll = 0x0040
	esReadOnly    = 0x0800
	wsVScroll     = 0x00200000

	cbs = wsCaption | wsSysMenu | wsMinimizeBox

	idRun   = 1001
	idClean = 1002
	idClose = 1003
	idAMD   = 1004

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
			case idAMD:
				go guarded("AMD 一键处理", runInstallAMD)
			case idClean:
				go guarded("一键清理", runRestore)
			case idClose:
				procDestroyWindow.Call(hwnd)
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
	desc := label(hwnd, "自动识别游戏目录，安装运行库，备份原文件，并用所选补丁目录覆盖游戏源文件。", 22, 68, 610, 24)
	procSendMessageW.Call(desc, wmSetFont, textFont, 1)

	btnRun = button(hwnd, "通用一键处理", idRun, 22, 134, 150, 46)
	btnAMD = button(hwnd, "AMD 一键处理", idAMD, 184, 134, 150, 46)
	btnClean = button(hwnd, "一键清理", idClean, 346, 134, 128, 46)
	btnClose = button(hwnd, "关闭", idClose, 506, 134, 110, 46)
	for _, h := range []uintptr{btnRun, btnAMD, btnClean, btnClose} {
		procSendMessageW.Call(h, wmSetFont, buttonFont, 1)
	}

	progress = create("msctls_progress32", "", wsChild|wsVisible, 22, 197, 594, 17, hwnd, 0)
	procSendMessageW.Call(progress, pbmSetRange32, 0, 100)
	procSendMessageW.Call(progress, pbmSetPos, 0, 0)

	statusCtl = label(hwnd, "就绪 - 请选择通用或 AMD 补丁覆盖游戏源文件", 22, 230, 594, 24)
	procSendMessageW.Call(statusCtl, wmSetFont, textFont, 1)
	logTitle := label(hwnd, "日志", 22, 263, 80, 20)
	procSendMessageW.Call(logTitle, wmSetFont, textFont, 1)
	logCtl = create("EDIT", "", wsChild|wsVisible|wsBorder|esMultiline|esAutovScroll|esReadOnly|wsVScroll, 22, 282, 594, 234, hwnd, 0)
	procSendMessageW.Call(logCtl, wmSetFont, textFont, 1)

	guideTitle := label(hwnd, "使用说明", 650, 28, 240, 26)
	procSendMessageW.Call(guideTitle, wmSetFont, titleFont, 1)
	guideCtl := create("EDIT", usageInstructions, wsChild|wsVisible|wsBorder|esMultiline|esReadOnly, 650, 68, 270, 448, hwnd, 0)
	procSendMessageW.Call(guideCtl, wmSetFont, guideFont, 1)

	appendLog("[prepare] ready; resources directory: " + resourceDirName)
	appendLog("[prepare] generic patch: resources\\" + genericPatchDirName)
	appendLog("[prepare] AMD patch: resources\\" + amdPatchDirName)
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
		return "通用一键处理"
	case idAMD:
		return "AMD 一键处理"
	case idClean:
		return "一键清理"
	case idClose:
		return "关闭"
	default:
		return ""
	}
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
		procEnableWindow.Call(btnAMD, en)
		procEnableWindow.Call(btnClean, en)
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
	return runInstallWithPatch(genericPatchDirName)
}

func runInstallAMD() error {
	return runInstallWithPatch(amdPatchDirName)
}

func runInstallWithPatch(patchDirName string) error {
	root, err := packageRoot()
	if err != nil {
		return err
	}
	resRoot := resourceRoot(root)
	appendLog("[prepare] exe root: " + root)
	appendLog("[prepare] resource root: " + resRoot)
	runtimeDir := resRoot
	patchDir, err := findNamedPackageDir(resRoot, patchDirName)
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
	if err := copyPatchFiles(man, backupRoot, gameRoot, patchDir); err != nil {
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
	setProgress(15)
	for i, e := range man.Files {
		if e.Existed {
			if e.Backup == "" {
				appendLog("[restore] missing backup path: " + e.Target)
				continue
			}
			if err := copyFile(e.Backup, e.Target); err != nil {
				appendLog("[restore] " + err.Error())
			} else {
				restoreFileMetadata(e)
				appendLog("[restore] " + e.Rel)
			}
		} else if exists(e.Target) {
			if err := os.Remove(e.Target); err != nil {
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
				appendLog("[steam] " + err.Error())
			} else {
				appendLog("[steam] restored: " + e.Target)
			}
		}
	}
	setProgress(85)
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

func copyPatchFiles(man *manifest, backupRoot, gameRoot, patchDir string) error {
	appendLog("[copy] source patch directory: " + patchDir)
	appendLog("[copy] target game directory: " + gameRoot)
	var files []string
	err := filepath.WalkDir(patchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for i, src := range files {
		rel, _ := filepath.Rel(patchDir, src)
		dst := filepath.Join(gameRoot, rel)
		if err := copyWithBackup(man, backupRoot, gameRoot, src, dst); err != nil {
			return err
		}
		if i%5 == 0 {
			setProgress(40 + i*35/max(1, len(files)))
		}
	}
	appendLog(fmt.Sprintf("[copy] %d patch files copied over the game files with backup", len(files)))
	return nil
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
	dir := filepath.Join(root, name)
	required := []string{"readme_l4n.txt", filepath.Join("bin", "left4neko.dll"), "dxgi.dll", "dxvk_d3d9.dll"}
	for _, rel := range required {
		if !exists(filepath.Join(dir, rel)) {
			return "", fmt.Errorf("未找到完整补丁目录: %s", dir)
		}
	}
	return dir, nil
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
