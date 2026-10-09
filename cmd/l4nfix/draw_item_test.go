//go:build windows

package main

import (
	"testing"
	"unsafe"
)

// Native memory only, deliberately no GUI windows, controls or drawing calls.
func TestCopyDrawItemWithoutWindow(t *testing.T) {
	if copyDrawItem(0) != nil {
		t.Fatal("null native pointer accepted")
	}
	item := drawItemStruct{ctrlID: 42, itemState: 7, hwndItem: 123, hdc: 456, rcItem: rect{left: 1, top: 2, right: 3, bottom: 4}}
	mem, _, _ := kernel32.NewProc("LocalAlloc").Call(0, unsafe.Sizeof(item))
	if mem == 0 {
		t.Fatal("native allocation failed")
	}
	defer kernel32.NewProc("LocalFree").Call(mem)
	kernel32.NewProc("RtlMoveMemory").Call(mem, uintptr(unsafe.Pointer(&item)), unsafe.Sizeof(item))
	copy := copyDrawItem(mem)
	if copy == nil || *copy != item {
		t.Fatal("native structure copy mismatch")
	}
	zero := drawItemStruct{}
	kernel32.NewProc("RtlMoveMemory").Call(mem, uintptr(unsafe.Pointer(&zero)), unsafe.Sizeof(zero))
	if *copy != item {
		t.Fatal("result aliases native memory")
	}
}
