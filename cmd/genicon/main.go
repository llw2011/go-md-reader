package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"github.com/akavel/rsrc/rsrc"
)

func main() {
	pngBytes := renderIconPNG(256)
	icoBytes := buildICO(pngBytes, 0) // 0 means 256x256 in ICO header

	if err := os.WriteFile("app.ico", icoBytes, 0644); err != nil {
		panic(err)
	}

	if err := rsrc.Embed("rsrc_windows_amd64.syso", "amd64", "", "app.ico"); err != nil {
		panic(err)
	}

	_ = os.MkdirAll("cmd/installer", 0755)
	if err := rsrc.Embed("cmd/installer/rsrc_windows_amd64.syso", "amd64", "", "app.ico"); err != nil {
		panic(err)
	}

	fmt.Println("Generated app.ico and rsrc_windows_amd64.syso successfully.")
}

func renderIconPNG(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	fSize := float64(size)
	radius := fSize * 0.22
	margin := fSize * 0.06

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			if insideRoundedRect(fx, fy, margin, margin, fSize-margin, fSize-margin, radius) {
				// Gradient from #3B82F6 (top-left) to #1D4ED8 (bottom-right)
				t := (fx + fy) / (2 * fSize)
				r := uint8(59*(1-t) + 29*t)
				g := uint8(130*(1-t) + 78*t)
				b := uint8(246*(1-t) + 216*t)
				img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: 255})
			}
		}
	}

	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	stroke := fSize * 0.085

	// Draw 'M' on left side
	x1, x2, x3 := fSize*0.20, fSize*0.37, fSize*0.54
	yTop, yBot, yMid := fSize*0.28, fSize*0.72, fSize*0.54
	drawThickLine(img, x1, yBot, x1, yTop, stroke, white)
	drawThickLine(img, x1, yTop, x2, yMid, stroke*0.9, white)
	drawThickLine(img, x2, yMid, x3, yTop, stroke*0.9, white)
	drawThickLine(img, x3, yTop, x3, yBot, stroke, white)

	// Draw Down Arrow '↓' on right side
	ax := fSize * 0.75
	drawThickLine(img, ax, fSize*0.28, ax, fSize*0.68, stroke, white)
	drawThickLine(img, ax-fSize*0.11, fSize*0.55, ax, fSize*0.70, stroke*0.9, white)
	drawThickLine(img, ax+fSize*0.11, fSize*0.55, ax, fSize*0.70, stroke*0.9, white)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func insideRoundedRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func drawThickLine(img *image.NRGBA, x0, y0, x1, y1, thickness float64, col color.NRGBA) {
	half := thickness / 2.0
	b := img.Bounds()
	vx, vy := x1-x0, y1-y0
	l2 := vx*vx + vy*vy
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			var t float64
			if l2 > 0 {
				t = ((px-x0)*vx + (py-y0)*vy) / l2
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
			}
			cx, cy := x0+t*vx, y0+t*vy
			dist := math.Hypot(px-cx, py-cy)
			if dist <= half {
				img.SetNRGBA(x, y, col)
			}
		}
	}
}

func buildICO(pngData []byte, dim uint8) []byte {
	var buf bytes.Buffer
	// ICONDIR header (6 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // Reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // Type: 1 = ICO
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // Count: 1 image

	// ICONDIRENTRY (16 bytes)
	buf.WriteByte(dim)                                                   // Width
	buf.WriteByte(dim)                                                   // Height
	buf.WriteByte(0)                                                     // ColorCount
	buf.WriteByte(0)                                                     // Reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))               // Planes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))              // BitCount
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngData)))    // BytesInRes
	_ = binary.Write(&buf, binary.LittleEndian, uint32(6+16))            // ImageOffset

	buf.Write(pngData)
	return buf.Bytes()
}
