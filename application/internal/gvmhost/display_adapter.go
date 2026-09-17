package gvmhost

import (
	"errors"
	"image"
	"image/color"
	"sync"

	"github.com/mirusu400/aram-core/gvm"
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

func (d *DisplayAdapter) DrawGVMSprite(resource []byte, x, y int16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	sprite, err := decodeIndexedSprite(resource)
	if err != nil {
		return err
	}
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
		mapped[index], err = d.palette.Map(d.mapping, int16(selector))
		if err != nil {
			return err
		}
		mappedSet[index] = true
	}
	rasterizeDecodedSprite(&d.drawing, sprite, mapped, int(x), int(y))
	return nil
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
	originX, originY := x-sprite.anchorX, y-sprite.anchorY
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
	_ gvm.DisplayClearSink   = (*DisplayAdapter)(nil)
	_ gvm.DisplayFillSink    = (*DisplayAdapter)(nil)
	_ gvm.DisplayCopySink    = (*DisplayAdapter)(nil)
	_ gvm.MappingSelectSink  = (*DisplayAdapter)(nil)
	_ gvm.ColorSelectSink    = (*DisplayAdapter)(nil)
	_ gvm.RectangleFillSink  = (*DisplayAdapter)(nil)
	_ gvm.SpriteDrawSink     = (*DisplayAdapter)(nil)
	_ gvm.DisplayPresentSink = (*DisplayAdapter)(nil)
)
