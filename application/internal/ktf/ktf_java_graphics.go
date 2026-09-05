package ktf

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"unicode"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) EnsureScreenGraphics() (uint32, error) {
	if r.ScreenGraphics != 0 {
		return r.ScreenGraphics, nil
	}
	if r.frame == nil {
		r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	}
	classAddress, err := r.EnsureJavaClass("org/kwis/msp/lcdui/Graphics")
	if err != nil {
		return 0, err
	}
	class, err := r.InspectJavaClass(classAddress)
	if err != nil {
		return 0, err
	}
	instance, err := r.NewJavaInstanceForClass(class)
	if err != nil {
		return 0, err
	}
	r.ScreenGraphics = instance
	r.Graphics[instance] = &ktfGraphics{
		Target:      r.frame,
		clip:        r.frame.Bounds(),
		color:       color.RGBA{A: 0xff},
		PixelsDirty: true,
	}
	surface, err := r.Services.Graphics.CreateSurface(
		r.ServiceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(r.frame.Bounds().Dx()),
			Height: int32(r.frame.Bounds().Dy()),
			Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		return 0, err
	}
	if err := r.Services.Graphics.SetScreen(
		r.ServiceOwner,
		surface,
	); err != nil {
		_ = r.Services.Graphics.DestroySurface(r.ServiceOwner, surface)
		return 0, err
	}
	r.GraphicsServices[instance] = surface
	if err := r.syncKTFGraphics(instance); err != nil {
		return 0, err
	}
	return instance, nil
}

// noteKTFFullScreenFrame records that the title composes its frame at the full
// screen size. A title that shows an annunciator normally gives the top of the
// screen up and lays its card out below it, but a few build a backbuffer as
// tall as the whole handset screen and blit it at the card origin: 대박돈까스
// composes 176x240 and puts its softkey bar on the last rows, so a card an
// annunciator short dropped the bar off the bottom edge (issue #133). A frame
// at least as large as the screen is the title saying how tall its card is, so
// the card takes the whole screen from the next paint on. Only a Graphics that
// draws the screen counts, and the card never shrinks back.
func (r *Runtime) noteKTFFullScreenFrame(
	state *ktfGraphics,
	source image.Image,
	x, y int,
) {
	if r.cardOwnsScreen || state == nil || r.frame == nil ||
		state.Target != r.frame || x != 0 || y != 0 {
		return
	}
	bounds := source.Bounds()
	if bounds.Dx() < int(r.DisplayWidth()) ||
		bounds.Dy() < int(r.displayHeight()) {
		return
	}
	r.cardOwnsScreen = true
}

func (r *Runtime) ResetScreenGraphics(instance uint32) {
	state := r.Graphics[instance]
	if state == nil {
		return
	}
	bounds := state.Target.Bounds()
	origin := image.Pt(bounds.Min.X, bounds.Min.Y+int(r.CardOriginY()))
	card := image.Rect(
		origin.X,
		origin.Y,
		bounds.Max.X,
		origin.Y+int(r.ActiveCardHeight()),
	).Intersect(bounds)
	if state.origin != origin {
		// A title that paints before showing its annunciator has already put
		// pixels where the card no longer reaches. Nothing clips into that
		// strip again, so it would keep a frozen slice of an older frame.
		clearOutside(state.Target, card)
		state.origin = origin
		state.PixelsDirty = true
	}
	// The strip the card gives up is the handset's status bar, not a
	// letterbox: draw the phone chrome the title is laying out around. This
	// runs on every reset rather than only when the origin moves, so a state
	// saved before the bar existed still comes back with one.
	paintKTFAnnunciator(
		state.Target,
		image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, card.Min.Y),
	)
	state.surface = card
	state.clip = card
	state.translate = image.Point{}
	state.color = color.RGBA{A: 0xff}
}

// clearOutside blacks out every part of target that falls outside inside.
func clearOutside(target draw.Image, inside image.Rectangle) {
	bounds := target.Bounds()
	black := image.NewUniform(color.RGBA{A: 0xff})
	for _, region := range []image.Rectangle{
		image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, inside.Min.Y),
		image.Rect(bounds.Min.X, inside.Max.Y, bounds.Max.X, bounds.Max.Y),
		image.Rect(bounds.Min.X, inside.Min.Y, inside.Min.X, inside.Max.Y),
		image.Rect(inside.Max.X, inside.Min.Y, bounds.Max.X, inside.Max.Y),
	} {
		region = region.Intersect(bounds)
		if region.Empty() {
			continue
		}
		draw.Draw(target, region, black, image.Point{}, draw.Src)
	}
}

// ensureGraphicsSurface answers the service surface a host draw through this
// Graphics needs, making it if the Image behind it has no mirror yet or lost
// the one it had to the mirror budget. A Graphics that draws the screen keeps
// the screen surface, which is never a mirror and never evicted.
func (r *Runtime) ensureGraphicsSurface(
	instance uint32,
) (shared.ServiceID, error) {
	if surface := r.GraphicsServices[instance]; surface != 0 {
		state := r.Graphics[instance]
		if state != nil && state.image != 0 {
			r.touchJavaImageSurface(state.image)
		}
		return surface, nil
	}
	state := r.Graphics[instance]
	if state == nil || state.image == 0 {
		return 0, nil
	}
	surface, err := r.ensureJavaImageSurface(state.image)
	if err != nil {
		return 0, err
	}
	r.GraphicsServices[instance] = surface
	// The mirror was just uploaded from the Image, which is the same pixels
	// this Graphics draws into, so nothing is outstanding.
	state.PixelsDirty = false
	return surface, nil
}

