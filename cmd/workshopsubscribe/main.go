//go:build windows && 386

// Isolated x86 Steamworks bridge: use the game's existing DLL, never ship one
// or load third-party native code into the main UI process.
package main

import (
	"context"
	"fmt"
	"l4nfix/internal/steamsubscription"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

type steamClient struct {
	ugc                                      uintptr
	state, subscribe, unsubscribe, callbacks *syscall.Proc
}

func (c steamClient) State(id uint64) uint32 {
	r, _, _ := c.state.Call(c.ugc, uintptr(uint32(id)), uintptr(uint32(id>>32)))
	return uint32(r)
}
func (c steamClient) Subscribe(id uint64) bool {
	lo, hi, _ := c.subscribe.Call(c.ugc, uintptr(uint32(id)), uintptr(uint32(id>>32)))
	return lo != 0 || hi != 0
}
func (c steamClient) RunCallbacks() { c.callbacks.Call() }
func (c steamClient) Unsubscribe(id uint64) bool {
	lo, hi, _ := c.unsubscribe.Call(c.ugc, uintptr(uint32(id)), uintptr(uint32(id>>32)))
	return lo != 0 || hi != 0
}

func run() error {
	if len(os.Args) != 3 || !filepath.IsAbs(os.Args[1]) || (os.Args[2] != "subscribe" && os.Args[2] != "unsubscribe") {
		return fmt.Errorf("usage: Steam_Workshop_Subscribe.exe ABSOLUTE_GAME_ROOT subscribe|unsubscribe")
	}
	root := filepath.Clean(os.Args[1])
	if _, e := os.Stat(filepath.Join(root, "left4dead2.exe")); e != nil {
		return e
	}
	// Set app context only in this disposable child, not in Steam or the UI.
	os.Setenv("SteamAppId", "550")
	os.Setenv("SteamGameId", "550")
	dllPath := filepath.Join(root, "bin", "steam_api.dll")
	name, e := syscall.UTF16PtrFromString(dllPath)
	if e != nil {
		return e
	}
	// Dependency search is restricted to this DLL's directory and System32.
	h, _, loadErr := syscall.NewLazyDLL("kernel32.dll").NewProc("LoadLibraryExW").Call(uintptr(unsafe.Pointer(name)), 0, 0x100|0x800)
	if h == 0 {
		return fmt.Errorf("load game Steam API: %v", loadErr)
	}
	dll := &syscall.DLL{Name: dllPath, Handle: syscall.Handle(h)}
	defer dll.Release()
	find := func(name string) (*syscall.Proc, error) { return dll.FindProc(name) }
	init, e := find("SteamAPI_Init")
	if e != nil {
		return e
	}
	shutdown, e := find("SteamAPI_Shutdown")
	if e != nil {
		return e
	}
	callbacks, e := find("SteamAPI_RunCallbacks")
	if e != nil {
		return e
	}
	state, e := find("SteamAPI_ISteamUGC_GetItemState")
	if e != nil {
		return e
	}
	subscribe, e := find("SteamAPI_ISteamUGC_SubscribeItem")
	if e != nil {
		return e
	}
	unsubscribe, e := find("SteamAPI_ISteamUGC_UnsubscribeItem")
	if e != nil {
		return e
	}
	if ok, _, _ := init.Call(); ok == 0 {
		return fmt.Errorf("SteamAPI_Init failed; start Steam, log in, and confirm ownership of AppID 550")
	}
	defer shutdown.Call()
	var ugc uintptr
	for v := 21; v >= 9; v-- {
		if get, err := find(fmt.Sprintf("SteamAPI_SteamUGC_v%03d", v)); err == nil {
			ugc, _, _ = get.Call()
			if ugc != 0 {
				break
			}
		}
	}
	if ugc == 0 {
		return fmt.Errorf("game Steam API has no supported flat ISteamUGC interface")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	wanted := os.Args[2] == "subscribe"
	if err := steamsubscription.Set(ctx, steamClient{ugc, state, subscribe, unsubscribe, callbacks}, steamsubscription.SkeetoItems[:], wanted, 200*time.Millisecond); err != nil {
		return err
	}
	fmt.Printf("SKEETO_%s_CONFIRMED:3782512354,3787239937\n", os.Args[2])
	return nil
}

func main() {
	runtime.LockOSThread()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
