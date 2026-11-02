package gvmhost

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"sync"

	"github.com/mirusu400/aram-core/gvm"
	shared "github.com/mirusu400/aram-core/runtime"
)

var ErrInvalidDisplayConfig = errors.New("gvmhost: invalid display configuration")

// DisplayOrientation explicitly selects the presentation conversion. The zero
// value is the mapper-backed native orientation.
type DisplayOrientation uint8

const (
	DisplayOrientationDefault DisplayOrientation = iota
	DisplayOrientationAlternate
)

// PaletteMapper supplies the policy that is not established by the recovered
// GVM implementation. Map converts a selector under the selected mapping row
// to an indexed drawing byte. Color converts that byte for default-orientation
// presentation.
type PaletteMapper interface {
	Map(mapping uint8, selector int16) (byte, error)
	Color(index byte) color.RGBA
}

// PhasedPaletteMapper is a PaletteMapper with blink phases. Phase 0 must map
// exactly like Map.
type PhasedPaletteMapper interface {
	PaletteMapper
	MapPhase(mapping, phase uint8, selector int16) (byte, error)
}

// maxGVMPalettePhase is the last of GVM2X's six blink phases.
const maxGVMPalettePhase = 5

// FramePublisher receives one immutable image for each successful guest
// presentation. Implementations must not call back into DisplayAdapter.
type FramePublisher interface {
	PublishGVMFrame(image.Image) error
}

type DisplayConfig struct {
	Width  int
	Height int
	// Origin translates the guest's drawing coordinates into framebuffer pixels.
	OriginX     int
	OriginY     int
	Orientation DisplayOrientation
	Palette     PaletteMapper
	Publisher   FramePublisher
	Text        *shared.Text
	TextOwner   shared.OwnerID
}

// DisplayAdapter owns all GVM indexed display state. Its methods are serialized
// so each guest operation is atomic with respect to every other operation.
type DisplayAdapter struct {
	mu          sync.Mutex
	drawing     indexedSurface
	auxiliary   indexedSurface
	orientation DisplayOrientation
	palette     PaletteMapper
	publisher   FramePublisher
	mapping     uint8
	phase       uint8
	selector    uint8
	activeColor byte
	originX     int
	originY     int
	text        *shared.Text
	textOwner   shared.OwnerID
	textFonts   [4]shared.ServiceID
}

func NewDisplayAdapter(config DisplayConfig) (*DisplayAdapter, error) {
	if config.Width < 1 || config.Width > 256 || config.Height < 1 || config.Height > 256 ||
		config.Orientation > DisplayOrientationAlternate || config.Palette == nil || config.Publisher == nil {
		return nil, ErrInvalidDisplayConfig
	}
	active, err := config.Palette.Map(0, 0)
	if err != nil {
		return nil, errors.Join(ErrInvalidDisplayConfig, err)
	}
	return &DisplayAdapter{
		drawing:     newIndexedSurface(config.Width, config.Height),
		auxiliary:   newIndexedSurface(config.Width, config.Height),
		orientation: config.Orientation,
		palette:     config.Palette,
		publisher:   config.Publisher,
		activeColor: active,
		originX:     config.OriginX,
		originY:     config.OriginY,
		text:        config.Text,
		textOwner:   config.TextOwner,
	}, nil
}

func (d *DisplayAdapter) ClearGVMDisplay() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	fillBytes(d.drawing.pixels, 0xff)
	return nil
}

func (d *DisplayAdapter) ZeroGVMDisplay() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	clear(d.drawing.pixels)
	return nil
}

func (d *DisplayAdapter) FillGVMDisplay(selector int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	mapped, err := d.mapColor(d.mapping, d.phase, selector)
	if err != nil {
		return err
	}
	fillBytes(d.drawing.pixels, mapped)
	return nil
}

func (d *DisplayAdapter) CopyGVMDisplay(source, destination gvm.DisplayBuffer) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	src, ok := d.surface(source)
	if !ok {
		return ErrInvalidDisplayConfig
	}
	dst, ok := d.surface(destination)
	if !ok {
		return ErrInvalidDisplayConfig
	}
	copy(dst.pixels, src.pixels)
	return nil
}