func (r *Runtime) syncKTFGraphics(instance uint32) error {
	state := r.Graphics[instance]
	serviceID := r.GraphicsServices[instance]
	if state == nil || serviceID == 0 {
		return nil
	}
	bounds := state.Target.Bounds()
	descriptor, err := r.Services.Graphics.Descriptor(
		r.ServiceOwner,
		serviceID,
	)
	if err != nil {
		return err
	}
	if descriptor.Width != int32(bounds.Dx()) ||
		descriptor.Height != int32(bounds.Dy()) ||
		descriptor.Format != shared.PixelRGBA8888 {
		return fmt.Errorf(
			"KTF graphics 0x%08x service geometry differs",
			instance,
		)
	}
	if state.PixelsDirty {
		var replaceErr error
		if rgba, ok := state.Target.(*image.RGBA); ok {
			start := rgba.PixOffset(bounds.Min.X, bounds.Min.Y)
			replaceErr = r.Services.Graphics.ReplacePixelRows(
				r.ServiceOwner,
				serviceID,
				rgba.Pix[start:],
				rgba.Stride,
			)
		} else {
			replaceErr = r.Services.Graphics.ReplacePixels(
				r.ServiceOwner,
				serviceID,
				ktfRGBABytes(state.Target),
			)
		}
		if replaceErr != nil {
			return replaceErr
		}
		state.PixelsDirty = false
	}
	if state.offset().X < -(1<<31) || state.offset().X > 1<<31-1 ||
		state.offset().Y < -(1<<31) || state.offset().Y > 1<<31-1 {
		return fmt.Errorf("KTF graphics translation overflows service state")
	}
	return r.Services.Graphics.SetDrawState(
		r.ServiceOwner,
		serviceID,
		shared.SurfaceDrawState{
			Clip: shared.Rectangle{
				X:      int32(state.clip.Min.X),
				Y:      int32(state.clip.Min.Y),
				Width:  int32(state.clip.Dx()),
				Height: int32(state.clip.Dy()),
			},
			TranslateX:  int32(state.offset().X),
			TranslateY:  int32(state.offset().Y),
			Raster:      shared.RasterCopy,
			GlobalAlpha: state.color.A,
		},
	)
}

