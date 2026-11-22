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
	sizes := []int{256, 64, 48, 32, 16}
	entries := make([][]byte, len(sizes))
	for i, sz := range sizes {
		entries[i] = renderIconPNG(sz)
	}

	icoBytes := buildMultiICO(sizes, entries)
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
	// 4x4 subpixel supersampling gives clean edges down to 16x16 and 32x32 desktop icons.
	const ss = 4
	fSize := float64(size)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var rSum, gSum, bSum, aSum float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/ss) / fSize
					v := (float64(y) + (float64(sy)+0.5)/ss) / fSize
					c := sampleDocumentIcon(u, v)
					af := float64(c.A) / 255.0
					rSum += float64(c.R) * af
					gSum += float64(c.G) * af
					bSum += float64(c.B) * af
					aSum += af
				}
			}
			samples := float64(ss * ss)
			avgA := aSum / samples
			if avgA <= 0.001 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: clampU8(rSum / aSum),
				G: clampU8(gSum / aSum),
				B: clampU8(bSum / aSum),
				A: clampU8(avgA * 255.0),
			})
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// sampleDocumentIcon renders a crisp folded-corner document sheet icon suitable for
// .txt, .md, .ini, .json, and config files in normalized [0, 1] coordinates.
func sampleDocumentIcon(u, v float64) color.NRGBA {
	const (
		x0     = 0.15
		y0     = 0.06
		x1     = 0.85
		y1     = 0.94
		rad    = 0.075
		foldS  = 0.23
		border = 0.032
	)
	foldStartX := x1 - foldS
	foldEndY := y0 + foldS

	// 1. Soft drop shadow beneath the page
	sdShadow := pageSDF(u-0.012, v-0.028, x0, y0, x1, y1, rad, foldStartX, foldEndY)
	out := color.NRGBA{}
	if sdShadow < 0.055 {
		t := 1.0 - math.Max(0, sdShadow+0.015)/0.070
		if t > 0 {
			alpha := uint8(math.Min(1, t*t) * 62)
			out = blendOver(out, color.NRGBA{R: 15, G: 23, B: 42, A: alpha})
		}
	}

	// 2. Page sheet silhouette (white paper with top-right dog-ear cut)
	sdPage := pageSDF(u, v, x0, y0, x1, y1, rad, foldStartX, foldEndY)
	if sdPage <= 0 {
		// Subtle cool paper gradient (#FFFFFF -> #EFF6FF)
		grad := math.Min(1, math.Max(0, (u-x0+v-y0)/1.4))
		paper := color.NRGBA{
			R: uint8(255*(1-grad) + 239*grad),
			G: uint8(255*(1-grad) + 246*grad),
			B: uint8(255*(1-grad) + 255*grad),
			A: 255,
		}

		// Left accent bar along header + heading block (#2563EB -> #1D4ED8)
		headingCol := color.NRGBA{R: 37, G: 99, B: 235, A: 255}
		linePrimary := color.NRGBA{R: 51, G: 65, B: 85, A: 255}
		lineSecondary := color.NRGBA{R: 100, G: 116, B: 139, A: 255}

		// Top heading pill (e.g., # Title / [Section])
		if insideCapsule(u, v, 0.26, 0.25, 0.54, 0.042) {
			paper = headingCol
		} else if insideCapsule(u, v, 0.26, 0.41, 0.72, 0.032) {
			paper = linePrimary
		} else if insideCapsule(u, v, 0.26, 0.53, 0.72, 0.032) {
			paper = linePrimary
		} else if insideCapsule(u, v, 0.26, 0.65, 0.63, 0.032) {
			paper = lineSecondary
		} else if insideCapsule(u, v, 0.26, 0.77, 0.50, 0.032) {
			paper = lineSecondary
		}

		// Folded dog-ear flap triangle in top-right corner
		if insideFoldFlap(u, v, foldStartX, y0, x1, foldEndY) {
			paper = color.NRGBA{R: 191, G: 219, B: 254, A: 255}
		} else if sdPage <= -border && v <= foldEndY+0.035 && u >= foldStartX-0.02 && (u-foldStartX) > (v-y0) {
			// Soft crease shadow just below the dog-ear flap
			if v > foldEndY && v < foldEndY+0.030 {
				paper = blendOver(paper, color.NRGBA{R: 30, G: 64, B: 175, A: 36})
			}
		}

		// Seamless round-capped crease strokes along fold L-corner and outer border
		halfBorder := border * 0.5
		creaseRadius := border * 0.44
		foldCornerDist := math.Min(
			segDist(u, v, foldStartX, y0+halfBorder, foldStartX, foldEndY),
			segDist(u, v, foldStartX, foldEndY, x1-halfBorder, foldEndY),
		)
		if foldCornerDist <= creaseRadius || sdPage > -border {
			paper = color.NRGBA{R: 37, G: 99, B: 235, A: 255}
		}

		out = blendOver(out, paper)
	}

	return out
}