func (d *DisplayAdapter) SelectGVMMapping(selector uint8) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	mapped, err := d.mapColor(selector, d.phase, int16(d.selector))
	if err != nil {
		return err
	}
	d.mapping, d.activeColor = selector, mapped
	return nil
}

func (d *DisplayAdapter) SelectGVMColor(selector uint8) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	mapped, err := d.mapColor(d.mapping, d.phase, int16(selector))
	if err != nil {
		return err
	}
	d.selector, d.activeColor = selector, mapped
	return nil
}

// SetGVMPalettePhase selects one of the six blink phases, as GVM2X's slot-0
// timer does with its frame counter modulo six (sub_40ED50). Later drawing
// and the current drawing color use the new phase; pixels already drawn keep
// their packed values. Phases outside 0..5 are rejected without a change.
func (d *DisplayAdapter) SetGVMPalettePhase(phase uint8) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if phase > maxGVMPalettePhase {
		return fmt.Errorf("%w: palette phase %d", ErrInvalidDisplayConfig, phase)
	}
	mapped, err := d.mapColor(d.mapping, phase, int16(d.selector))
	if err != nil {
		return err
	}
	d.phase, d.activeColor = phase, mapped
	return nil
}

// mapColor maps through the blink phase when the palette has phases. Phase 0
// is the steady row every PaletteMapper provides.
func (d *DisplayAdapter) mapColor(mapping, phase uint8, selector int16) (byte, error) {
	if phase != 0 {
		if phased, ok := d.palette.(PhasedPaletteMapper); ok {
			return phased.MapPhase(mapping, phase, selector)
		}
	}
	return d.palette.Map(mapping, selector)
}

func (d *DisplayAdapter) FillGVMRectangle(x1, y1, x2, y2 int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.selector == 4 {
		return nil
	}
	left, right := ordered(int(x1)+d.originX, int(x2)+d.originX)
	top, bottom := ordered(int(y1)+d.originY, int(y2)+d.originY)
	left, top = max(left, 0), max(top, 0)
	right, bottom = min(right, d.drawing.width-1), min(bottom, d.drawing.height-1)
	for y := top; y <= bottom; y++ {
		for x := left; x <= right; x++ {
			d.drawing.pixels[y*d.drawing.width+x] = d.activeColor
		}
	}
	return nil
}

func (d *DisplayAdapter) DrawGVMRectangle(x1, y1, x2, y2 int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.selector == 4 {
		return nil
	}
	left, right := ordered(int(x1)+d.originX, int(x2)+d.originX)
	top, bottom := ordered(int(y1)+d.originY, int(y2)+d.originY)
	d.drawHorizontal(left, right, top)
	d.drawHorizontal(left, right, bottom)
	d.drawVertical(top, bottom, left)
	d.drawVertical(top, bottom, right)
	return nil
}

func (d *DisplayAdapter) DrawGVMLine(x1, y1, x2, y2 int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.selector == 4 {
		return nil
	}
	x, y := int(x1)+d.originX, int(y1)+d.originY
	endX, endY := int(x2)+d.originX, int(y2)+d.originY
	dx := endX - x
	if dx < 0 {
		dx = -dx
	}
	dy := endY - y
	if dy < 0 {
		dy = -dy
	}
	signX, signY := 1, 1
	if x > endX {
		signX = -1
	}
	if y > endY {
		signY = -1
	}
	decision := dx - dy
	for {
		if x >= 0 && x < d.drawing.width && y >= 0 && y < d.drawing.height {
			d.drawing.pixels[y*d.drawing.width+x] = d.activeColor
		}
		if x == endX && y == endY {
			break
		}
		twice := 2 * decision
		if twice > -dy {
			decision -= dy
			x += signX
		}
		if twice < dx {
			decision += dx
			y += signY
		}
	}
	return nil
}

func (d *DisplayAdapter) DrawGVMPoint(x, y, selector int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if selector == 4 {
		return nil
	}
	mapped, err := d.mapColor(d.mapping, d.phase, selector)
	if err != nil {
		return err
	}
	px, py := int(x)+d.originX, int(y)+d.originY
	if px >= 0 && px < d.drawing.width && py >= 0 && py < d.drawing.height {
		d.drawing.pixels[py*d.drawing.width+px] = mapped
	}
	return nil
}

