//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"l4nfix/internal/steamsubscription"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func shellOpen(target string) error {
	r, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(hWnd, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(target))), 0, 0, swShow)
	if r <= 32 {
		return fmt.Errorf("打开失败 (%d): %s", r, target)
	}
	return nil
}

// TASKDIALOGCONFIG and TASKDIALOG_BUTTON are packed(1), on both x86/x64.
func steamChoiceDialog() (bool, error) {
	proc := comctl32.NewProc("TaskDialogIndirect")
	if err := proc.Find(); err != nil {
		return false, err
	}
	ptrSize := int(unsafe.Sizeof(uintptr(0)))
	var keep [][]uint16
	str := func(s string) uintptr {
		u, _ := syscall.UTF16FromString(s)
		keep = append(keep, u)
		return uintptr(unsafe.Pointer(&u[0]))
	}
	put32 := func(b *[]byte, v uint32) { *b = binary.LittleEndian.AppendUint32(*b, v) }
	putPtr := func(b *[]byte, v uintptr) {
		if ptrSize == 8 {
			*b = binary.LittleEndian.AppendUint64(*b, uint64(v))
		} else {
			put32(b, uint32(v))
		}
	}
	buttons := []byte{}
	put32(&buttons, 100)
	putPtr(&buttons, str("确认关闭并自动填写参数"))
	put32(&buttons, 101)
	putPtr(&buttons, str("否，稍后我会自己填写参数"))
	b := []byte{}
	put32(&b, 0)
	putPtr(&b, hWnd)
	putPtr(&b, 0)
	put32(&b, 0x10|0x8)
	put32(&b, 0) // command links; allow cancel
	putPtr(&b, str("L4N＋DXVK 部署"))
	putPtr(&b, 0)
	putPtr(&b, str("一键修改启动参数必须关闭 Steam"))
	putPtr(&b, str("请先退出游戏。确认后将请求 Steam 正常退出，等待退出后部署并填写参数，结束后重新打开 Steam。\n选择“否”只部署文件，不修改 Steam 配置；参数将显示在日志中。"))
	put32(&b, 2)
	putPtr(&b, uintptr(unsafe.Pointer(&buttons[0])))
	put32(&b, 101)
	put32(&b, 0)
	putPtr(&b, 0)
	put32(&b, 0)
	for i := 0; i < 8; i++ {
		putPtr(&b, 0)
	}
	put32(&b, 380)
	binary.LittleEndian.PutUint32(b, uint32(len(b)))
	var selected int32
	hr, _, _ := proc.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&selected)), 0, 0)
	runtime.KeepAlive(keep)
	runtime.KeepAlive(buttons)
	runtime.KeepAlive(b)
	if int32(hr) < 0 {
		return false, fmt.Errorf("Steam 选择框失败: HRESULT=%08x", uint32(hr))
	}
	if selected == 100 {
		return true, nil
	}
	if selected == 101 {
		return false, nil
	}
	return false, fmt.Errorf("已取消部署，未修改文件")
}

// Process enumeration errors must not be interpreted as "Steam is closed".
func steamProcessesRunning() (bool, error) {
	return processNamesRunning([]string{"steam.exe", "steamwebhelper.exe"})
}

func processNamesRunning(names []string) (bool, error) {
	h, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer syscall.CloseHandle(h)
	p := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	err = syscall.Process32First(h, &p)
	for err == nil {
		name := syscall.UTF16ToString(p.ExeFile[:])
		for _, wanted := range names {
			if strings.EqualFold(name, wanted) {
				return true, nil
			}
		}
		err = syscall.Process32Next(h, &p)
	}
	if err != syscall.ERROR_NO_MORE_FILES {
		return false, err
	}
	return false, nil
}

