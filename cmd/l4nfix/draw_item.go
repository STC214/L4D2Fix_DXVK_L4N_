//go:build windows

package main

import "unsafe"

// LPARAM is a Windows-owned address, not a Go pointer. Copy the native struct
// through the Windows API instead of converting an integer to unsafe.Pointer.
// This does not create a window or modify the native message buffer.
func copyDrawItem(address uintptr) *drawItemStruct {
	if address == 0 {
		return nil
	}
	item := new(drawItemStruct)
	kernel32.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(item)), address, unsafe.Sizeof(*item))
	return item
}