func (d *DisplayAdapter) DrawGVMEllipse(x, y, radiusX, radiusY int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.selector == 4 {
		return nil
	}
	cx, cy := int(x)+d.originX, int(y)+d.originY
	// Native opcode65 fills one horizontal span per row on each side.
	fillGVMEllipse(int(radiusX), int(radiusY), func(dx, dy int) {
		d.drawHorizontal(cx-dx, cx+dx, cy+dy)
		d.drawHorizontal(cx-dx, cx+dx, cy-dy)
	})
	return nil
}

// DrawGVMEllipseOutline plots opcode64's four symmetric outline points per
// step of the native midpoint ellipse.
func (d *DisplayAdapter) DrawGVMEllipseOutline(x, y, radiusX, radiusY int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.selector == 4 {
		return nil
	}
	cx, cy := int(x)+d.originX, int(y)+d.originY
	plot := func(px, py int) {
		if px >= 0 && px < d.drawing.width && py >= 0 && py < d.drawing.height {
			d.drawing.pixels[py*d.drawing.width+px] = d.activeColor
		}
	}
	outlineGVMEllipse(int(radiusX), int(radiusY), func(dx, dy int) {
		plot(cx+dx, cy+dy)
		plot(cx+dx, cy-dy)
		plot(cx-dx, cy+dy)
		plot(cx-dx, cy-dy)
	})
	return nil
}

// outlineGVMEllipse is the GVM2X midpoint ellipse behind opcode64. It reports
// quadrant offsets (x, y) with the native double-precision decision values and
// step order, including its sparse steep region; callers mirror each offset.
func outlineGVMEllipse(radiusX, radiusY int, plot func(x, y int)) {
	rx2 := radiusX * radiusX
	ry := max(radiusY, -radiusY)
	ry2 := ry * ry
	x, y, yRef := 0, ry, ry
	decision := float64((rx2 >> 2) + rx2*(1-y))
	plot(0, y)
	frx2, fry2 := float64(rx2), float64(ry2)
	if (float64(ry)-0.5)*frx2 > fry2 {
		stepX, accumulated, stepY := 3*ry2, ry2, rx2*(2-2*ry)
		for {
			x++
			accumulated += ry2
			if decision >= 0 {
				y--
				decision += float64(stepY + stepX)
				yRef = y
				stepY += 2 * rx2
			} else {
				decision += float64(stepX)
			}
			stepX += 2 * ry2
			plot(x, y)
			if float64(accumulated) >= (float64(yRef)-0.5)*frx2 {
				break
			}
		}
	}
	if y <= 0 {
		return
	}
	decision = float64(rx2*(y-1)*(y-1)) + fry2*(float64(x)+0.5)*(float64(x)+0.5) - float64(rx2*ry2)
	stepX, stepY := 2*ry2*(x+1), rx2*(3-2*y)
	for {
		next := float64(stepY) + decision
		if decision < 0 {
			x++
			next = float64(stepY+stepX) + decision
			stepX += 2 * ry2
		}
		y--
		stepY += 2 * rx2
		plot(x, y)
		if y <= 0 {
			return
		}
		decision = next
	}
}

// fillGVMEllipse is the GVM2X midpoint ellipse behind opcode65. It reports one
// (half width, row offset) span per visited row in the native order.
func fillGVMEllipse(radiusX, radiusY int, span func(x, y int)) {
	rx2 := radiusX * radiusX
	ry := max(radiusY, -radiusY)
	ry2 := ry * ry
	x, y, yRef := 0, ry, ry
	decision := float64((rx2 >> 2) + rx2*(1-y))
	span(0, y)
	frx2, fry2 := float64(rx2), float64(ry2)
	regionX := 0
	if (float64(ry)-0.5)*frx2 > fry2 {
		stepX, accumulated, stepY := 3*ry2, ry2, rx2*(2-2*ry)
		for {
			if decision >= 0 {
				span(x, y)
				x++
				accumulated += ry2
				decision += float64(stepY + stepX)
				y--
				stepX += 2 * ry2
				yRef = y
				stepY += 2 * rx2
			} else {
				x++
				decision += float64(stepX)
				accumulated += ry2
				stepX += 2 * ry2
			}
			if float64(accumulated) >= (float64(yRef)-0.5)*frx2 {
				break
			}
		}
		regionX = x
	}
	span(x, y)
	if y <= 0 {
		return
	}
	decision = float64(rx2*(y-1)*(y-1)) + fry2*(float64(regionX)+0.5)*(float64(regionX)+0.5) - float64(rx2*ry2)
	stepX, stepY := 2*ry2*(x+1), rx2*(3-2*y)
	for {
		next := float64(stepY) + decision
		if decision < 0 {
			x++
			next = float64(stepY+stepX) + decision
			stepX += 2 * ry2
		}
		y--
		stepY += 2 * rx2
		span(x, y)
		if y <= 0 {
			return
		}
		decision = next
	}
}

