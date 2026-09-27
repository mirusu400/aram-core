package brewrt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const (
	displayAlignCenter   = uint32(0x0020)
	displayAlignLeft     = uint32(0x0010)
	displayAlignRight    = uint32(0x0040)
	displayAlignTop      = uint32(0x0100)
	displayAlignMiddle   = uint32(0x0200)
	displayAlignBottom   = uint32(0x0400)
	displayTextFormatOEM = uint32(0x10000)
	displayColorText     = uint32(1)
)

func (r *Runtime) drawDisplayText() error {
	textAddress, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW DrawText string: %w", err)
	}
	rawCount, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW DrawText count: %w", err)
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW DrawText stack: %w", err)
	}
	var arguments [16]byte
	if err := r.cpu.ReadMemory(sp, arguments[:]); err != nil {
		return fmt.Errorf("read BREW DrawText arguments: %w", err)
	}
	x := int32(binary.LittleEndian.Uint32(arguments[0:4]))
	y := int32(binary.LittleEndian.Uint32(arguments[4:8]))
	clipPointer := binary.LittleEndian.Uint32(arguments[8:12])
	flags := binary.LittleEndian.Uint32(arguments[12:16])
	text, err := r.displayText(textAddress, rawCount, flags&displayTextFormatOEM != 0)
	if err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	clip, err := r.displayTextClip(clipPointer)
	if err != nil {
		return err
	}
	if clip.Empty() {
		return nil
	}
	textWidth, err := r.displayTextWidth(text)
	if err != nil {
		return err
	}
	textHeight := int64(basicfont.Face7x13.Height)
	drawX, drawY := int64(x), int64(y)
	switch flags & 0x00f0 {
	case displayAlignLeft:
		drawX = int64(clip.Min.X)
	case displayAlignCenter:
		drawX = int64(clip.Min.X) + (int64(clip.Dx())-textWidth)/2
	case displayAlignRight:
		drawX = int64(clip.Max.X) - textWidth
	}
	switch flags & 0x0f00 {
	case displayAlignTop:
		drawY = int64(clip.Min.Y)
	case displayAlignMiddle:
		drawY = int64(clip.Min.Y) + (int64(clip.Dy())-textHeight)/2
	case displayAlignBottom:
		drawY = int64(clip.Max.Y) - textHeight
	}
	dirty := image.Rect(int(drawX), int(drawY), int(drawX+textWidth), int(drawY+textHeight)).Intersect(clip)
	if dirty.Empty() {
		return nil
	}

	pixels := make([]byte, r.screenBytes())
	if err := r.cpu.ReadMemory(framebufferBase, pixels); err != nil {
		return fmt.Errorf("read BREW DrawText framebuffer: %w", err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, int(r.screenWidth), int(r.screenHeight)))
	for py := uint32(0); py < r.screenHeight; py++ {
		for px := uint32(0); px < r.screenWidth; px++ {
			native := binary.LittleEndian.Uint16(pixels[(py*r.screenWidth+px)*2:])
			canvas.SetRGBA(int(px), int(py), color.RGBA{
				R: uint8((uint32(native>>11) * 255) / 31),
				G: uint8((uint32((native>>5)&0x3f) * 255) / 63),
				B: uint8((uint32(native&0x1f) * 255) / 31),
				A: 255,
			})
		}
	}
	foreground := color.RGBA{255, 255, 255, 255}
	if r.displayColorSet[displayColorText] {
		value := r.displayColors[displayColorText]
		foreground = color.RGBA{uint8(value >> 8), uint8(value >> 16), uint8(value >> 24), 255}
	} else if dirty.Min.X >= 0 && dirty.Min.Y >= 0 {
		background := canvas.RGBAAt(dirty.Min.X, dirty.Min.Y)
		if uint32(background.R)*299+uint32(background.G)*587+uint32(background.B)*114 > 128000 {
			foreground = color.RGBA{0, 0, 0, 255}
		}
	}
	// Some early handset titles set black text but rely on an OEM-owned light
	// background that is not present in their package. Avoid turning DrawText
	// into a complete no-op when the emulated destination is uniformly the same
	// color; use the opposite contrast only for that otherwise invisible span.
	uniformBackground := true
	for py := dirty.Min.Y; py < dirty.Max.Y && uniformBackground; py++ {
		for px := dirty.Min.X; px < dirty.Max.X; px++ {
			if canvas.RGBAAt(px, py) != canvas.RGBAAt(dirty.Min.X, dirty.Min.Y) {
				uniformBackground = false
				break
			}
		}
	}
	background := canvas.RGBAAt(dirty.Min.X, dirty.Min.Y)
	if uniformBackground && displayRGB565(foreground) == displayRGB565(background) {
		if uint32(background.R)*299+uint32(background.G)*587+uint32(background.B)*114 > 128000 {
			foreground = color.RGBA{0, 0, 0, 255}
		} else {
			foreground = color.RGBA{255, 255, 255, 255}
		}
	}
	drawer := font.Drawer{
		Dst:  canvas.SubImage(clip).(*image.RGBA),
		Src:  image.NewUniform(foreground),
		Face: basicfont.Face7x13,
	}
	cursor := int(drawX)
	for _, character := range text {
		if character <= 0xff {
			drawer.Dot = fixed.P(cursor, int(drawY)+basicfont.Face7x13.Ascent)
			drawer.DrawString(string(character))
			cursor += 7
			continue
		}
		glyph, err := r.textRaster.Glyph(1, r.displayFont, character)
		if err != nil {
			return fmt.Errorf("rasterize BREW DrawText glyph %U: %w", character, err)
		}
		drawDisplayGlyph(canvas, clip, cursor, int(drawY), foreground, glyph)
		cursor += int(glyph.Advance)
	}
	for py := dirty.Min.Y; py < dirty.Max.Y; py++ {
		for px := dirty.Min.X; px < dirty.Max.X; px++ {
			value := canvas.RGBAAt(px, py)
			native := uint16(value.R>>3)<<11 | uint16(value.G>>2)<<5 | uint16(value.B>>3)
			binary.LittleEndian.PutUint16(pixels[(uint32(py)*r.screenWidth+uint32(px))*2:], native)
		}
		start := (uint32(py)*r.screenWidth + uint32(dirty.Min.X)) * 2
		end := (uint32(py)*r.screenWidth + uint32(dirty.Max.X)) * 2
		if err := r.cpu.WriteMemory(framebufferBase+start, pixels[start:end]); err != nil {
			return fmt.Errorf("write BREW DrawText framebuffer: %w", err)
		}
	}
	return nil
}

