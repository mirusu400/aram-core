package wipi

import (
	"encoding/binary"
	"fmt"
	"image"
	"unicode/utf8"

	shared "github.com/mirusu400/aram-core/runtime"
)

// OEMText rasterizes into an OEM software surface using the shared handset font.
// A zero pixel address measures the same text without writing guest memory.
func (r *Runtime) OEMText(pixels uint32, width, height, x, y int, color uint32,
	text []byte, size int, clip image.Rectangle) (int, error) {
	encoding := shared.EncodingUTF8
	if !utf8.Valid(text) {
		encoding = shared.EncodingEUCKR
	}
	decoded, err := r.Services.Text.Decode(text, encoding)
	if err != nil {
		return 0, err
	}
	font, err := r.Services.Text.EnsureFont(r.ServiceOwner, shared.FontDescriptor{
		Family: "aram-fallback", Size: int32(size),
	})
	if err != nil {
		return 0, err
	}
	advance, err := r.Services.Text.Measure(r.ServiceOwner, font, decoded)
	if err != nil || pixels == 0 {
		return int(advance), err
	}
	if width <= 0 || height <= 0 || uint64(width)*uint64(height) > 16<<20 {
		return 0, fmt.Errorf("invalid OEM text surface")
	}
	clip = clip.Intersect(image.Rect(0, 0, width, height))
	cursor := x
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], color&0xffffff)
	for _, character := range decoded {
		glyph, err := r.Services.Text.Glyph(r.ServiceOwner, font, character)
		if err != nil {
			return 0, err
		}
		for row := 0; row < int(glyph.Height); row++ {
			for column := 0; column < int(glyph.Width); column++ {
				target := image.Pt(cursor+int(glyph.BearingX)+column, y+int(glyph.BearingY)+row)
				if !target.In(clip) || glyph.Alpha[row*int(glyph.Width)+column] == 0 {
					continue
				}
				offset := uint64(pixels) + uint64(target.Y*width+target.X)*4
				if offset+4 > 1<<32 {
					return 0, fmt.Errorf("OEM text address overflows")
				}
				if err := r.CPU.WriteMemory(uint32(offset), encoded[:]); err != nil {
					return 0, err
				}
			}
		}
		cursor += int(glyph.Advance)
	}
	return int(advance), nil
}
