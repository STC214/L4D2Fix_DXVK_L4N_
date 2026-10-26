//go:build windows && 386

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A tiny dependency-free x86 fixture exercises the real DLL ABI without
// starting Steam, accessing an account, or modifying a game installation.
func TestNativeSteamBridgeFixture(t *testing.T) {
	assembler, e := exec.LookPath("as")
	if e != nil {
		t.Skip("GNU assembler missing")
	}
	linker, e := exec.LookPath("ld")
	if e != nil {
		t.Skip("GNU linker missing")
	}
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	t.Setenv("SteamAppId", os.Getenv("SteamAppId"))
	t.Setenv("SteamGameId", os.Getenv("SteamGameId"))
	for _, action := range []string{"subscribe", "unsubscribe"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if e := os.Mkdir(bin, 0755); e != nil {
				t.Fatal(e)
			}
			os.WriteFile(filepath.Join(root, "left4dead2.exe"), nil, 0644)
			initial := 0
			if action == "unsubscribe" {
				initial = 1
			}
			asm := fmt.Sprintf(".data\ns0: .long %d\ns1: .long %d\n.text\n", initial, initial)
			def := "EXPORTS\n"
			add := func(name, body string) {
				asm += ".globl _" + name + "\n_" + name + ":\n" + body + "\nret\n"
				def += name + "\n"
			}
			add("SteamAPI_Init", "movl $1,%eax")
			add("SteamAPI_Shutdown", "")
			add("SteamAPI_RunCallbacks", "")
			add("SteamAPI_SteamUGC_v021", "movl $1,%eax")
			add("SteamAPI_ISteamUGC_GetItemState", `cmpl $3782512354,8(%esp)
jne 1f
movl s0,%eax
ret
1: movl s1,%eax`)
			for _, method := range []struct {
				name  string
				value int
			}{{"SubscribeItem", 1}, {"UnsubscribeItem", 0}} {
				add("SteamAPI_ISteamUGC_"+method.name, fmt.Sprintf(`cmpl $1,4(%%esp)
jne 3f
cmpl $0,12(%%esp)
jne 3f
cmpl $3782512354,8(%%esp)
jne 1f
movl $%d,s0
jmp 2f
1: cmpl $3787239937,8(%%esp)
jne 3f
movl $%d,s1
2: xorl %%eax,%%eax
movl $1,%%edx
ret
3: xorl %%eax,%%eax
xorl %%edx,%%edx`, method.value, method.value))
			}
			os.WriteFile(filepath.Join(root, "native.s"), []byte(asm), 0644)
			os.WriteFile(filepath.Join(root, "native.def"), []byte(def), 0644)
			for _, cmd := range []*exec.Cmd{exec.Command(assembler, "--32", "-o", filepath.Join(root, "native.o"), filepath.Join(root, "native.s")), exec.Command(linker, "-m", "i386pe", "--dll", "--entry", "0", "-o", filepath.Join(bin, "steam_api.dll"), filepath.Join(root, "native.o"), filepath.Join(root, "native.def"))} {
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("fixture build: %v %s", e, out)
				}
			}
			os.Args = []string{"bridge", root, action}
			if e := run(); e != nil {
				t.Fatal(e)
			}
			// Removing a required export must fail, never return success.
			os.WriteFile(filepath.Join(root, "native.def"), []byte(strings.ReplaceAll(def, "SteamAPI_ISteamUGC_GetItemState\n", "")), 0644)
			cmd := exec.Command(linker, "-m", "i386pe", "--dll", "--entry", "0", "--exclude-all-symbols", "-o", filepath.Join(bin, "steam_api.dll"), filepath.Join(root, "native.o"), filepath.Join(root, "native.def"))
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("fixture rebuild: %v %s", e, out)
			}
			if e := run(); e == nil {
				t.Fatal("missing export accepted")
			}
		})
	}
}

func TestBridgeRejectsInvalidArguments(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	for _, args := range [][]string{{"bridge"}, {"bridge", "relative", "subscribe"}, {"bridge", `C:\fixture`, "unknown"}} {
		os.Args = args
		if e := run(); e == nil {
			t.Fatal(args)
		}
	}
}