func (r *Runtime) displayTextWidth(value string) (int64, error) {
	var width int64
	for _, character := range value {
		if character <= 0xff {
			width += 7
			continue
		}
		glyph, err := r.textRaster.Glyph(1, r.displayFont, character)
		if err != nil {
			return 0, fmt.Errorf("measure BREW DrawText glyph %U: %w", character, err)
		}
		width += int64(glyph.Advance)
	}
	return width, nil
}

func drawDisplayGlyph(
	canvas *image.RGBA,
	clip image.Rectangle,
	x, y int,
	foreground color.RGBA,
	glyph shared.Glyph,
) {
	for row := int32(0); row < glyph.Height; row++ {
		for column := int32(0); column < glyph.Width; column++ {
			alpha := glyph.Alpha[row*glyph.Width+column]
			if alpha == 0 {
				continue
			}
			px := x + int(glyph.BearingX+column)
			py := y + int(glyph.BearingY+row)
			if !image.Pt(px, py).In(clip) {
				continue
			}
			background := canvas.RGBAAt(px, py)
			inverse := uint32(255 - alpha)
			coverage := uint32(alpha)
			canvas.SetRGBA(px, py, color.RGBA{
				R: uint8((uint32(foreground.R)*coverage + uint32(background.R)*inverse) / 255),
				G: uint8((uint32(foreground.G)*coverage + uint32(background.G)*inverse) / 255),
				B: uint8((uint32(foreground.B)*coverage + uint32(background.B)*inverse) / 255),
				A: 255,
			})
		}
	}
}

func displayRGB565(value color.RGBA) uint16 {
	return uint16(value.R>>3)<<11 | uint16(value.G>>2)<<5 | uint16(value.B>>3)
}

func (r *Runtime) displayTextClip(address uint32) (image.Rectangle, error) {
	clip := image.Rect(0, 0, int(r.screenWidth), int(r.screenHeight))
	if address == 0 {
		return clip, nil
	}
	var encoded [8]byte
	if err := r.cpu.ReadMemory(address, encoded[:]); err != nil {
		return image.Rectangle{}, fmt.Errorf("read BREW DrawText clipping rectangle: %w", err)
	}
	x := int32(int16(binary.LittleEndian.Uint16(encoded[0:2])))
	y := int32(int16(binary.LittleEndian.Uint16(encoded[2:4])))
	width := int32(int16(binary.LittleEndian.Uint16(encoded[4:6])))
	height := int32(int16(binary.LittleEndian.Uint16(encoded[6:8])))
	if width <= 0 || height <= 0 {
		return image.Rectangle{}, nil
	}
	return image.Rect(int(x), int(y), int(x+width), int(y+height)).Intersect(clip), nil
}