func waitSteamClosed(ctx context.Context) error {
	for {
		running, err := steamProcessesRunning()
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Steam 尚未退出（可能仍在确认关闭或更新），已停止部署: %w", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func steamExecutable() (string, error) {
	for _, root := range steamRoots("") {
		p := filepath.Join(root, "steam.exe")
		if exists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到本机 Steam.exe；可选择稍后手动填写参数")
}

type steamInstallHooks struct {
	running  func() (bool, error)
	shutdown func() error
	wait     func() error
	start    func() error
}

// Restart on success (even if initially closed), or on failure if previously
// running. The manual branch does not close/start Steam or write its config.
func withSteamInstall(auto bool, h steamInstallHooks, install func(bool) error) (err error) {
	if !auto {
		return install(false)
	}
	wasRunning, err := h.running()
	if err != nil {
		return err
	}
	defer func() {
		if wasRunning || err == nil {
			if e := h.start(); e != nil {
				err = errors.Join(err, fmt.Errorf("Steam 重启失败: %w", e))
			}
		}
	}()
	if wasRunning {
		if err = h.shutdown(); err != nil {
			return err
		}
	}
	if err = h.wait(); err != nil {
		return err
	}
	return install(true)
}

func installWithSteamChoice(install func(bool) error) error {
	auto, err := steamChoiceDialog()
	if err != nil {
		return err
	}
	if !auto {
		appendLog("[steam] 已选择手动填写参数；不关闭 Steam、不写 Steam 配置")
		return install(false)
	}
	exe, err := steamExecutable()
	if err != nil {
		return err
	}
	h := steamInstallHooks{
		running: steamProcessesRunning,
		shutdown: func() error {
			appendLog("[steam] 正在请求 Steam 正常退出（不强制结束进程）")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-shutdown")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			return cmd.Run()
		},
		wait: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			return waitSteamClosed(ctx)
		},
		start: func() error { appendLog("[steam] 正在重新打开 Steam"); return shellOpen("steam://open/main") },
	}
	return withSteamInstall(true, h, install)
}

func updateSkeetoSubscriptions(root, gameRoot string, subscribe bool) {
	action, title := "subscribe", "订阅"
	if !subscribe {
		action, title = "unsubscribe", "取消订阅"
	}
	appendLog("[steam] 正在" + title + " Skeeto 及预设素材包")
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	var operationErr error
	if err := shellOpen("steam://open/main"); err != nil {
		operationErr = err
	} else {
		// SteamAPI_Init can race a just-started client; bounded retry only for
		// startup/init failures. Native helper failures remain isolated.
		for {
			cmd := exec.CommandContext(ctx, filepath.Join(root, "Steam_Workshop_Subscribe.exe"), gameRoot, action)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			out, err := cmd.CombinedOutput()
			confirmed := "SKEETO_" + action + "_CONFIRMED:3782512354,3787239937"
			if err == nil && subscriptionConfirmed(string(out), confirmed) {
				note := "；后续工坊校验/更新由 Steam 管理"
				if subscribe {
					note = "；本次 MOD 文件已由部署阶段预置，后续校验/更新由 Steam 管理"
				}
				appendLog("[steam] 已确认两个 MOD 均已" + title + note)
				return
			}
			operationErr = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			if !strings.Contains(string(out), "SteamAPI_Init failed") || ctx.Err() != nil {
				break
			}
			select {
			case <-ctx.Done():
				break
			case <-time.After(2 * time.Second):
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	appendLog("[steam] 文件操作已完成，但自动" + title + "未获确认: " + operationErr.Error())
	for _, id := range steamsubscription.SkeetoItems {
		url := fmt.Sprintf("steam://url/CommunityFilePage/%d", id)
		if err := shellOpen(url); err != nil {
			appendLog("[steam] " + err.Error())
		}
	}
	message := "Skeeto 文件操作已完成，但两个 MOD 的自动" + title + "未获确认。\n已请求本机 Steam 打开工坊页面，请登录并逐个确认“" + title + "”。\n3782512354（预设素材包）\n3787239937（Skeeto）\n详细原因见日志。"
	user32.NewProc("MessageBoxW").Call(hWnd, uintptr(unsafe.Pointer(utf16Ptr(message))), uintptr(unsafe.Pointer(utf16Ptr("Steam 工坊"+title+"待确认"))), 0x30)
}

// Steam API diagnostic output can precede our marker on stdout/stderr.
func subscriptionConfirmed(output, marker string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

func withPlatformSubscriptions(kind string, operation func() error, update func()) error {
	if err := operation(); err != nil {
		return err
	}
	if kind == "skeeto" {
		update()
	}
	return nil
}