func (d *DisplayAdapter) drawHorizontal(x1, x2, y int) {
	if y < 0 || y >= d.drawing.height || x2 < 0 || x1 >= d.drawing.width {
		return
	}
	x1, x2 = max(x1, 0), min(x2, d.drawing.width-1)
	for x := x1; x <= x2; x++ {
		d.drawing.pixels[y*d.drawing.width+x] = d.activeColor
	}
}

func (d *DisplayAdapter) drawVertical(y1, y2, x int) {
	if x < 0 || x >= d.drawing.width || y2 < 0 || y1 >= d.drawing.height {
		return
	}
	y1, y2 = max(y1, 0), min(y2, d.drawing.height-1)
	for y := y1; y <= y2; y++ {
		d.drawing.pixels[y*d.drawing.width+x] = d.activeColor
	}
}

// nativeSkipsGVMSprite reports resources the GVM2X sprite entry points
// (0x4100f0 and 0x4107b0) draw nothing for. They ignore a kind outside 2..8,
// and they read the width and height straight from the header without
// bounding it by the record, so a zero width or height draws nothing even
// when the record ends right after that byte.
func nativeSkipsGVMSprite(resource []byte) bool {
	if len(resource) == 0 {
		return false
	}
	if resource[0] < 2 || resource[0] > 8 {
		return true
	}
	return len(resource) > 1 && resource[1] == 0 || len(resource) > 2 && resource[2] == 0
}

func (d *DisplayAdapter) DrawGVMSprite(resource []byte, x, y int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if nativeSkipsGVMSprite(resource) {
		return nil
	}
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
	mapped, err := d.mapSprite(sprite)
	if err != nil {
		return err
	}
	rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x)+d.originX, int(y)+d.originY)
	return nil
}

func (d *DisplayAdapter) DrawGVMTransformedSprite(resource []byte, x, y int16, mirrorHorizontal bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if nativeSkipsGVMSprite(resource) {
		return nil
	}
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
	mapped, err := d.mapSprite(sprite)
	if err != nil {
		return err
	}
	if mirrorHorizontal {
		rasterizeMirroredSprite(&d.drawing, sprite, mapped, int(x)+d.originX, int(y)+d.originY)
	} else {
		rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x)+d.originX, int(y)+d.originY)
	}
	return nil
}

func (d *DisplayAdapter) DrawGVMSpriteWithPalette(resource, palette []byte, x, y int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if nativeSkipsGVMSprite(resource) {
		return nil
	}
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
	if err := sprite.useGuestPalette(resource[0], palette); err != nil {
		return err
	}
	mapped, err := d.mapSprite(sprite)
	if err != nil {
		return err
	}
	rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x)+d.originX, int(y)+d.originY)
	return nil
}

func (d *DisplayAdapter) DrawGVMTransformedSpriteWithPalette(resource, palette []byte, x, y int16, mirrorHorizontal bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if nativeSkipsGVMSprite(resource) {
		return nil
	}
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
	if err := sprite.useGuestPalette(resource[0], palette); err != nil {
		return err
	}
	mapped, err := d.mapSprite(sprite)
	if err != nil {
		return err
	}
	if mirrorHorizontal {
		rasterizeMirroredSprite(&d.drawing, sprite, mapped, int(x)+d.originX, int(y)+d.originY)
	} else {
		rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x)+d.originX, int(y)+d.originY)
	}
	return nil
}