func (r *Runtime) displayText(address, rawCount uint32, oem bool) (string, error) {
	if address == 0 {
		return "", nil
	}
	if oem {
		var raw []byte
		if rawCount == ^uint32(0) {
			if address > ^uint32(0)-4095 {
				return "", fmt.Errorf("BREW OEM DrawText string address 0x%08x exceeds range", address)
			}
			value, err := r.readCString(address)
			if err != nil {
				return "", fmt.Errorf("read BREW OEM DrawText string: %w", err)
			}
			raw = []byte(value)
		} else if int32(rawCount) < 0 {
			return "", fmt.Errorf("BREW OEM DrawText count %d is invalid", int32(rawCount))
		} else {
			if rawCount > 4096 {
				return "", fmt.Errorf("BREW OEM DrawText count %d exceeds limit", rawCount)
			}
			raw = make([]byte, rawCount)
			if err := r.cpu.ReadMemory(address, raw); err != nil {
				return "", fmt.Errorf("read BREW OEM DrawText bytes: %w", err)
			}
		}
		decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), raw)
		if err != nil {
			return "", fmt.Errorf("decode BREW OEM DrawText string: %w", err)
		}
		return string(decoded), nil
	}
	var units []uint16
	if rawCount == ^uint32(0) {
		var encoded [2]byte
		for count := uint32(0); count < 4096; count++ {
			if address > ^uint32(0)-count*2-1 {
				return "", fmt.Errorf("BREW DrawText string address 0x%08x exceeds range", address)
			}
			if err := r.cpu.ReadMemory(address+count*2, encoded[:]); err != nil {
				return "", fmt.Errorf("read BREW DrawText string: %w", err)
			}
			unit := binary.LittleEndian.Uint16(encoded[:])
			if unit == 0 {
				return decodeBREWAECHARPreferred(units, r.preferPackedAECHAR), nil
			}
			units = append(units, unit)
		}
		return "", fmt.Errorf("BREW DrawText string at 0x%08x exceeded 4096 characters", address)
	} else if int32(rawCount) < 0 {
		return "", fmt.Errorf("BREW DrawText count %d is invalid", int32(rawCount))
	} else {
		if rawCount > 4096 {
			return "", fmt.Errorf("BREW DrawText count %d exceeds limit", rawCount)
		}
		raw := make([]byte, rawCount*2)
		if err := r.cpu.ReadMemory(address, raw); err != nil {
			return "", fmt.Errorf("read BREW DrawText characters: %w", err)
		}
		units = make([]uint16, rawCount)
		for index := range units {
			units[index] = binary.LittleEndian.Uint16(raw[index*2:])
		}
	}
	return decodeBREWAECHARPreferred(units, r.preferPackedAECHAR), nil
}

// STREXPAND on Korean BREW handsets can place one EUC-KR double-byte code in
// each AECHAR. Many games also pass genuine UTF-16, so only select this path
// when every non-ASCII unit is a valid-looking Korean pair and the string has
// evidence it is not ordinary precomposed Hangul. Otherwise leave UTF-16
// untouched, including surrogate pairs.
func decodeBREWAECHAR(units []uint16) string {
	return decodeBREWAECHARPreferred(units, false)
}

func decodeBREWAECHARPreferred(units []uint16, preferPacked bool) string {
	unicodeText := string(utf16.Decode(units))
	packed := make([]byte, 0, len(units)*2)
	evidence := preferPacked
	for _, unit := range units {
		if unit < 0x80 {
			packed = append(packed, byte(unit))
			continue
		}
		lead, trail := byte(unit), byte(unit>>8)
		if lead < 0xa1 || trail < 0xa1 {
			return unicodeText
		}
		if unit < 0xac00 || unit > 0xd7a3 || lead < 0xb0 {
			evidence = true
		}
		packed = append(packed, lead, trail)
	}
	if !evidence {
		return unicodeText
	}
	decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), packed)
	if err != nil || bytes.Contains(decoded, []byte("\xef\xbf\xbd")) {
		return unicodeText
	}
	return string(decoded)
}
