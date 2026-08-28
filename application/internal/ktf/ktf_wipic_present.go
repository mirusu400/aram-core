package ktf

import (
	"context"
	"errors"
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

// KTF hands WIPI-C text to the same shared fallback font the Java surface
// draws with, so a Clet that measures a run and then paints it observes one
// set of advances. Glyphs are blitted straight into the guest framebuffer
// because Clets read those pixels back through the framebuffer object.
func ktfWIPICGraphicsDrawString(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	return runtime.drawWIPICString(false)
}

func ktfWIPICGraphicsDrawUnicodeString(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	return runtime.drawWIPICString(true)
}

func (r *Runtime) drawWIPICString(unicode bool) (uint32, error) {
	values := make([]uint32, 6)
	for index := range values {
		value, err := r.parameter(uint32(index))
		if err != nil {
			return 0, fmt.Errorf(
				"read KTF WIPI-C string parameter %d: %w",
				index,
				err,
			)
		}
		values[index] = value
	}
	if r.wipicFramebuffers[values[0]] == nil {
		return 0, nil
	}
	state, err := r.wipicGraphicsContext(values[5])
	if err != nil {
		return 0, err
	}
	text, err := r.wipicText(values[3], int32(values[4]), unicode)
	if err != nil || text == "" {
		return 0, err
	}
	fontID, err := r.ensureWIPICFontService(state.font)
	if err != nil {
		return 0, err
	}
	metrics, err := r.Services.Text.Metrics(r.ServiceOwner, fontID)
	if err != nil {
		return 0, err
	}
	if err := r.drawWIPICGlyphs(
		values[0],
		int(int32(values[1]))+state.offsetX,
		int(int32(values[2]))+state.offsetY-int(metrics.Ascent),
		text,
		fontID,
		state,
	); err != nil {
		return 0, err
	}
	return 0, r.commitKTFWIPICFramebuffer(values[0])
}

// drawWIPICGlyphs places the run relative to the already baseline-adjusted y.
// KTF Clets hand MC_grpDrawString a baseline coordinate: ??뺤삋?ⓦ끇以??clips its
// menu labels to exactly [y-ascent, y+descent), so a top-left origin leaves
// only the first glyph rows inside the Clet's own clip rectangle.
func (r *Runtime) drawWIPICGlyphs(
	handle uint32,
	x, y int,
	text string,
	fontID shared.ServiceID,
	state ktfWIPICGraphicsContext,
) error {
	cursor := x
	for _, character := range text {
		glyph, err := r.Services.Text.Glyph(r.ServiceOwner, fontID, character)
		if err != nil {
			return fmt.Errorf(
				"rasterize KTF WIPI-C glyph %q: %w",
				character,
				err,
			)
		}
		for row := int32(0); row < glyph.Height; row++ {
			for column := int32(0); column < glyph.Width; column++ {
				alpha := glyph.Alpha[row*glyph.Width+column]
				if alpha == 0 {
					continue
				}
				if err := r.writeWIPICPixelAlpha(
					handle,
					cursor+int(glyph.BearingX+column),
					y+int(glyph.BearingY+row),
					state,
					alpha,
				); err != nil {
					return err
				}
			}
		}
		cursor += int(glyph.Advance)
	}
	return nil
}

func ktfWIPICGraphicsGetStringWidth(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	return runtime.measureWIPICString(false)
}

func ktfWIPICGraphicsGetUnicodeStringWidth(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	return runtime.measureWIPICString(true)
}

func (r *Runtime) measureWIPICString(unicode bool) (uint32, error) {
	font, err := r.parameter(0)
	if err != nil {
		return 0, err
	}
	address, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	length, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	text, err := r.wipicText(address, int32(length), unicode)
	if err != nil || text == "" {
		return 0, err
	}
	fontID, err := r.ensureWIPICFontService(font)
	if err != nil {
		return 0, err
	}
	width, err := r.Services.Text.Measure(r.ServiceOwner, fontID, text)
	if err != nil {
		return 0, err
	}
	return uint32(max(int32(0), width)), nil
}

func ktfWIPICGraphicsGetFont(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	face, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	size, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	style, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	return face&0xe0 | style<<8 | size&0x1f, nil
}

func ktfWIPICGraphicsGetFontHeight(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	metrics, err := runtime.wipicFontMetrics()
	if err != nil {
		return 0, err
	}
	return uint32(metrics.Height), nil
}

func ktfWIPICGraphicsGetFontAscent(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	metrics, err := runtime.wipicFontMetrics()
	if err != nil {
		return 0, err
	}
	return uint32(metrics.Ascent), nil
}

func ktfWIPICGraphicsGetFontDescent(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	metrics, err := runtime.wipicFontMetrics()
	if err != nil {
		return 0, err
	}
	return uint32(metrics.Descent), nil
}

func (r *Runtime) wipicFontMetrics() (shared.FontMetrics, error) {
	font, err := r.parameter(0)
	if err != nil {
		return shared.FontMetrics{}, err
	}
	fontID, err := r.ensureWIPICFontService(font)
	if err != nil {
		return shared.FontMetrics{}, err
	}
	return r.Services.Text.Metrics(r.ServiceOwner, fontID)
}

// WIPI-C font handles are plain integers rather than guest objects, so their
// shared text services are cached under a reserved key range that no guest
// allocation can produce.
const ktfWIPICFontServiceKey = uint32(0xffff0000)

func (r *Runtime) ensureWIPICFontService(
	font uint32,
) (shared.ServiceID, error) {
	height := int32(guest.FontHeight(font))
	var style shared.FontStyle
	if font&0x0100 != 0 {
		style |= shared.FontBold
	}
	if font&0x0200 != 0 {
		style |= shared.FontItalic
	}
	if font&0x0400 != 0 {
		style |= shared.FontUnderlined
	}
	key := ktfWIPICFontServiceKey | uint32(height)<<8 | uint32(style)
	if serviceID := r.FontServices[key]; serviceID != 0 {
		return serviceID, nil
	}
	serviceID, err := r.Services.Text.CreateFont(
		r.ServiceOwner,
		shared.FontDescriptor{
			Family: "aram-fallback",
			Size:   height,
			Style:  style,
		},
	)
	if err != nil {
		return 0, err
	}
	r.FontServices[key] = serviceID
	return serviceID, nil
}

// KTF passes M_Char runs in the handset's EUC-KR encoding and M_UCode runs as
// UTF-16LE. A negative length means the run is terminated instead of counted.
const ktfWIPICStringLimit = uint32(4096)

func (r *Runtime) wipicText(
	address uint32,
	length int32,
	unicode bool,
) (string, error) {
	if address == 0 {
		return "", nil
	}
	unit, encoding := uint32(1), shared.EncodingEUCKR
	if unicode {
		unit, encoding = 2, shared.EncodingUTF16LE
	}
	limit := ktfWIPICStringLimit / unit
	count := uint32(0)
	if length >= 0 {
		count = uint32(length)
		if count > limit {
			return "", fmt.Errorf(
				"KTF WIPI-C string at 0x%08x spans %d units",
				address,
				count,
			)
		}
	} else {
		// Truncating an unterminated run would hand the decoder a partial
		// multi-byte sequence, so report the bad pointer instead.
		var element [2]byte
		for ; count < limit; count++ {
			if err := r.CPU.ReadMemory(
				address+count*unit,
				element[:unit],
			); err != nil {
				return "", fmt.Errorf(
					"read KTF WIPI-C string at 0x%08x: %w",
					address+count*unit,
					err,
				)
			}
			if element[0] == 0 && (unit == 1 || element[1] == 0) {
				break
			}
		}
		if count == limit {
			return "", fmt.Errorf(
				"KTF WIPI-C string at 0x%08x is not terminated within %d units",
				address,
				limit,
			)
		}
	}
	if count == 0 {
		return "", nil
	}
	data := make([]byte, count*unit)
	if err := r.CPU.ReadMemory(address, data); err != nil {
		return "", fmt.Errorf(
			"read KTF WIPI-C string at 0x%08x: %w",
			address,
			err,
		)
	}
	return r.Services.Text.Decode(data, encoding)
}

func ktfWIPICGraphicsGetPixelFromRGB(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	red, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	green, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	blue, err := runtime.parameter(2)
	if err != nil {
		return 0, err
	}
	return (red&0xff)>>3<<11 |
		(green&0xff)>>2<<5 |
		(blue&0xff)>>3, nil
}

func ktfWIPICGraphicsGetRGBFromPixel(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	pixel, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	red := pixel >> 11 & 0x1f
	green := pixel >> 5 & 0x3f
	blue := pixel & 0x1f
	values := [...]uint32{
		red<<3 | red>>2,
		green<<2 | green>>4,
		blue<<3 | blue>>2,
	}
	for index, value := range values {
		output, parameterErr := runtime.parameter(uint32(index + 1))
		if parameterErr != nil {
			return 0, parameterErr
		}
		if output != 0 {
			if err := runtime.WriteU32(output, value); err != nil {
				return 0, err
			}
		}
	}
	return pixel, nil
}

func ktfWIPICGraphicsGetDisplayInfo(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	display, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	output, err := runtime.parameter(1)
	if err != nil || output == 0 || display > 1 {
		return 0, err
	}
	if runtime.frame == nil {
		runtime.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	}
	width, height := runtime.frame.Bounds().Dx(), runtime.frame.Bounds().Dy()
	values := [...]uint32{
		16, 16, uint32(width), uint32(height), uint32(width * 2),
		1, 0xf800, 0x001f, 0x07e0,
	}
	if err := runtime.writeWords(output, values[:]); err != nil {
		return 0, err
	}
	return 1, nil
}

func ktfWIPICGraphicsFlushLCD(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	framebuffer, err := runtime.parameter(1)
	if err != nil {
		return 0, err
	}
	if framebuffer == 0 {
		framebuffer = runtime.WipicScreenFramebuffer
	}
	return 0, runtime.presentWIPICFramebuffer(framebuffer)
}

func ktfWIPICGraphicsRepaint(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	if _, err := runtime.parameter(0); err != nil {
		return 0, err
	}
	// MC_grpRepaint requests a paint event, not just an LCD flush: the
	// handset re-invokes the clet's paint handler afterwards. Titles that
	// drive their game loop from paintClet stall on the first frame without
	// this (issue #47).
	if card := runtime.DisplayCards[runtime.DefaultDisplay]; card != 0 {
		runtime.dirtyCards[card] = true
		if runtime.DeferThreads && runtime.activeTask != nil {
			runtime.deferCardPaint(runtime.activeTask, card, false)
		}
	}
	return 0, runtime.presentWIPICFramebuffer(runtime.WipicScreenFramebuffer)
}

func (r *Runtime) presentWIPICFramebuffer(handle uint32) error {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil || r.frame == nil {
		return nil
	}
	if err := r.syncKTFWIPICFramebuffer(handle); err != nil {
		return err
	}
	surface := r.wipicSurfaceServices[handle]
	if surface == 0 {
		return fmt.Errorf("KTF WIPI-C framebuffer 0x%08x has no shared surface", handle)
	}
	presentationSurface := surface
	if !framebuffer.screen {
		screenHandle, err := r.EnsureWIPICScreenFramebuffer()
		if err != nil {
			return err
		}
		screen := r.wipicFramebuffers[screenHandle]
		presentationSurface = r.wipicSurfaceServices[screenHandle]
		if screen == nil || presentationSurface == 0 {
			return errors.New("KTF WIPI-C screen framebuffer is unavailable")
		}
		width := min(framebuffer.width, screen.width)
		height := min(framebuffer.height, screen.height)
		if width > 0 && height > 0 {
			if err := r.Services.Graphics.Blit(
				r.ServiceOwner,
				presentationSurface,
				surface,
				0,
				0,
				shared.Rectangle{
					Width:  int32(width),
					Height: int32(height),
				},
			); err != nil {
				return fmt.Errorf(
					"flush KTF WIPI-C framebuffer 0x%08x to screen: %w",
					handle,
					err,
				)
			}
		}
	}
	if r.Services.Graphics.Screen() != presentationSurface {
		if err := r.Services.Graphics.SetScreen(
			r.ServiceOwner,
			presentationSurface,
		); err != nil {
			return err
		}
	}
	frame, err := r.Services.Graphics.Present(
		r.ServiceOwner,
		presentationSurface,
		shared.Rectangle{},
	)
	if err != nil {
		return err
	}
	bounds := r.frame.Bounds()
	width := min(int(frame.Width), bounds.Dx())
	height := min(int(frame.Height), bounds.Dy())
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*int(frame.Width) + x) * 4
			r.frame.SetRGBA(
				bounds.Min.X+x,
				bounds.Min.Y+y,
				color.RGBA{
					R: frame.RGBA[offset+0],
					G: frame.RGBA[offset+1],
					B: frame.RGBA[offset+2],
					A: frame.RGBA[offset+3],
				},
			)
		}
	}
	r.WipicScreenPending = false
	if state := r.Graphics[r.ScreenGraphics]; state != nil {
		state.PixelsDirty = true
	}
	r.PresentCount++
	return nil
}