func (r *Runtime) handleGraphicsMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	state := r.Graphics[instance]
	switch name + descriptor {
	case "<init>(Lorg/kwis/msp/lcdui/Display;)V",
		"<init>(Ljavax/microedition/lcdui/Graphics;)V":
		if state == nil {
			if r.frame == nil {
				r.frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
			}
			// A Graphics built from the Display draws the same card the
			// screen Graphics does, so it has to sit below the annunciator
			// too; leaving it at the framebuffer origin let a title paint
			// over the strip the card no longer owns.
			r.Graphics[instance] = &ktfGraphics{Target: r.frame}
			r.ResetScreenGraphics(instance)
			screen, screenErr := r.EnsureScreenGraphics()
			if screenErr != nil {
				return 0, screenErr
			}
			r.GraphicsServices[instance] = r.GraphicsServices[screen]
		}
		return 0, nil
	case "getFont()Lorg/kwis/msp/lcdui/Font;":
		return r.KtfGraphicsFont(instance)
	case "setFont(Lorg/kwis/msp/lcdui/Font;)V":
		if state == nil {
			return 0, nil
		}
		font, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if font == 0 {
			font, valueErr = r.ensureDefaultFont()
			if valueErr != nil {
				return 0, valueErr
			}
		}
		if _, valueErr = r.ensureKTFFontService(font); valueErr != nil {
			return 0, valueErr
		}
		return 0, r.WriteJavaFieldWord(instance, 0, font)
	case "setColor(I)V":
		if state == nil {
			return 0, nil
		}
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.color.R = uint8(value >> 16)
		state.color.G = uint8(value >> 8)
		state.color.B = uint8(value)
		return 0, nil
	case "setColor(III)V":
		if state == nil {
			return 0, nil
		}
		red, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		green, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		blue, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		state.color.R = uint8(red)
		state.color.G = uint8(green)
		state.color.B = uint8(blue)
		return 0, nil
	case "setAlpha(I)V":
		if state != nil {
			alpha, valueErr := r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
			state.color.A = uint8(alpha)
		}
		return 0, nil
	case "fillRect(IIII)V", "fillRoundRect(IIIIII)V", "fillArc(IIIIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		if name == "fillRect" && r.menuForegroundCompat != nil {
			bounds := state.Target.Bounds()
			if rect.Min.X <= bounds.Min.X && rect.Min.Y <= bounds.Min.Y &&
				rect.Max.X >= bounds.Max.X && rect.Max.Y >= bounds.Max.Y-20 {
				r.menuForegroundCompat.pending = nil
			}
		}
		draw.Draw(state.Target, rect.Intersect(state.clip), image.NewUniform(state.color), image.Point{}, draw.Src)
		state.PixelsDirty = true
		return 0, nil
	case "setXORMode(Z)V":
		if state == nil {
			return 0, nil
		}
		mode, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.xorMode = mode != 0
		return 0, nil
	case "isXORMode()Z":
		if state == nil || !state.xorMode {
			return 0, nil
		}
		return 1, nil
	case "drawLine(IIII)V":
		if state == nil {
			return 0, nil
		}
		x1, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y1, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		x2, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		y2, valueErr := r.signedParameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		r.drawGraphicsLine(
			state,
			x1+state.offset().X,
			y1+state.offset().Y,
			x2+state.offset().X,
			y2+state.offset().Y,
		)
		state.PixelsDirty = true
		return 0, nil
	case "drawRect(IIII)V", "drawRoundRect(IIIIII)V", "drawArc(IIIIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.drawGraphicsRectangle(state, rect)
		state.PixelsDirty = true
		return 0, nil
	case "drawChar(CIII)V":
		character, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.drawGraphicsTextParameters(
			state,
			string(rune(character)),
			3,
		)
	case "drawChars([CIIIII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.drawGraphicsTextParameters(state, text, 5)
	case "drawString(Ljava/lang/String;III)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.drawGraphicsTextParameters(
			state,
			r.javaStringValue(value),
			3,
		)
	case "drawSubstring(Ljava/lang/String;IIIII)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.javaStringValue(value))
		if offset > uint32(len(runes)) ||
			count > uint32(len(runes))-offset {
			return 0, nil
		}
		return 0, r.drawGraphicsTextParameters(
			state,
			string(runes[offset:offset+count]),
			5,
		)
	case "drawImage(Lorg/kwis/msp/lcdui/Image;III)V":
		if state == nil {
			return 0, nil
		}
		imageAddress, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		source := r.images[imageAddress]
		if source == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		anchor, valueErr := r.parameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		r.noteKTFFullScreenFrame(state, source, x, y)
		r.drawKTFJavaImage(state, imageAddress, source, x, y, anchor)
		return 0, nil
	case "setClip(IIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.clip = rect.Intersect(state.drawable())
		return 0, nil
	case "clipRect(IIII)V":
		if state == nil {
			return 0, nil
		}
		rect, valueErr := r.graphicsRectangle(state, 2)
		if valueErr != nil {
			return 0, valueErr
		}
		state.clip = state.clip.Intersect(rect).Intersect(state.drawable())
		return 0, nil
	case "getColor()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.R)<<16 |
			uint32(state.color.G)<<8 |
			uint32(state.color.B), nil
	case "getAlpha()I":
		if state == nil {
			return 0xff, nil
		}
		return uint32(state.color.A), nil
	case "getRedComponent()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.R), nil
	case "getGreenComponent()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.G), nil
	case "getBlueComponent()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.color.B), nil
	case "getGrayScale()I":
		if state == nil {
			return 0, nil
		}
		return (uint32(state.color.R)*77 +
			uint32(state.color.G)*150 +
			uint32(state.color.B)*29) >> 8, nil
	case "getPixel(II)I":
		if state == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		point := image.Pt(x+state.offset().X, y+state.offset().Y)
		if !point.In(state.Target.Bounds()) {
			return 0, nil
		}
		red, green, blue, _ := state.Target.At(point.X, point.Y).RGBA()
		return uint32(red>>8)<<16 |
			uint32(green>>8)<<8 |
			uint32(blue>>8), nil
	case "getPixels(IIII[BII)V":
		return 0, r.copyGraphicsPixelsToByteArray(state)
	case "getClipX()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.clip.Min.X - state.offset().X)), nil
	case "getClipY()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.clip.Min.Y - state.offset().Y)), nil
	case "getClipWidth()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.clip.Dx()), nil
	case "getClipHeight()I":
		if state == nil {
			return 0, nil
		}
		return uint32(state.clip.Dy()), nil
	case "getTranslateX()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.translate.X)), nil
	case "getTranslateY()I":
		if state == nil {
			return 0, nil
		}
		return uint32(int32(state.translate.Y)), nil
	case "translate(II)V":
		if state == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		state.translate = state.translate.Add(image.Pt(x, y))
		return 0, nil
	case "setPixel(II)V":
		if state == nil {
			return 0, nil
		}
		x, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		point := image.Pt(x+state.offset().X, y+state.offset().Y)
		if point.In(state.clip) {
			state.plot(point.X, point.Y)
			state.PixelsDirty = true
		}
		return 0, nil
	case "setRGBPixels(IIII[III)V":
		return 0, r.setGraphicsRGBPixels(state)
	case "getRGBPixels(IIII[III)V":
		return 0, r.getGraphicsRGBPixels(state)
	case "setGrayScale(I)V":
		if state != nil {
			value, valueErr := r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
			gray := uint8(value)
			state.color.R, state.color.G, state.color.B = gray, gray, gray
		}
		return 0, nil
	case "encodeImage(IIII)[B":
		return r.encodeGraphicsImage(instance, state)
	default:
		return 0, nil
	}
}

func (r *Runtime) KtfGraphicsFont(instance uint32) (uint32, error) {
	font, err := r.readJavaFieldWord(instance, 0)
	if err != nil {
		return 0, err
	}
	if font == 0 {
		return r.ensureDefaultFont()
	}
	if _, err := r.ensureKTFFontService(font); err != nil {
		return 0, err
	}
	return font, nil
}

