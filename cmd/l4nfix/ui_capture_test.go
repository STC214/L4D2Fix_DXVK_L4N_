//go:build windows

package main

import (
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"unsafe"
)

// Optional offscreen render of the real Win32 controls, not a mockup. The
// window stays hidden; no game, deployment or network button is clicked.
func captureNativeTestWindow(t *testing.T, hwnd uintptr, path string) {
	t.Helper()
	var bounds rect
	user32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&bounds)))
	w, h := int(bounds.right-bounds.left), int(bounds.bottom-bounds.top)
	hdc, _, _ := user32.NewProc("GetWindowDC").Call(hwnd)
	defer user32.NewProc("ReleaseDC").Call(hwnd, hdc)
	mem, _, _ := gdi32.NewProc("CreateCompatibleDC").Call(hdc)
	defer gdi32.NewProc("DeleteDC").Call(mem)
	bitmap, _, _ := gdi32.NewProc("CreateCompatibleBitmap").Call(hdc, uintptr(w), uintptr(h))
	defer procDeleteObject.Call(bitmap)
	old, _, _ := procSelectObject.Call(mem, bitmap)
	// WM_PRINT without PRF_CHECKVISIBLE paints a genuinely hidden window;
	// PrintWindow can report success while returning an all-black bitmap.
	procSendMessageW.Call(hwnd, 0x0317, mem, 0x0002|0x0004|0x0010|0x0020)
	procSelectObject.Call(mem, old)
	header := make([]byte, 40)
	binary.LittleEndian.PutUint32(header, 40)
	binary.LittleEndian.PutUint32(header[4:], uint32(w))
	negativeHeight := -int32(h)
	binary.LittleEndian.PutUint32(header[8:], uint32(negativeHeight))
	binary.LittleEndian.PutUint16(header[12:], 1)
	binary.LittleEndian.PutUint16(header[14:], 32)
	pixels := make([]byte, w*h*4)
	if n, _, err := gdi32.NewProc("GetDIBits").Call(hdc, bitmap, 0, uintptr(h), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&header[0])), 0); n != uintptr(h) {
		t.Fatalf("offscreen pixel read: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			img.SetRGBA(x, y, color.RGBA{R: pixels[i+2], G: pixels[i+1], B: pixels[i], A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, img)
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}