// commitKTFWIPICFramebuffer marks a guest RGB565 framebuffer as ready to be
// published to the shared graphics service. The actual CPU-memory read and
// ReplacePixels conversion is deferred to the point where the surface is
// consumed (presentWIPICFramebuffer / applyPendingWIPICScreen), both of which
// call syncKTFWIPICFramebuffer unconditionally before reading. Syncing eagerly
// here made every draw primitive (DrawImage, FillRect, ...) read and convert
// the whole framebuffer, so a frame that composited many sprites paid an
// O(draw-calls * framebuffer) cost and stuttered on graphics-heavy titles
// (issue #54). Screen writes remain pending until the Java Card paint boundary:
// KTF Clets commonly render from calcClet through WIPI-C and use an otherwise
// empty Java paintClet only to submit that native framebuffer.
func (r *Runtime) commitKTFWIPICFramebuffer(handle uint32) error {
	if framebuffer := r.wipicFramebuffers[handle]; framebuffer != nil && framebuffer.screen {
		r.WipicScreenPending = true
	}
	return nil
}

// applyPendingWIPICScreen makes the native screen the base of the next Java
// paint. Java drawing then lands on the same canonical RGBA frame instead of
// a separate stale surface, preserving the provider's physical-screen model.
func (r *Runtime) applyPendingWIPICScreen() error {
	if !r.WipicScreenPending {
		return nil
	}
	handle := r.WipicScreenFramebuffer
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil || !framebuffer.screen || r.frame == nil {
		return errors.New("pending KTF WIPI-C screen framebuffer is unavailable")
	}
	if err := r.syncKTFWIPICFramebuffer(handle); err != nil {
		return err
	}
	surface := r.wipicSurfaceServices[handle]
	if surface == 0 {
		return fmt.Errorf(
			"KTF WIPI-C framebuffer 0x%08x has no shared surface",
			handle,
		)
	}
	descriptor, err := r.Services.Graphics.Descriptor(r.ServiceOwner, surface)
	if err != nil {
		return err
	}
	bounds := r.frame.Bounds()
	if descriptor.Width != int32(framebuffer.width) ||
		descriptor.Height != int32(framebuffer.height) ||
		descriptor.Format != shared.PixelRGB565 ||
		framebuffer.width != bounds.Dx() || framebuffer.height != bounds.Dy() {
		return fmt.Errorf(
			"KTF WIPI-C screen 0x%08x geometry differs from the Java frame",
			handle,
		)
	}
	pixels, err := r.Services.Graphics.RGBA(r.ServiceOwner, surface)
	if err != nil {
		return err
	}
	rowBytes := framebuffer.width * 4
	if len(pixels) != rowBytes*framebuffer.height {
		return fmt.Errorf(
			"KTF WIPI-C screen 0x%08x RGBA payload has %d bytes, want %d",
			handle,
			len(pixels),
			rowBytes*framebuffer.height,
		)
	}
	for y := 0; y < framebuffer.height; y++ {
		destination := r.frame.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		copy(
			r.frame.Pix[destination:destination+rowBytes],
			pixels[y*rowBytes:(y+1)*rowBytes],
		)
	}
	r.WipicScreenPending = false
	if state := r.Graphics[r.ScreenGraphics]; state != nil {
		state.PixelsDirty = true
	}
	r.tracef("wipic_screen_merge:framebuffer=0x%08x", handle)
	return nil
}

func (r *Runtime) syncKTFWIPICFramebuffer(handle uint32) error {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil {
		return nil
	}
	serviceID, err := r.ensureWIPICSurface(handle)
	if err != nil {
		return err
	}
	if serviceID == 0 {
		return nil
	}
	data := make([]byte, framebuffer.stride*framebuffer.height)
	if err := r.CPU.ReadMemory(framebuffer.pixels, data); err != nil {
		return err
	}
	if err := r.Services.Graphics.ReplacePixels(
		r.ServiceOwner,
		serviceID,
		data,
	); err != nil {
		return fmt.Errorf(
			"sync KTF WIPI-C framebuffer 0x%08x: %w",
			handle,
			err,
		)
	}
	return nil
}

func ModeStatus(procedure uint32) uint32 {
	if procedure&1 != 0 {
		return cpu.StatusThumb
	}
	return 0
}