func (d *DisplayAdapter) DrawGVMText(resource []byte, x, y int16, style gvm.TextDrawStyle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.text == nil || style.Mode > 3 || style.Primary > 181 || style.Alignment > 2 {
		return ErrInvalidDisplayConfig
	}
	terminated := resource
	for index, value := range resource {
		if value == 0 {
			terminated = resource[:index]
			break
		}
	}
	if len(terminated) == 0 || style.Primary == 4 {
		return nil
	}
	// Enforce the shared service's configured string limit independently from
	// native tokenization. The reference consumes every high-bit byte as a
	// two-byte token and substitutes one fallback glyph for invalid pairs.
	if _, err := d.text.Decode(make([]byte, len(terminated)), shared.EncodingUTF8); err != nil {
		return err
	}
	fontID := d.textFonts[style.Mode]
	var err error
	if fontID == 0 {
		sizes := [...]int32{6, 8, 12, 24}
		fontID, err = d.text.EnsureFont(d.textOwner, shared.FontDescriptor{
			Family: "aram-fallback", Size: sizes[style.Mode],
		})
		if err != nil {
			return err
		}
		d.textFonts[style.Mode] = fontID
	}
	type positionedGlyph struct {
		alpha  []byte
		width  int
		height int
		x      int
	}
	units, err := gvmTextUnits(d.text, terminated, style.Mode)
	if err != nil {
		return err
	}
	glyphs := make([]positionedGlyph, 0, len(units))
	cursor := 0
	for _, unit := range units {
		inkWidth, inkHeight, cellWidth, ok := gvmTextGeometry(style.Mode, unit.encodedBytes)
		if !ok {
			return fmt.Errorf("%w: mode %d cannot render a %d-byte EUC-KR unit", ErrInvalidDisplayConfig, style.Mode, unit.encodedBytes)
		}
		var alpha []byte
		if unit.invalidPair {
			alpha = rasterGVMInvalidPair(style.Mode, inkWidth, inkHeight)
		} else {
			glyph, glyphErr := d.text.Glyph(d.textOwner, fontID, unit.character)
			if glyphErr != nil {
				return glyphErr
			}
			alpha = scaleGVMGlyph(glyph, inkWidth, inkHeight)
		}
		glyphs = append(glyphs, positionedGlyph{
			alpha: alpha,
			width: inkWidth, height: inkHeight, x: cursor,
		})
		cursor += cellWidth
	}
	colorIndex, err := d.mapColor(d.mapping, d.phase, int16(style.Primary))
	if err != nil {
		return err
	}
	cellWidths := [...]int{4, 6, 6, 12}
	nativeWidth := len(terminated) * cellWidths[style.Mode]
	originX := int(x) + d.originX
	if style.Alignment == 1 {
		originX -= nativeWidth / 2
	} else if style.Alignment == 2 {
		originX -= nativeWidth
	}
	originY := int(y) + d.originY
	for _, positioned := range glyphs {
		for row := 0; row < positioned.height; row++ {
			for column := 0; column < positioned.width; column++ {
				if positioned.alpha[row*positioned.width+column] == 0 {
					continue
				}
				destinationX := originX + positioned.x + column
				destinationY := originY + row
				if destinationX < 0 || destinationX >= d.drawing.width || destinationY < 0 || destinationY >= d.drawing.height {
					continue
				}
				d.drawing.pixels[destinationY*d.drawing.width+destinationX] = colorIndex
			}
		}
	}
	return nil
}

type gvmTextUnit struct {
	character    rune
	encodedBytes int
	invalidPair  bool
}

func gvmTextUnits(text *shared.Text, encoded []byte, mode uint8) ([]gvmTextUnit, error) {
	units := make([]gvmTextUnit, 0, len(encoded))
	for byteIndex := 0; byteIndex < len(encoded); {
		if encoded[byteIndex]&0x80 == 0 {
			units = append(units, gvmTextUnit{character: rune(encoded[byteIndex]), encodedBytes: 1})
			byteIndex++
			continue
		}
		if mode < 2 {
			units = append(units, gvmTextUnit{character: 0, encodedBytes: 1})
			byteIndex++
			continue
		}
		second := byte(0)
		if byteIndex+1 < len(encoded) {
			second = encoded[byteIndex+1]
		}
		pair := uint16(encoded[byteIndex])<<8 | uint16(second)
		character := rune(0)
		invalidPair := true
		if (pair >= 0xb0a1 && pair <= 0xc9fe) || (pair >= 0xa1a1 && pair <= 0xacfe) {
			decoded, err := text.Decode([]byte{encoded[byteIndex], second}, shared.EncodingEUCKR)
			if err != nil {
				return nil, err
			}
			runes := []rune(decoded)
			if len(runes) == 1 {
				character = runes[0]
				invalidPair = false
			}
		}
		units = append(units, gvmTextUnit{character: character, encodedBytes: 2, invalidPair: invalidPair})
		byteIndex += 2
	}
	return units, nil
}