func (r *Runtime) drawGraphicsTextParameters(
	state *ktfGraphics,
	text string,
	firstPositionParameter uint32,
) error {
	if state == nil || text == "" {
		return nil
	}
	x, err := r.signedParameter(firstPositionParameter)
	if err != nil {
		return err
	}
	y, err := r.signedParameter(firstPositionParameter + 1)
	if err != nil {
		return err
	}
	anchor, err := r.parameter(firstPositionParameter + 2)
	if err != nil {
		return err
	}
	return r.drawGraphicsTextShared(state, text, x, y, anchor)
}

func (r *Runtime) drawGraphicsTextShared(
	state *ktfGraphics,
	text string,
	x, y int,
	anchor uint32,
) error {
	var graphicsInstance uint32
	for instance, candidate := range r.Graphics {
		if candidate == state {
			graphicsInstance = instance
			break
		}
	}
	serviceID, err := r.ensureGraphicsSurface(graphicsInstance)
	if err != nil {
		return err
	}
	if serviceID == 0 {
		return fmt.Errorf("KTF graphics text target has no shared surface")
	}
	if err := r.syncKTFGraphics(graphicsInstance); err != nil {
		return err
	}
	font, err := r.KtfGraphicsFont(graphicsInstance)
	if err != nil {
		return err
	}
	fontID, err := r.ensureKTFFontService(font)
	if err != nil {
		return err
	}
	textAnchor := shared.AnchorLeft | shared.AnchorTop
	switch {
	case anchor&8 != 0:
		textAnchor = textAnchor&^shared.AnchorLeft | shared.AnchorRight
	case anchor&1 != 0:
		textAnchor = textAnchor&^shared.AnchorLeft |
			shared.AnchorHorizontalCenter
	}
	switch {
	case anchor&32 != 0:
		textAnchor = textAnchor&^shared.AnchorTop | shared.AnchorBottom
	case anchor&2 != 0:
		textAnchor = textAnchor&^shared.AnchorTop |
			shared.AnchorVerticalCenter
	case anchor&64 != 0:
		textAnchor = textAnchor&^shared.AnchorTop | shared.AnchorBaseline
	}
	top, bottom, err := r.Services.Text.DrawBounds(
		r.ServiceOwner,
		fontID,
		serviceID,
		text,
		int32(x),
		int32(y),
		textAnchor,
		shared.Color{
			R: state.color.R,
			G: state.color.G,
			B: state.color.B,
			A: 0xff,
		},
	)
	if err != nil {
		return err
	}
	bounds := state.Target.Bounds()
	if top >= bottom {
		// Nothing was inked, so the target already matches the surface.
		state.PixelsDirty = false
		return nil
	}
	// Only the band the run inked is read back, into a buffer kept across
	// calls. A title that draws its whole screen with drawString - 현영맞고2006
	// issues hundreds a frame - otherwise converted, allocated and copied the
	// whole surface twice per string, which was most of its frame (issue #79).
	target, direct := state.Target.(*image.RGBA)
	direct = direct && target.Rect == bounds &&
		bounds.Min == (image.Point{}) &&
		target.Stride == bounds.Dx()*4
	if !direct {
		top, bottom = 0, bounds.Dy()
	}
	top = max(top, 0)
	bottom = min(bottom, bounds.Dy())
	if top >= bottom {
		state.PixelsDirty = false
		return nil
	}
	pixels, err := r.Services.Graphics.RGBARowsInto(
		r.ServiceOwner,
		serviceID,
		top,
		bottom,
		r.textSurfaceScratch,
	)
	if err != nil {
		return err
	}
	r.textSurfaceScratch = pixels
	if len(pixels) != bounds.Dx()*(bottom-top)*4 {
		return fmt.Errorf("KTF text surface geometry changed")
	}
	if direct {
		copy(target.Pix[top*target.Stride:bottom*target.Stride], pixels)
		state.PixelsDirty = false
		return nil
	}
	source := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bottom-top))
	copy(source.Pix, pixels)
	draw.Draw(
		state.Target,
		image.Rect(bounds.Min.X, bounds.Min.Y+top, bounds.Max.X, bounds.Min.Y+bottom),
		source,
		image.Point{},
		draw.Src,
	)
	state.PixelsDirty = false
	return nil
}

// drawGraphicsText is retained as a compatibility reference for the handset
// bitmap metrics; active drawing goes through the shared Text service above.
func (r *Runtime) drawGraphicsText(
	state *ktfGraphics,
	text string,
	x, y int,
	anchor uint32,
) {
	const (
		glyphAdvance = 6
		fontHeight   = 12
		glyphTop     = 2
	)
	runes := []rune(text)
	width := len(runes) * glyphAdvance
	switch {
	case anchor&8 != 0:
		x -= width
	case anchor&1 != 0:
		x -= width / 2
	}
	switch {
	case anchor&32 != 0:
		y -= fontHeight
	case anchor&2 != 0:
		y -= fontHeight / 2
	case anchor&64 != 0:
		y -= 10
	}
	x += state.offset().X
	y += state.offset().Y
	for _, character := range runes {
		rows := ktfBasicGlyph(character)
		for row, bits := range rows {
			for column := 0; column < 5; column++ {
				if bits&(1<<uint(4-column)) == 0 {
					continue
				}
				point := image.Pt(x+column, y+glyphTop+row)
				if point.In(state.clip) &&
					point.In(state.Target.Bounds()) {
					state.plot(point.X, point.Y)
				}
			}
		}
		x += glyphAdvance
	}
}

