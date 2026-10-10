// CryptBridge (https://github.com/burnersen/CryptBridge)
// Copyright (C) 2026 burnersen
// SPDX-License-Identifier: GPL-3.0-only

// Package icon draws CryptBridge's padlock icon in code, so that the tray
// can show the state of the drive by colour and no image files are needed.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Colour is the colour of the padlock.
type Colour = color.NRGBA

// Colours of the tray icon for each state.
var (
	Connected    = Colour{R: 0x2e, G: 0xb8, B: 0x5c, A: 0xff} // green
	Disconnected = Colour{R: 0x8b, G: 0x94, B: 0x9e, A: 0xff} // grey
	Busy         = Colour{R: 0xf0, G: 0xa0, B: 0x20, A: 0xff} // amber
	Failed       = Colour{R: 0xe5, G: 0x48, B: 0x4d, A: 0xff} // red
	brand        = Colour{R: 0x1f, G: 0x6f, B: 0xeb, A: 0xff} // blue background of the app icon
)

// traySizes cover 100 % to 200 % display scaling.
var traySizes = []int{16, 20, 24, 32, 40, 48, 64}

// TrayICO returns a Windows .ico with the padlock in the given colour.
func TrayICO(c color.NRGBA) []byte {
	images := make([]*image.NRGBA, 0, len(traySizes))
	for _, size := range traySizes {
		images = append(images, Padlock(size, c))
	}
	return encodeICO(images)
}

// AppPNG returns the program icon: a white padlock on a blue rounded square.
func AppPNG(size int) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	lock := Padlock(size*3/4, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	offset := (size - size*3/4) / 2
	scale := float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			u, v := (float64(x)+0.5)/scale, (float64(y)+0.5)/scale
			bg := coverage(roundedRect(u, v, 0.5, 0.5, 0.46, 0.46, 0.18), scale)
			px := blend(color.NRGBA{}, brand, bg)
			lx, ly := x-offset, y-offset
			if lx >= 0 && ly >= 0 && lx < lock.Bounds().Dx() && ly < lock.Bounds().Dy() {
				fg := lock.NRGBAAt(lx, ly)
				px = blend(px, color.NRGBA{R: fg.R, G: fg.G, B: fg.B, A: 0xff}, float64(fg.A)/255)
			}
			img.SetNRGBA(x, y, px)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Padlock draws a padlock of size×size pixels in colour c on a transparent
// background. Shapes are signed distance functions in a 0..1 square; the
// distance gives smooth, anti-aliased edges at every size.
func Padlock(size int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			u, v := (float64(x)+0.5)/scale, (float64(y)+0.5)/scale
			alpha := coverage(padlockDistance(u, v), scale)
			if alpha > 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(math.Round(alpha * float64(c.A)))})
			}
		}
	}
	return img
}

// padlockDistance is negative inside the padlock and positive outside.
func padlockDistance(u, v float64) float64 {
	body := roundedRect(u, v, 0.5, 0.69, 0.38, 0.27, 0.09)

	// Shackle: the upper half of a ring plus two straight legs down into the body.
	const cx, cy, radius, half = 0.5, 0.40, 0.235, 0.075
	ring := math.Abs(math.Hypot(u-cx, v-cy)-radius) - half
	if v > cy {
		leftLeg := roundedRect(u, v, cx-radius, cy+0.05, half, 0.05, 0)
		rightLeg := roundedRect(u, v, cx+radius, cy+0.05, half, 0.05, 0)
		ring = math.Min(leftLeg, rightLeg)
	}

	keyhole := math.Min(
		math.Hypot(u-0.5, v-0.64)-0.065,
		roundedRect(u, v, 0.5, 0.73, 0.03, 0.08, 0.01),
	)
	return math.Max(math.Min(body, ring), -keyhole)
}

// roundedRect is the distance to a rectangle centred at (cx, cy) with half
// sizes (hw, hh) and corner radius r.
func roundedRect(u, v, cx, cy, hw, hh, r float64) float64 {
	dx := math.Abs(u-cx) - hw + r
	dy := math.Abs(v-cy) - hh + r
	outside := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	inside := math.Min(math.Max(dx, dy), 0)
	return outside + inside - r
}

// coverage turns a distance (in 0..1 units) into pixel coverage.
func coverage(distance, scale float64) float64 {
	return math.Max(0, math.Min(1, 0.5-distance*scale))
}

// blend paints top over bottom with the given amount (0..1).
func blend(bottom, top color.NRGBA, amount float64) color.NRGBA {
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-amount) + float64(b)*amount)) }
	return color.NRGBA{
		R: mix(bottom.R, top.R), G: mix(bottom.G, top.G), B: mix(bottom.B, top.B),
		A: uint8(math.Round(math.Min(255, float64(bottom.A)*(1-amount)+255*amount))),
	}
}

// encodeICO writes images as an .ico file with uncompressed 32-bit
// bitmaps, the format every Windows API reads at every size.
func encodeICO(images []*image.NRGBA) []byte {
	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(images))}) // reserved, type icon, count

	offset := 6 + 16*len(images)
	bodies := make([][]byte, len(images))
	for i, img := range images {
		bodies[i] = bitmapEntry(img)
		w := img.Bounds().Dx()
		dim := uint8(w)
		if w >= 256 {
			dim = 0
		}
		_ = binary.Write(&out, le, struct {
			Width, Height, Colors, Reserved uint8
			Planes, BitCount                uint16
			Size, Offset                    uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(bodies[i])), uint32(offset)})
		offset += len(bodies[i])
	}
	for _, body := range bodies {
		out.Write(body)
	}
	return out.Bytes()
}

// bitmapEntry encodes one image as BITMAPINFOHEADER + BGRA rows (bottom up)
// + an all-zero AND mask (transparency comes from the alpha channel).
func bitmapEntry(img *image.NRGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	maskRow := ((w + 31) / 32) * 4
	var buf bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, struct {
		Size                     uint32
		Width, Height            int32
		Planes, BitCount         uint16
		Compression, ImageSize   uint32
		XPerMeter, YPerMeter     int32
		ColorsUsed, ColorsImport uint32
	}{40, int32(w), int32(2 * h), 1, 32, 0, uint32(w*h*4 + maskRow*h), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			p := img.NRGBAAt(x, y)
			buf.Write([]byte{p.B, p.G, p.R, p.A})
		}
	}
	buf.Write(make([]byte, maskRow*h))
	return buf.Bytes()
}