func pageSDF(u, v, x0, y0, x1, y1, r, foldStartX, foldEndY float64) float64 {
	// Signed distance to rounded rectangle
	cx := (x0 + x1) * 0.5
	cy := (y0 + y1) * 0.5
	hx := (x1-x0)*0.5 - r
	hy := (y1-y0)*0.5 - r
	qx := math.Abs(u-cx) - hx
	qy := math.Abs(v-cy) - hy
	rectDist := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r

	// Half-plane clip for top-right folded corner: (u - foldStartX) + (v - y0) <= (foldEndY - y0)
	// Normal pointing outward toward top-right is (1/sqrt(2), -1/sqrt(2))? Wait:
	// Diagonal passes through (foldStartX, y0) and (x1, foldEndY), where (x1 - foldStartX) == (foldEndY - y0) == foldS.
	// Line equation: u - foldStartX - (v - y0) = 0? No:
	// At (foldStartX, y0): u - foldStartX = 0, v - y0 = 0.
	// Wait: diagonal passes through (foldStartX, y0) top-left of square and (x1, foldEndY) bottom-right of square!
	// Inside the page (below-left of diagonal): u - foldStartX <= v - y0  =>  (u - foldStartX) - (v - y0) <= 0.
	diagDist := ((u - foldStartX) - (v - y0)) / math.Sqrt2
	return math.Max(rectDist, diagDist)
}

func insideFoldFlap(u, v, foldStartX, y0, x1, foldEndY float64) bool {
	return u >= foldStartX && v <= foldEndY && v >= y0 && u <= x1 && (u-foldStartX) <= (v-y0)
}

func segDist(px, py, x0, y0, x1, y1 float64) float64 {
	vx, vy := x1-x0, y1-y0
	l2 := vx*vx + vy*vy
	if l2 == 0 {
		return math.Hypot(px-x0, py-y0)
	}
	t := ((px-x0)*vx + (py-y0)*vy) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(x0+t*vx), py-(y0+t*vy))
}

func insideCapsule(u, v, x0, yc, x1, halfH float64) bool {
	cx := math.Max(x0+halfH, math.Min(u, x1-halfH))
	return math.Hypot(u-cx, v-yc) <= halfH
}

func blendOver(dst, src color.NRGBA) color.NRGBA {
	sa := float64(src.A) / 255.0
	da := float64(dst.A) / 255.0
	outA := sa + da*(1.0-sa)
	if outA <= 0.0001 {
		return color.NRGBA{}
	}
	r := (float64(src.R)*sa + float64(dst.R)*da*(1.0-sa)) / outA
	g := (float64(src.G)*sa + float64(dst.G)*da*(1.0-sa)) / outA
	b := (float64(src.B)*sa + float64(dst.B)*da*(1.0-sa)) / outA
	return color.NRGBA{R: clampU8(r), G: clampU8(g), B: clampU8(b), A: clampU8(outA * 255.0)}
}

func clampU8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func buildMultiICO(sizes []int, pngList [][]byte) []byte {
	var buf bytes.Buffer
	count := uint16(len(sizes))
	// ICONDIR header (6 bytes)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // Reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // Type: 1 = ICO
	_ = binary.Write(&buf, binary.LittleEndian, count)     // Count

	offset := uint32(6 + 16*len(sizes))
	for i, sz := range sizes {
		var dim uint8
		if sz < 256 {
			dim = uint8(sz)
		}
		pngData := pngList[i]
		buf.WriteByte(dim)                                                // Width (0 = 256)
		buf.WriteByte(dim)                                                // Height (0 = 256)
		buf.WriteByte(0)                                                  // ColorCount
		buf.WriteByte(0)                                                  // Reserved
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))            // Planes
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))           // BitCount
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngData))) // BytesInRes
		_ = binary.Write(&buf, binary.LittleEndian, offset)               // ImageOffset
		offset += uint32(len(pngData))
	}

	for _, pngData := range pngList {
		buf.Write(pngData)
	}
	return buf.Bytes()
}
