package tray

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Icon returns the tray icon for a dark taskbar as .ico bytes.
func Icon(hasSelection bool) []byte { return IconFor(hasSelection, false) }

// IconFor returns the tray icon as .ico bytes (16x16 and 32x32, 32-bit BGRA),
// drawn in code so no binary asset is needed. The mark is a pair of braces
// { } (variables, config) around two chevrons (stacked layers that swap): the
// left brace and lower chevron in olive, the right brace and upper chevron in
// white, the white layer drawn over the olive one. With no selected profile
// every stroke is gray. On a light taskbar the white layer turns dark.
func IconFor(hasSelection, lightTaskbar bool) []byte {
	sizes := []int{16, 32}
	images := make([][]byte, len(sizes))
	for i, s := range sizes {
		images[i] = iconImage(s, paletteFor(hasSelection, lightTaskbar))
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

type rgb struct{ r, g, b float64 }

// palette is the two stroke layers: back (olive side) and front (white side).
type palette struct {
	back, front rgb
	frontAlpha  float64
}

func paletteFor(active, light bool) palette {
	switch {
	case active && !light:
		return palette{rgb{0xA6, 0xAB, 0x5B}, rgb{0xED, 0xED, 0xED}, 0.9}
	case active && light:
		return palette{rgb{0x7F, 0x85, 0x30}, rgb{0x2B, 0x2B, 0x2B}, 0.88}
	case !light:
		return palette{rgb{0x7A, 0x80, 0x8A}, rgb{0xB4, 0xB9, 0xC1}, 0.85}
	default:
		return palette{rgb{0x9A, 0xA0, 0xA9}, rgb{0x6B, 0x70, 0x78}, 0.85}
	}
}

type pt struct{ x, y float64 }

// stroke is a polyline drawn with a round pen of width w (round caps and joins).
type stroke struct {
	pts   []pt
	w     float64
	small float64 // width multiplier for the small (16px) image
}

func quad(a, c, b pt, steps int) []pt {
	out := make([]pt, 0, steps)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		u := 1 - t
		out = append(out, pt{u*u*a.x + 2*u*t*c.x + t*t*b.x, u*u*a.y + 2*u*t*c.y + t*t*b.y})
	}
	return out
}

func path(start pt, segs ...[]pt) []pt {
	out := []pt{start}
	for _, s := range segs {
		out = append(out, s...)
	}
	return out
}

// Geometry in a 32x32 box.
var (
	braceW   = 3.2
	chevronW = 2.8

	leftBrace = stroke{path(pt{11, 5},
		quad(pt{11, 5}, pt{6, 5}, pt{6, 10}, 8),
		[]pt{{6, 12}},
		quad(pt{6, 12}, pt{6, 16}, pt{3, 16}, 8),
		quad(pt{3, 16}, pt{6, 16}, pt{6, 20}, 8),
		[]pt{{6, 22}},
		quad(pt{6, 22}, pt{6, 27}, pt{11, 27}, 8),
	), braceW, 1.25}
	rightBrace = stroke{path(pt{21, 5},
		quad(pt{21, 5}, pt{26, 5}, pt{26, 10}, 8),
		[]pt{{26, 12}},
		quad(pt{26, 12}, pt{26, 16}, pt{29, 16}, 8),
		quad(pt{29, 16}, pt{26, 16}, pt{26, 20}, 8),
		[]pt{{26, 22}},
		quad(pt{26, 22}, pt{26, 27}, pt{21, 27}, 8),
	), braceW, 1.25}
	upChevron   = stroke{[]pt{{12, 13}, {16, 9}, {20, 13}}, chevronW, 1.0}
	downChevron = stroke{[]pt{{12, 19}, {16, 23}, {20, 19}}, chevronW, 1.0}

	backLayer  = []stroke{leftBrace, downChevron}
	frontLayer = []stroke{rightBrace, upChevron}
)

func segDist2(p, a, b pt) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	l2 := dx*dx + dy*dy
	t := 0.0
	if l2 > 0 {
		t = math.Max(0, math.Min(1, ((p.x-a.x)*dx+(p.y-a.y)*dy)/l2))
	}
	ex, ey := p.x-(a.x+t*dx), p.y-(a.y+t*dy)
	return ex*ex + ey*ey
}

func covers(layer []stroke, p pt, small bool) bool {
	for _, s := range layer {
		w := s.w
		if small {
			w *= s.small
		}
		r2 := w * w / 4
		for i := 1; i < len(s.pts); i++ {
			if segDist2(p, s.pts[i-1], s.pts[i]) <= r2 {
				return true
			}
		}
	}
	return false
}

// iconImage returns one BMP-format icon image: BITMAPINFOHEADER, BGRA pixels
// bottom-up, then an all-zero AND mask (transparency comes from alpha).
func iconImage(size int, pal palette) []byte {
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

	scale := 32 / float64(size)
	small := size < 32               // thicker braces keep the small image readable
	const sub = 4                    // 4x4 supersampling
	for y := size - 1; y >= 0; y-- { // bottom-up rows
		for x := 0; x < size; x++ {
			var pr, pg, pb, pa float64 // premultiplied sums
			for sy := 0; sy < sub; sy++ {
				for sx := 0; sx < sub; sx++ {
					p := pt{(float64(x) + (float64(sx)+0.5)/sub) * scale, (float64(y) + (float64(sy)+0.5)/sub) * scale}
					var r, g, bl, a float64
					if covers(backLayer, p, small) {
						r, g, bl, a = pal.back.r, pal.back.g, pal.back.b, 1
					}
					if covers(frontLayer, p, small) {
						fa := pal.frontAlpha
						r = pal.front.r*fa + r*a*(1-fa)
						g = pal.front.g*fa + g*a*(1-fa)
						bl = pal.front.b*fa + bl*a*(1-fa)
						a = fa + a*(1-fa)
					} else {
						r, g, bl = r*a, g*a, bl*a
					}
					pr += r
					pg += g
					pb += bl
					pa += a
				}
			}
			n := float64(sub * sub)
			if pa == 0 {
				b.Write([]byte{0, 0, 0, 0})
				continue
			}
			b.WriteByte(byte(math.Round(pb / pa)))
			b.WriteByte(byte(math.Round(pg / pa)))
			b.WriteByte(byte(math.Round(pr / pa)))
			b.WriteByte(byte(math.Round(pa / n * 255)))
		}
	}
	b.Write(make([]byte, maskRow*size))
	return b.Bytes()
}
