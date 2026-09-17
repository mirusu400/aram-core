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

// FramePublisher receives one immutable image for each successful guest
// presentation. Implementations must not call back into DisplayAdapter.
type FramePublisher interface {
	PublishGVMFrame(image.Image) error
}

type DisplayConfig struct {
	Width       int
	Height      int
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
	selector    uint8
	activeColor byte
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

func (d *DisplayAdapter) FillGVMDisplay(selector int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	mapped, err := d.palette.Map(d.mapping, selector)
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
	mapped, err := d.palette.Map(selector, int16(d.selector))
	if err != nil {
		return err
	}
	d.mapping, d.activeColor = selector, mapped
	return nil
}

func (d *DisplayAdapter) SelectGVMColor(selector uint8) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	mapped, err := d.palette.Map(d.mapping, int16(selector))
	if err != nil {
		return err
	}
	d.selector, d.activeColor = selector, mapped
	return nil
}

func (d *DisplayAdapter) FillGVMRectangle(x1, y1, x2, y2 int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.selector == 4 {
		return nil
	}
	left, right := ordered(int(x1), int(x2))
	top, bottom := ordered(int(y1), int(y2))
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
	left, right := ordered(int(x1), int(x2))
	top, bottom := ordered(int(y1), int(y2))
	d.drawHorizontal(left, right, top)
	d.drawHorizontal(left, right, bottom)
	d.drawVertical(top, bottom, left)
	d.drawVertical(top, bottom, right)
	return nil
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

func (d *DisplayAdapter) DrawGVMSprite(resource []byte, x, y int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
	mapped, err := d.mapSprite(sprite)
	if err != nil {
		return err
	}
	rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x), int(y))
	return nil
}

func (d *DisplayAdapter) DrawGVMTransformedSprite(resource []byte, x, y int16, mirrorHorizontal bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
	mapped, err := d.mapSprite(sprite)
	if err != nil {
		return err
	}
	if mirrorHorizontal {
		rasterizeMirroredSprite(&d.drawing, sprite, mapped, int(x), int(y))
	} else {
		rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x), int(y))
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
	text, err := d.text.Decode(terminated, shared.EncodingEUCKR)
	if err != nil {
		return err
	}
	fontID := d.textFonts[style.Mode]
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
	runes := []rune(text)
	units, err := gvmTextUnits(terminated, runes)
	if err != nil {
		return err
	}
	glyphs := make([]positionedGlyph, 0, len(units))
	cursor := 0
	for _, unit := range units {
		glyph, glyphErr := d.text.Glyph(d.textOwner, fontID, unit.character)
		if glyphErr != nil {
			return glyphErr
		}
		inkWidth, inkHeight, cellWidth, ok := gvmTextGeometry(style.Mode, unit.encodedBytes)
		if !ok {
			return fmt.Errorf("%w: mode %d cannot render a %d-byte EUC-KR unit", ErrInvalidDisplayConfig, style.Mode, unit.encodedBytes)
		}
		glyphs = append(glyphs, positionedGlyph{
			alpha: scaleGVMGlyph(glyph, inkWidth, inkHeight),
			width: inkWidth, height: inkHeight, x: cursor,
		})
		cursor += cellWidth
	}
	colorIndex, err := d.palette.Map(d.mapping, int16(style.Primary))
	if err != nil {
		return err
	}
	nativeWidth := cursor
	originX := int(x)
	if style.Alignment == 1 {
		originX -= nativeWidth / 2
	} else if style.Alignment == 2 {
		originX -= nativeWidth
	}
	originY := int(y)
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
}

func gvmTextUnits(encoded []byte, decoded []rune) ([]gvmTextUnit, error) {
	units := make([]gvmTextUnit, 0, len(decoded))
	runeIndex := 0
	for byteIndex := 0; byteIndex < len(encoded); {
		width := 1
		if encoded[byteIndex] >= 0x80 {
			width = 2
		}
		if byteIndex+width > len(encoded) || runeIndex >= len(decoded) {
			return nil, fmt.Errorf("%w: malformed EUC-KR unit", ErrInvalidDisplayConfig)
		}
		units = append(units, gvmTextUnit{character: decoded[runeIndex], encodedBytes: width})
		byteIndex += width
		runeIndex++
	}
	if runeIndex != len(decoded) {
		return nil, fmt.Errorf("%w: EUC-KR unit count mismatch", ErrInvalidDisplayConfig)
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
	if encodedBytes == 2 && mode >= 2 {
		scale := 1
		if mode == 3 {
			scale = 2
		}
		return 11 * scale, 11 * scale, 12 * scale, true
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
		}
		value, err := d.palette.Map(d.mapping, int16(selector))
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
	_ gvm.DisplayClearSink    = (*DisplayAdapter)(nil)
	_ gvm.DisplayFillSink     = (*DisplayAdapter)(nil)
	_ gvm.DisplayCopySink     = (*DisplayAdapter)(nil)
	_ gvm.MappingSelectSink   = (*DisplayAdapter)(nil)
	_ gvm.ColorSelectSink     = (*DisplayAdapter)(nil)
	_ gvm.RectangleDrawSink   = (*DisplayAdapter)(nil)
	_ gvm.RectangleFillSink   = (*DisplayAdapter)(nil)
	_ gvm.SpriteDrawSink      = (*DisplayAdapter)(nil)
	_ gvm.SpriteTransformSink = (*DisplayAdapter)(nil)
	_ gvm.TextDrawSink        = (*DisplayAdapter)(nil)
	_ gvm.DisplayPresentSink  = (*DisplayAdapter)(nil)
)