func gvmTextGeometry(mode uint8, encodedBytes int) (inkWidth, inkHeight, cellWidth int, ok bool) {
	if encodedBytes == 1 {
		widths := [...]int{3, 5, 5, 10}
		heights := [...]int{5, 7, 11, 22}
		cells := [...]int{4, 6, 6, 12}
		return widths[mode], heights[mode], cells[mode], true
	}
	if encodedBytes == 2 {
		widths := [...]int{8, 12, 11, 22}
		heights := [...]int{6, 8, 11, 22}
		cells := [...]int{8, 12, 12, 24}
		return widths[mode], heights[mode], cells[mode], true
	}
	return 0, 0, 0, false
}

func scaleGVMGlyph(glyph shared.Glyph, width, height int) []byte {
	result := make([]byte, width*height)
	if glyph.Width <= 0 || glyph.Height <= 0 || len(glyph.Alpha) != int(glyph.Width*glyph.Height) {
		return result
	}
	for y := 0; y < height; y++ {
		sourceY := y * int(glyph.Height) / height
		for x := 0; x < width; x++ {
			sourceX := x * int(glyph.Width) / width
			result[y*width+x] = glyph.Alpha[sourceY*int(glyph.Width)+sourceX]
		}
	}
	return result
}

var gvmInvalidPairBitmap = [...]byte{
	0xfc, 0x80, 0x90, 0x12, 0x02, 0x40, 0x48, 0x11,
	0xc2, 0x20, 0x84, 0x20, 0x98, 0x10, 0x02, 0x00,
}

func rasterGVMInvalidPair(mode uint8, width, height int) []byte {
	result := make([]byte, width*height)
	if mode != 2 && mode != 3 {
		return result
	}
	scale := 1
	if mode == 3 {
		scale = 2
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sourceX, sourceY := x/scale, y/scale
			bit := sourceY*11 + sourceX
			if gvmInvalidPairBitmap[bit/8]&(0x80>>uint(bit%8)) != 0 {
				result[y*width+x] = 0xff
			}
		}
	}
	return result
}

func (d *DisplayAdapter) mapSprite(sprite indexedSprite) ([]byte, error) {
	mapped := make([]byte, 256)
	mappedSet := make([]bool, 256)
	for _, index := range sprite.pixels {
		if int(index) == sprite.transparentIndex || mappedSet[index] {
			continue
		}
		selector := index
		if sprite.palette != nil {
			selector = sprite.palette[index]
			if selector > lastSKTPaletteSelector {
				// The native palette path indexes the 182-byte remap row
				// without a bound; past it the reference emulator reads its
				// idle polygon vertex buffer, which holds zero.
				mappedSet[index] = true
				continue
			}
		}
		value, err := d.mapColor(d.mapping, d.phase, int16(selector))
		if err != nil {
			return nil, err
		}
		mapped[index] = value
		mappedSet[index] = true
	}
	return mapped, nil
}

func (d *DisplayAdapter) PresentGVMDisplay() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var frame image.Image
	if d.orientation == DisplayOrientationAlternate {
		frame = freezeRGBA(renderAlternateOrientation(d.drawing))
	} else {
		frame = d.renderDefault()
	}
	return d.publisher.PublishGVMFrame(frame)
}

func (d *DisplayAdapter) renderDefault() image.Image {
	pixels := make([]color.RGBA, len(d.drawing.pixels))
	for i, index := range d.drawing.pixels {
		pixels[i] = d.palette.Color(index)
	}
	return immutableRGBA{bounds: image.Rect(0, 0, d.drawing.width, d.drawing.height), pixels: pixels}
}

