package tray

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Icon returns the tray icon as .ico bytes (16x16 and 32x32, 32-bit BGRA),
// generated in code so no binary asset is needed. Without a selection it is a
// gray ring; with a selected profile, a filled green disc.
func Icon(hasSelection bool) []byte {
	sizes := []int{16, 32}
	images := make([][]byte, len(sizes))
	for i, s := range sizes {
		images[i] = iconImage(s, hasSelection)
	}
	var b bytes.Buffer
	le := binary.LittleEndian
	// ICONDIR
	binary.Write(&b, le, uint16(0))
	binary.Write(&b, le, uint16(1))
	binary.Write(&b, le, uint16(len(sizes)))
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		// ICONDIRENTRY
		b.WriteByte(byte(s))
		b.WriteByte(byte(s))
		b.WriteByte(0)                   // palette colors
		b.WriteByte(0)                   // reserved
		binary.Write(&b, le, uint16(1))  // planes
		binary.Write(&b, le, uint16(32)) // bits per pixel
		binary.Write(&b, le, uint32(len(images[i])))
		binary.Write(&b, le, uint32(offset))
		offset += len(images[i])
	}
	for _, img := range images {
		b.Write(img)
	}
	return b.Bytes()
}

// iconImage returns one BMP-format icon image: BITMAPINFOHEADER, BGRA pixels
// bottom-up, then an all-zero AND mask (transparency comes from alpha).
func iconImage(size int, filled bool) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	maskRow := ((size + 31) / 32) * 4
	pixels := size * size * 4
	binary.Write(&b, le, uint32(40))    // biSize
	binary.Write(&b, le, int32(size))   // biWidth
	binary.Write(&b, le, int32(2*size)) // biHeight (XOR + AND)
	binary.Write(&b, le, uint16(1))     // biPlanes
	binary.Write(&b, le, uint16(32))    // biBitCount
	binary.Write(&b, le, uint32(0))     // biCompression (BI_RGB)
	binary.Write(&b, le, uint32(pixels+maskRow*size))
	binary.Write(&b, le, int32(0))
	binary.Write(&b, le, int32(0))
	binary.Write(&b, le, uint32(0))
	binary.Write(&b, le, uint32(0))

	c := float64(size) / 2
	outer := c - 0.5
	ring := math.Max(1.5, float64(size)/8)
	var fr, fg, fb float64 = 0x2E, 0xA0, 0x43 // green fill
	var gr, gg, gb float64 = 0x80, 0x80, 0x80 // gray ring
	const sub = 4                             // 4x4 supersampling
	for y := size - 1; y >= 0; y-- {          // bottom-up rows
		for x := 0; x < size; x++ {
			cover := 0.0
			for sy := 0; sy < sub; sy++ {
				for sx := 0; sx < sub; sx++ {
					px := float64(x) + (float64(sx)+0.5)/sub
					py := float64(y) + (float64(sy)+0.5)/sub
					d := math.Hypot(px-c, py-c)
					if d <= outer && (filled || d >= outer-ring) {
						cover++
					}
				}
			}
			a := cover / (sub * sub)
			r, g, bl := gr, gg, gb
			if filled {
				r, g, bl = fr, fg, fb
			}
			// Premultiplied-free BGRA.
			b.WriteByte(byte(bl))
			b.WriteByte(byte(g))
			b.WriteByte(byte(r))
			b.WriteByte(byte(math.Round(a * 255)))
		}
	}
	b.Write(make([]byte, maskRow*size))
	return b.Bytes()
}
