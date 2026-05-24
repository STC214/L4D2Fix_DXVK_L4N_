package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: makeicon input.jpg output.ico")
	}
	in, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	src, err := jpeg.Decode(in)
	if err != nil {
		log.Fatal(err)
	}
	b := src.Bounds()
	size := b.Dx()
	if b.Dy() < size {
		size = b.Dy()
	}
	cropRect := image.Rect(0, 0, size, size).Add(b.Min)
	crop := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(crop, crop.Bounds(), src, cropRect.Min, draw.Src)

	sizes := []int{256, 128, 64, 48, 32, 16}
	pngs := make([][]byte, 0, len(sizes))
	for _, s := range sizes {
		img := resizeBilinear(crop, s, s)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			log.Fatal(err)
		}
		pngs = append(pngs, buf.Bytes())
	}

	out, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	binary.Write(out, binary.LittleEndian, uint16(0))
	binary.Write(out, binary.LittleEndian, uint16(1))
	binary.Write(out, binary.LittleEndian, uint16(len(sizes)))
	offset := uint32(6 + len(sizes)*16)
	for i, s := range sizes {
		w, h := byte(s), byte(s)
		if s == 256 {
			w, h = 0, 0
		}
		out.Write([]byte{w, h, 0, 0})
		binary.Write(out, binary.LittleEndian, uint16(1))
		binary.Write(out, binary.LittleEndian, uint16(32))
		binary.Write(out, binary.LittleEndian, uint32(len(pngs[i])))
		binary.Write(out, binary.LittleEndian, offset)
		offset += uint32(len(pngs[i]))
	}
	for _, p := range pngs {
		out.Write(p)
	}
}

func resizeBilinear(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	for y := 0; y < h; y++ {
		fy := float64(y) * float64(sh-1) / float64(h-1)
		y0 := int(fy)
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		for x := 0; x < w; x++ {
			fx := float64(x) * float64(sw-1) / float64(w-1)
			x0 := int(fx)
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			wx := fx - float64(x0)
			c00 := src.RGBAAt(x0, y0)
			c10 := src.RGBAAt(x1, y0)
			c01 := src.RGBAAt(x0, y1)
			c11 := src.RGBAAt(x1, y1)
			dst.SetRGBA(x, y, color.RGBA{
				R: mix(c00.R, c10.R, c01.R, c11.R, wx, wy),
				G: mix(c00.G, c10.G, c01.G, c11.G, wx, wy),
				B: mix(c00.B, c10.B, c01.B, c11.B, wx, wy),
				A: mix(c00.A, c10.A, c01.A, c11.A, wx, wy),
			})
		}
	}
	return dst
}

func mix(a, b, c, d uint8, wx, wy float64) uint8 {
	top := float64(a)*(1-wx) + float64(b)*wx
	bottom := float64(c)*(1-wx) + float64(d)*wx
	return uint8(top*(1-wy) + bottom*wy + 0.5)
}