func ktfBasicGlyph(character rune) [7]uint8 {
	if character >= 'a' && character <= 'z' {
		character = unicode.ToUpper(character)
	}
	if glyph, ok := ktfBasicGlyphs[character]; ok {
		return glyph
	}
	if unicode.IsSpace(character) {
		return [7]uint8{}
	}
	// A deterministic outlined fallback keeps non-ASCII handset text visible
	// without depending on a host font or proprietary device font.
	middle := uint8((uint32(character) ^ uint32(character>>5)) & 0x0e)
	return [7]uint8{0x1f, 0x11, 0x11 | middle, 0x11, 0x11 | middle, 0x11, 0x1f}
}

var ktfBasicGlyphs = map[rune][7]uint8{
	' ':  {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	'!':  {0x04, 0x04, 0x04, 0x04, 0x04, 0x00, 0x04},
	'"':  {0x0a, 0x0a, 0x0a, 0x00, 0x00, 0x00, 0x00},
	'#':  {0x0a, 0x1f, 0x0a, 0x0a, 0x1f, 0x0a, 0x00},
	'%':  {0x19, 0x19, 0x02, 0x04, 0x08, 0x13, 0x13},
	'&':  {0x0c, 0x12, 0x14, 0x08, 0x15, 0x12, 0x0d},
	'\'': {0x04, 0x04, 0x08, 0x00, 0x00, 0x00, 0x00},
	'(':  {0x02, 0x04, 0x08, 0x08, 0x08, 0x04, 0x02},
	')':  {0x08, 0x04, 0x02, 0x02, 0x02, 0x04, 0x08},
	'*':  {0x00, 0x0a, 0x04, 0x1f, 0x04, 0x0a, 0x00},
	'+':  {0x00, 0x04, 0x04, 0x1f, 0x04, 0x04, 0x00},
	',':  {0x00, 0x00, 0x00, 0x00, 0x04, 0x04, 0x08},
	'-':  {0x00, 0x00, 0x00, 0x1f, 0x00, 0x00, 0x00},
	'.':  {0x00, 0x00, 0x00, 0x00, 0x00, 0x0c, 0x0c},
	'/':  {0x01, 0x02, 0x02, 0x04, 0x08, 0x08, 0x10},
	'0':  {0x0e, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0e},
	'1':  {0x04, 0x0c, 0x14, 0x04, 0x04, 0x04, 0x1f},
	'2':  {0x0e, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1f},
	'3':  {0x1e, 0x01, 0x01, 0x0e, 0x01, 0x01, 0x1e},
	'4':  {0x02, 0x06, 0x0a, 0x12, 0x1f, 0x02, 0x02},
	'5':  {0x1f, 0x10, 0x10, 0x1e, 0x01, 0x01, 0x1e},
	'6':  {0x0e, 0x10, 0x10, 0x1e, 0x11, 0x11, 0x0e},
	'7':  {0x1f, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	'8':  {0x0e, 0x11, 0x11, 0x0e, 0x11, 0x11, 0x0e},
	'9':  {0x0e, 0x11, 0x11, 0x0f, 0x01, 0x01, 0x0e},
	':':  {0x00, 0x0c, 0x0c, 0x00, 0x0c, 0x0c, 0x00},
	';':  {0x00, 0x0c, 0x0c, 0x00, 0x04, 0x04, 0x08},
	'<':  {0x02, 0x04, 0x08, 0x10, 0x08, 0x04, 0x02},
	'=':  {0x00, 0x00, 0x1f, 0x00, 0x1f, 0x00, 0x00},
	'>':  {0x08, 0x04, 0x02, 0x01, 0x02, 0x04, 0x08},
	'?':  {0x0e, 0x11, 0x01, 0x02, 0x04, 0x00, 0x04},
	'@':  {0x0e, 0x11, 0x17, 0x15, 0x17, 0x10, 0x0e},
	'A':  {0x0e, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'B':  {0x1e, 0x11, 0x11, 0x1e, 0x11, 0x11, 0x1e},
	'C':  {0x0f, 0x10, 0x10, 0x10, 0x10, 0x10, 0x0f},
	'D':  {0x1e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1e},
	'E':  {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x1f},
	'F':  {0x1f, 0x10, 0x10, 0x1e, 0x10, 0x10, 0x10},
	'G':  {0x0f, 0x10, 0x10, 0x13, 0x11, 0x11, 0x0f},
	'H':  {0x11, 0x11, 0x11, 0x1f, 0x11, 0x11, 0x11},
	'I':  {0x0e, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0e},
	'J':  {0x01, 0x01, 0x01, 0x01, 0x11, 0x11, 0x0e},
	'K':  {0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11},
	'L':  {0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1f},
	'M':  {0x11, 0x1b, 0x15, 0x15, 0x11, 0x11, 0x11},
	'N':  {0x11, 0x19, 0x15, 0x13, 0x11, 0x11, 0x11},
	'O':  {0x0e, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'P':  {0x1e, 0x11, 0x11, 0x1e, 0x10, 0x10, 0x10},
	'Q':  {0x0e, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0d},
	'R':  {0x1e, 0x11, 0x11, 0x1e, 0x14, 0x12, 0x11},
	'S':  {0x0f, 0x10, 0x10, 0x0e, 0x01, 0x01, 0x1e},
	'T':  {0x1f, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04},
	'U':  {0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0e},
	'V':  {0x11, 0x11, 0x11, 0x11, 0x11, 0x0a, 0x04},
	'W':  {0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0a},
	'X':  {0x11, 0x11, 0x0a, 0x04, 0x0a, 0x11, 0x11},
	'Y':  {0x11, 0x11, 0x0a, 0x04, 0x04, 0x04, 0x04},
	'Z':  {0x1f, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1f},
	'[':  {0x0e, 0x08, 0x08, 0x08, 0x08, 0x08, 0x0e},
	'\\': {0x10, 0x08, 0x08, 0x04, 0x02, 0x02, 0x01},
	']':  {0x0e, 0x02, 0x02, 0x02, 0x02, 0x02, 0x0e},
	'^':  {0x04, 0x0a, 0x11, 0x00, 0x00, 0x00, 0x00},
	'_':  {0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x1f},
}

func (r *Runtime) setGraphicsRGBPixels(state *ktfGraphics) error {
	if state == nil {
		return nil
	}
	x, err := r.signedParameter(2)
	if err != nil {
		return err
	}
	y, err := r.signedParameter(3)
	if err != nil {
		return err
	}
	width, err := r.signedParameter(4)
	if err != nil {
		return err
	}
	height, err := r.signedParameter(5)
	if err != nil {
		return err
	}
	array, err := r.parameter(6)
	if err != nil {
		return err
	}
	offset, err := r.signedParameter(7)
	if err != nil {
		return err
	}
	bytesPerLine, err := r.signedParameter(8)
	if err != nil {
		return err
	}
	if array == 0 {
		return r.raiseHostJavaException("java/lang/NullPointerException")
	}
	if width < 0 || height < 0 || bytesPerLine < 0 ||
		int64(bytesPerLine) < int64(width)*4 || bytesPerLine%4 != 0 {
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	if width == 0 || height == 0 {
		return nil
	}
	destination := image.Rect(
		x+state.offset().X,
		y+state.offset().Y,
		x+state.offset().X+width,
		y+state.offset().Y+height,
	)
	if !destination.In(state.Target.Bounds()) {
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	fields, err := r.readGraphicsRGBWord(array)
	if err != nil {
		return err
	}
	length, err := r.readGraphicsRGBWord(fields + 4)
	if err != nil {
		return err
	}
	stride := bytesPerLine / 4
	lastExclusive := int64(offset) + int64(height-1)*int64(stride) + int64(width)
	if offset < 0 || lastExclusive < 0 || lastExclusive > int64(length) {
		return r.raiseHostJavaException(
			"java/lang/ArrayIndexOutOfBoundsException",
		)
	}
	visible := destination.Intersect(state.clip).Intersect(state.Target.Bounds())
	if visible.Empty() {
		return nil
	}
	row := r.graphicsRGBBuffer(visible.Dx() * 4)
	for destinationY := visible.Min.Y; destinationY < visible.Max.Y; destinationY++ {
		sourceY := destinationY - destination.Min.Y
		sourceX := visible.Min.X - destination.Min.X
		sourceIndex := int64(offset) +
			int64(sourceY)*int64(stride) + int64(sourceX)
		sourceAddress := uint64(fields) + 8 + uint64(sourceIndex)*4
		if sourceAddress+uint64(len(row)) > uint64(^uint32(0))+1 {
			return errors.New("KTF Java RGB pixel source address overflows")
		}
		if err := r.CPU.ReadMemory(uint32(sourceAddress), row); err != nil {
			return err
		}
		for column := 0; column < visible.Dx(); column++ {
			value := binary.LittleEndian.Uint32(row[column*4:])
			setKTFGraphicsRGBPixel(
				state.Target,
				visible.Min.X+column,
				destinationY,
				value,
			)
		}
	}
	state.PixelsDirty = true
	return nil
}

// getGraphicsRGBPixels is the read half of setGraphicsRGBPixels. Titles
// composite by reading a region back, transforming it, and writing it again;
// leaving this unimplemented handed them an untouched array, which turned
// every such effect black (issue #44).
func (r *Runtime) getGraphicsRGBPixels(state *ktfGraphics) error {
	if state == nil {
		return nil
	}
	x, err := r.signedParameter(2)
	if err != nil {
		return err
	}
	y, err := r.signedParameter(3)
	if err != nil {
		return err
	}
	width, err := r.signedParameter(4)
	if err != nil {
		return err
	}
	height, err := r.signedParameter(5)
	if err != nil {
		return err
	}
	array, err := r.parameter(6)
	if err != nil {
		return err
	}
	offset, err := r.signedParameter(7)
	if err != nil {
		return err
	}
	bytesPerLine, err := r.signedParameter(8)
	if err != nil {
		return err
	}
	if array == 0 {
		return r.raiseHostJavaException("java/lang/NullPointerException")
	}
	if width < 0 || height < 0 || bytesPerLine < 0 ||
		int64(bytesPerLine) < int64(width)*4 || bytesPerLine%4 != 0 {
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	if width == 0 || height == 0 {
		return nil
	}
	source := image.Rect(
		x+state.offset().X,
		y+state.offset().Y,
		x+state.offset().X+width,
		y+state.offset().Y+height,
	)
	if !source.In(state.Target.Bounds()) {
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	fields, err := r.readGraphicsRGBWord(array)
	if err != nil {
		return err
	}
	length, err := r.readGraphicsRGBWord(fields + 4)
	if err != nil {
		return err
	}
	stride := bytesPerLine / 4
	lastExclusive := int64(offset) + int64(height-1)*int64(stride) + int64(width)
	if offset < 0 || lastExclusive < 0 || lastExclusive > int64(length) {
		return r.raiseHostJavaException(
			"java/lang/ArrayIndexOutOfBoundsException",
		)
	}
	row := r.graphicsRGBBuffer(width * 4)
	for sourceY := source.Min.Y; sourceY < source.Max.Y; sourceY++ {
		for column := 0; column < width; column++ {
			value := getKTFGraphicsRGBPixel(
				state.Target,
				source.Min.X+column,
				sourceY,
			)
			binary.LittleEndian.PutUint32(row[column*4:], value)
		}
		destinationIndex := int64(offset) +
			int64(sourceY-source.Min.Y)*int64(stride)
		destinationAddress := uint64(fields) + 8 +
			uint64(destinationIndex)*4
		if destinationAddress+uint64(len(row)) > uint64(^uint32(0))+1 {
			return errors.New(
				"KTF Java RGB pixel destination address overflows",
			)
		}
		if err := r.CPU.WriteMemory(uint32(destinationAddress), row); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) graphicsRGBBuffer(size int) []byte {
	if cap(r.graphicsRGBScratch) < size {
		r.graphicsRGBScratch = make([]byte, size)
	}
	r.graphicsRGBScratch = r.graphicsRGBScratch[:size]
	return r.graphicsRGBScratch
}

func (r *Runtime) readGraphicsRGBWord(address uint32) (uint32, error) {
	data := r.graphicsRGBBuffer(4)
	if err := r.CPU.ReadMemory(address, data); err != nil {
		return 0, fmt.Errorf("read KTF word at 0x%08x: %w", address, err)
	}
	return binary.LittleEndian.Uint32(data), nil
}

func setKTFGraphicsRGBPixel(target draw.Image, x, y int, value uint32) {
	red := uint8(value >> 16)
	green := uint8(value >> 8)
	blue := uint8(value)
	switch target := target.(type) {
	case *image.RGBA:
		offset := target.PixOffset(x, y)
		target.Pix[offset+0] = red
		target.Pix[offset+1] = green
		target.Pix[offset+2] = blue
		target.Pix[offset+3] = 0xff
	case *image.NRGBA:
		offset := target.PixOffset(x, y)
		target.Pix[offset+0] = red
		target.Pix[offset+1] = green
		target.Pix[offset+2] = blue
		target.Pix[offset+3] = 0xff
	default:
		target.Set(x, y, color.RGBA{R: red, G: green, B: blue, A: 0xff})
	}
}

func getKTFGraphicsRGBPixel(target draw.Image, x, y int) uint32 {
	switch target := target.(type) {
	case *image.RGBA:
		offset := target.PixOffset(x, y)
		return uint32(target.Pix[offset+0])<<16 |
			uint32(target.Pix[offset+1])<<8 |
			uint32(target.Pix[offset+2])
	case *image.NRGBA:
		offset := target.PixOffset(x, y)
		return uint32(target.Pix[offset+0])<<16 |
			uint32(target.Pix[offset+1])<<8 |
			uint32(target.Pix[offset+2])
	default:
		red, green, blue, _ := target.At(x, y).RGBA()
		return (red>>8)<<16 | (green>>8)<<8 | blue>>8
	}
}

func (r *Runtime) encodeGraphicsImage(
	instance uint32,
	state *ktfGraphics,
) (uint32, error) {
	if state == nil {
		return 0, r.raiseHostJavaException(
			"java/lang/IllegalArgumentException",
		)
	}
	x, err := r.signedParameter(2)
	if err != nil {
		return 0, err
	}
	y, err := r.signedParameter(3)
	if err != nil {
		return 0, err
	}
	width, err := r.signedParameter(4)
	if err != nil {
		return 0, err
	}
	height, err := r.signedParameter(5)
	if err != nil {
		return 0, err
	}
	if width <= 0 || height <= 0 {
		return 0, r.raiseHostJavaException(
			"java/lang/IllegalArgumentException",
		)
	}
	x += state.offset().X
	y += state.offset().Y
	region := image.Rect(x, y, x+width, y+height)
	if !region.In(state.Target.Bounds()) {
		return 0, r.raiseHostJavaException(
			"java/lang/IllegalArgumentException",
		)
	}
	surface, err := r.ensureGraphicsSurface(instance)
	if err != nil {
		return 0, err
	}
	if err := r.syncKTFGraphics(instance); err != nil {
		return 0, err
	}
	if surface == 0 {
		return 0, fmt.Errorf(
			"KTF graphics 0x%08x has no shared surface",
			instance,
		)
	}
	encoded, err := r.Services.Assets.EncodeSurface(
		r.ServiceOwner,
		surface,
		"image/bmp",
		shared.Rectangle{
			X:      int32(region.Min.X),
			Y:      int32(region.Min.Y),
			Width:  int32(region.Dx()),
			Height: int32(region.Dy()),
		},
	)
	if err != nil {
		return 0, err
	}
	return r.newJavaByteArray(encoded)
}

func (r *Runtime) copyGraphicsPixelsToByteArray(
	state *ktfGraphics,
) error {
	x, err := r.signedParameter(2)
	if err != nil {
		return err
	}
	y, err := r.signedParameter(3)
	if err != nil {
		return err
	}
	width, err := r.signedParameter(4)
	if err != nil {
		return err
	}
	height, err := r.signedParameter(5)
	if err != nil {
		return err
	}
	array, err := r.parameter(6)
	if err != nil {
		return err
	}
	offsetValue, err := r.parameter(7)
	if err != nil {
		return err
	}
	bytesPerLineValue, err := r.parameter(8)
	if err != nil {
		return err
	}
	offset := int64(int32(offsetValue))
	bytesPerLine := int64(int32(bytesPerLineValue))
	if width < 0 || height < 0 || offset < 0 ||
		bytesPerLine < int64(width) {
		return fmt.Errorf(
			"invalid KTF Graphics.getPixels rectangle %dx%d "+
				"offset=%d bytes-per-line=%d",
			width,
			height,
			offset,
			bytesPerLine,
		)
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return err
	}
	required := offset
	if height > 0 {
		required += int64(height-1)*bytesPerLine + int64(width)
	}
	if required > int64(length) {
		return fmt.Errorf(
			"KTF Graphics.getPixels destination requires %d bytes, has %d",
			required,
			length,
		)
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return err
	}
	row := make([]byte, width)
	for rowIndex := 0; rowIndex < height; rowIndex++ {
		clear(row)
		if state != nil {
			sourceY := y + rowIndex + state.offset().Y
			for column := 0; column < width; column++ {
				sourceX := x + column + state.offset().X
				point := image.Pt(sourceX, sourceY)
				if !point.In(state.Target.Bounds()) {
					continue
				}
				red, green, blue, _ := state.Target.At(
					point.X,
					point.Y,
				).RGBA()
				row[column] = uint8((uint32(red>>8)*77 +
					uint32(green>>8)*150 +
					uint32(blue>>8)*29) >> 8)
			}
		}
		destination := fields + 8 + uint32(
			offset+int64(rowIndex)*bytesPerLine,
		)
		if err := r.CPU.WriteMemory(destination, row); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) signedParameter(index uint32) (int, error) {
	value, err := r.parameter(index)
	return int(int32(value)), err
}

func (r *Runtime) graphicsRectangle(
	state *ktfGraphics,
	firstParameter uint32,
) (image.Rectangle, error) {
	x, err := r.signedParameter(firstParameter)
	if err != nil {
		return image.Rectangle{}, err
	}
	y, err := r.signedParameter(firstParameter + 1)
	if err != nil {
		return image.Rectangle{}, err
	}
	width, err := r.signedParameter(firstParameter + 2)
	if err != nil {
		return image.Rectangle{}, err
	}
	height, err := r.signedParameter(firstParameter + 3)
	if err != nil {
		return image.Rectangle{}, err
	}
	x += state.offset().X
	y += state.offset().Y
	return image.Rect(x, y, x+width, y+height), nil
}

func (r *Runtime) drawGraphicsRectangle(
	state *ktfGraphics,
	rect image.Rectangle,
) {
	if rect.Empty() {
		return
	}
	r.drawGraphicsLine(state, rect.Min.X, rect.Min.Y, rect.Max.X-1, rect.Min.Y)
	r.drawGraphicsLine(state, rect.Min.X, rect.Max.Y-1, rect.Max.X-1, rect.Max.Y-1)
	r.drawGraphicsLine(state, rect.Min.X, rect.Min.Y, rect.Min.X, rect.Max.Y-1)
	r.drawGraphicsLine(state, rect.Max.X-1, rect.Min.Y, rect.Max.X-1, rect.Max.Y-1)
}

func (r *Runtime) drawGraphicsLine(
	state *ktfGraphics,
	x1, y1, x2, y2 int,
) {
	dx := guest.Abs(x2 - x1)
	stepX := -1
	if x1 < x2 {
		stepX = 1
	}
	dy := -guest.Abs(y2 - y1)
	stepY := -1
	if y1 < y2 {
		stepY = 1
	}
	lineError := dx + dy
	for {
		point := image.Pt(x1, y1)
		if point.In(state.clip) && point.In(state.Target.Bounds()) {
			state.plot(x1, y1)
		}
		if x1 == x2 && y1 == y2 {
			return
		}
		doubleError := lineError * 2
		if doubleError >= dy {
			lineError += dy
			x1 += stepX
		}
		if doubleError <= dx {
			lineError += dx
			y1 += stepY
		}
	}
}
