//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"unsafe"
)

const steamIntegrityReminder = "请使用 Steam 平台检查求生之路 2 的文件完整性：\nSteam 库 → 求生之路 2 → 属性 → 已安装文件 → 验证游戏文件的完整性。\n手动部署没有原文件备份，校验完成前请勿启动游戏。"

func checkCleanupGameStopped() error {
	return checkGameStoppedFor("清理")
}

func checkGameStoppedFor(action string) error {
	running, e := processNamesRunning([]string{"left4dead2.exe", "left4dead2_skeeto.exe", "left4dead2_necola.exe"})
	if e != nil {
		return e
	}
	if running {
		return fmt.Errorf("请先退出游戏和旧平台启动器，再%s", action)
	}
	return nil
}

func manualCleanupMessage(title, text string, flags uintptr) uintptr {
	r, _, _ := user32.NewProc("MessageBoxW").Call(hWnd, uintptr(unsafe.Pointer(utf16Ptr(text))), uintptr(unsafe.Pointer(utf16Ptr(title))), flags)
	return r
}

func runManualCleanupUI(root, scope string) error {
	if e := checkCleanupGameStopped(); e != nil {
		return e
	}
	gameExe, e := resolveGameExe(root)
	if e != nil {
		return e
	}
	game := filepath.Dir(gameExe)
	release, e := acquireOperationLock(game, ".l4n-game-operation.lock")
	if e != nil {
		return e
	}
	// Release before Steam API calls, which can wait for user login/network.
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	res := resourceRoot(root)
	appendLog("[manual-clean] 无本工具安装清单，分析全部本地版本及已知历史资源")
	rows, warnings, e := refreshCleanupSignatures(res)
	if e != nil {
		return e
	}
	plan, e := buildManualCleanupPlan(game, scope, rows)
	if e != nil {
		return e
	}
	plan.Warnings = warnings
	for _, w := range warnings {
		appendLog("[manual-clean] 资源跳过: " + w)
	}
	for _, f := range plan.Files {
		appendLog("[manual-clean] 待处理: " + f.Kind + " / " + f.Rel)
	}
	for _, f := range plan.Kept {
		appendLog("[manual-clean] 保留: " + f)
	}
	name := "L4N＋DXVK"
	if scope == "platform" {
		name = "Skeeto / Evolved"
	}
	message := fmt.Sprintf("未找到本工具的 %s 安装清单。\n游戏目录：%s\n已识别 %d 个待处理文件，%d 个未确认文件将保留。\n\n确认后先完整留档，再清理已识别文件；共享文件仅在内容匹配资源包时处理，不清空插件、MOD 或着色器公共目录。\n其中可能包含手动替换的游戏 EXE，清理后需要 Steam 校验恢复。\n配置与私有目录里的个人数据也会留档后移除。\n\n清单见日志。是否继续？", name, game, len(plan.Files), len(plan.Kept))
	if manualCleanupMessage("手动部署残留清理确认", message, 0x4|0x100|0x30) != 6 {
		return fmt.Errorf("已取消残留清理，游戏文件未修改")
	}
	if e := checkCleanupGameStopped(); e != nil {
		return e
	}
	backup, e := applyManualCleanup(res, plan)
	if e != nil {
		appendLog("[manual-clean] " + e.Error())
		if backup != "" {
			appendLog("[manual-clean] 留档与状态: " + backup)
			manualCleanupMessage("残留清理未完成", "部分操作可能尚未完成，请查看日志和留档。\n"+backup+"\n\n"+steamIntegrityReminder, 0x30)
		}
		return e
	}
	release()
	locked = false
	if scope == "platform" {
		for _, f := range plan.Files {
			if f.Kind == "skeeto" {
				updateSkeetoSubscriptions(root, game, false)
				break
			}
		}
	}
	if backup != "" {
		appendLog("[manual-clean] 留档与 rollback.ps1: " + backup)
	}
	text := fmt.Sprintf("已处理 %d 个已识别文件；%d 个未确认文件已保留，详情见日志。\n", len(plan.Files), len(plan.Kept))
	if len(warnings) > 0 {
		text += fmt.Sprintf("有 %d 个资源包未能分析；不宣称已覆盖未知版本，原因见日志。\n", len(warnings))
	}
	text += "\n" + steamIntegrityReminder
	appendLog("[manual-clean] " + text)
	manualCleanupMessage("手动部署残留清理结果", text, 0x40)
	return nil
}
