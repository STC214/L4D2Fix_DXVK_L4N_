//go:build windows

package main

import (
	"fmt"
	"sync"
	"unsafe"
)

const idCheckNetwork = 1014
const windowWidth, windowHeight = 1020, 760

var btnCheckNetwork, networkStatusCtl, updateStatusCtl uintptr

type checkState uint8

const (
	checkUnknown checkState = iota
	checkRunning
	checkPassed
	checkFailed
)

type workflowState struct {
	Network             checkState
	Updates             checkState
	L4NReady, DXVKReady bool
	BaseInstalled       bool
}

func (s workflowState) canDeployBase() bool {
	return s.Updates != checkRunning && s.L4NReady && s.DXVKReady
}

var workflowMu sync.Mutex
var workflow workflowState

func workflowSnapshot() workflowState {
	workflowMu.Lock()
	defer workflowMu.Unlock()
	return workflow
}

func changeWorkflow(fn func(*workflowState)) {
	workflowMu.Lock()
	fn(&workflow)
	workflowMu.Unlock()
	invokeUI(refreshWorkflowControls)
}

func uiText(h uintptr, s string) {
	if h != 0 {
		procSetWindowTextW.Call(h, uintptr(unsafe.Pointer(utf16Ptr(s))))
	}
}

func enableControl(h uintptr, enabled bool) {
	v := uintptr(0)
	if enabled {
		v = 1
	}
	if h != 0 {
		procEnableWindow.Call(h, v)
	}
}

// Called only on the UI thread. Cleanup is independent of update/network
// prerequisites; existing ownership and game-running checks still apply.
func refreshWorkflowControls() {
	s := workflowSnapshot()
	busyMu.Lock()
	working := busy
	busyMu.Unlock()
	for _, h := range []uintptr{btnCheckNetwork, btnCheckUpdates, comboDxvk, comboL4n,
		btnBackupMod, btnRestoreMod, btnClean, btnClose, btnRefreshVersion, btnCleanPlatform, comboPlatform} {
		enableControl(h, !working)
	}
	enableControl(btnRun, !working && s.canDeployBase())
	enableControl(btnDeployPlatform, !working && s.BaseInstalled && len(platformChoices) > 0)
	networkText := "可选先测网络，或直接进行 2"
	switch s.Network {
	case checkRunning:
		networkText = "检测接口与下载链路中…"
	case checkPassed:
		networkText = "通过 · 可继续 2 检测更新"
	case checkFailed:
		networkText = "失败 · 不建议更新，请检查网络"
	}
	uiText(networkStatusCtl, networkText)
	updateText := "下载并校验后解锁部署"
	switch s.Updates {
	case checkRunning:
		updateText = "检查 / 下载 / 校验中…"
	case checkPassed:
		updateText = "已就绪 · 可继续 3 部署基础"
	case checkFailed:
		updateText = "部分失败 · 建议先做 1 网络检测"
		if s.canDeployBase() {
			updateText = "部分失败 · 基础就绪；建议检测网络"
		}
	}
	uiText(updateStatusCtl, updateText)
	platformText := "请先进行 2 检测更新"
	if len(platformChoices) > 0 {
		platformText = fmt.Sprintf("%d 个平台就绪 · 请先完成 3 基础部署", len(platformChoices))
		if s.BaseInstalled {
			platformText = fmt.Sprintf("%d 个平台就绪 · 可部署（二选一）", len(platformChoices))
		}
	}
	uiText(platformStatusCtl, platformText)
}

func runCheckUpdatesWithGuidance() error {
	if workflowSnapshot().Network == checkFailed {
		// Advisory, not a permanent lock: users may have changed their network.
		if manualCleanupMessage("网络检测未通过", "上次网络检测失败，不建议立即检查更新。\n建议先调整网络环境，再进行 1 网络检测。\n\n仍要继续检查更新吗？", 0x34|0x100) != 6 {
			return fmt.Errorf("已取消更新，请先检查网络环境并测试连通性")
		}
	}
	return runCheckUpdates()
}