func (d *DisplayAdapter) surface(which gvm.DisplayBuffer) (*indexedSurface, bool) {
	switch which {
	case gvm.DisplayBufferDrawing:
		return &d.drawing, true
	case gvm.DisplayBufferAuxiliary:
		return &d.auxiliary, true
	default:
		return nil, false
	}
}

func rasterizeDecodedSprite(surface *indexedSurface, sprite indexedSprite, mapped []byte, x, y int) {
	originX := int(int16(x - sprite.anchorX))
	originY := int(int16(y - sprite.anchorY))
	for sourceY := 0; sourceY < sprite.height; sourceY++ {
		for sourceX := 0; sourceX < sprite.width; sourceX++ {
			destinationX, destinationY := originX+sourceX, originY+sourceY
			if destinationX < 0 || destinationX >= surface.width || destinationY < 0 || destinationY >= surface.height {
				continue
			}
			index := sprite.pixels[sourceY*sprite.width+sourceX]
			if int(index) != sprite.transparentIndex {
				surface.pixels[destinationY*surface.width+destinationX] = mapped[index]
			}
		}
	}
}

func rasterizeMirroredSprite(surface *indexedSurface, sprite indexedSprite, mapped []byte, x, y int) {
	// Native 0x4107b0 publishes x+anchorX-width and its mirrored rasterizers
	// start at origin+width-1, then decrement the destination for each source
	// pixel. Preserve that one-past-anchor convention exactly.
	originX := int(int16(x + sprite.anchorX - sprite.width))
	originY := int(int16(y - sprite.anchorY))
	for sourceY := 0; sourceY < sprite.height; sourceY++ {
		for sourceX := 0; sourceX < sprite.width; sourceX++ {
			destinationX := originX + sprite.width - 1 - sourceX
			destinationY := originY + sourceY
			if destinationX < 0 || destinationX >= surface.width || destinationY < 0 || destinationY >= surface.height {
				continue
			}
			index := sprite.pixels[sourceY*sprite.width+sourceX]
			if int(index) != sprite.transparentIndex {
				surface.pixels[destinationY*surface.width+destinationX] = mapped[index]
			}
		}
	}
}

type immutableRGBA struct {
	bounds image.Rectangle
	pixels []color.RGBA
}

func (i immutableRGBA) ColorModel() color.Model { return color.RGBAModel }
func (i immutableRGBA) Bounds() image.Rectangle { return i.bounds }
func (i immutableRGBA) At(x, y int) color.Color {
	if !image.Pt(x, y).In(i.bounds) {
		return color.RGBA{}
	}
	return i.pixels[(y-i.bounds.Min.Y)*i.bounds.Dx()+x-i.bounds.Min.X]
}

func freezeRGBA(source *image.RGBA) image.Image {
	pixels := make([]color.RGBA, source.Bounds().Dx()*source.Bounds().Dy())
	for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
		for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
			pixels[(y-source.Bounds().Min.Y)*source.Bounds().Dx()+x-source.Bounds().Min.X] = source.RGBAAt(x, y)
		}
	}
	return immutableRGBA{bounds: source.Bounds(), pixels: pixels}
}

func fillBytes(values []byte, value byte) {
	for i := range values {
		values[i] = value
	}
}

func ordered(a, b int) (int, int) {
	if a > b {
		return b, a
	}
	return a, b
}

var (
	_ gvm.DisplayClearSink           = (*DisplayAdapter)(nil)
	_ gvm.DisplayFillSink            = (*DisplayAdapter)(nil)
	_ gvm.DisplayCopySink            = (*DisplayAdapter)(nil)
	_ gvm.MappingSelectSink          = (*DisplayAdapter)(nil)
	_ gvm.ColorSelectSink            = (*DisplayAdapter)(nil)
	_ gvm.RectangleDrawSink          = (*DisplayAdapter)(nil)
	_ gvm.RectangleFillSink          = (*DisplayAdapter)(nil)
	_ gvm.SpriteDrawSink             = (*DisplayAdapter)(nil)
	_ gvm.SpriteTransformSink        = (*DisplayAdapter)(nil)
	_ gvm.SpriteTransformPaletteSink = (*DisplayAdapter)(nil)
	_ gvm.TextDrawSink               = (*DisplayAdapter)(nil)
	_ gvm.DisplayPresentSink         = (*DisplayAdapter)(nil)
)
