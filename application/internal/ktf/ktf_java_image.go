package ktf

import (
	"errors"
	"fmt"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleFontMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "getDefaultFont()Lorg/kwis/msp/lcdui/Font;":
		return r.ensureDefaultFont()
	case "getFont(III)Lorg/kwis/msp/lcdui/Font;":
		face, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		style, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		size, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		font := JavaFont{
			Face: face, Style: style, Size: size,
		}
		if !font.valid() {
			return r.raiseJavaException(
				"java/lang/IllegalArgumentException",
				0,
			)
		}
		return r.EnsureKTFFont(font)
	case "getHeight()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		metrics, err := r.Services.Text.Metrics(r.ServiceOwner, fontID)
		return uint32(metrics.Height), err
	case "getBaselinePosition()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		metrics, err := r.Services.Text.Metrics(r.ServiceOwner, fontID)
		return uint32(metrics.Ascent), err
	case "getFace()I", "getSize()I", "getStyle()I",
		"isBold()Z", "isItalic()Z", "isPlain()Z", "isUnderlined()Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		font, err := r.ktfFont(instance)
		if err != nil {
			return 0, err
		}
		switch name {
		case "getFace":
			return font.Face, nil
		case "getSize":
			return font.Size, nil
		case "getStyle":
			return font.Style, nil
		case "isBold":
			return boolWord(font.Style&1 != 0), nil
		case "isItalic":
			return boolWord(font.Style&2 != 0), nil
		case "isPlain":
			return boolWord(font.Style == 0), nil
		default:
			return boolWord(font.Style&4 != 0), nil
		}
	case "charWidth(C)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		character, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		glyph, err := r.Services.Text.Glyph(
			r.ServiceOwner,
			fontID,
			rune(character),
		)
		return uint32(glyph.Advance), err
	case "charsWidth([CII)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		text, err := r.readJavaCharArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		width, err := r.Services.Text.Measure(
			r.ServiceOwner,
			fontID,
			text,
		)
		return uint32(width), err
	case "stringWidth(Ljava/lang/String;)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		width, err := r.Services.Text.Measure(
			r.ServiceOwner,
			fontID,
			r.javaStringValue(value),
		)
		return uint32(width), err
	case "substringWidth(Ljava/lang/String;II)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(4)
		if err != nil {
			return 0, err
		}
		runes := []rune(r.javaStringValue(value))
		if offset > uint32(len(runes)) ||
			count > uint32(len(runes))-offset {
			return 0, nil
		}
		fontID, err := r.ensureKTFFontService(instance)
		if err != nil {
			return 0, err
		}
		width, err := r.Services.Text.Measure(
			r.ServiceOwner,
			fontID,
			string(runes[offset:offset+count]),
		)
		return uint32(width), err
	default:
		return 0, nil
	}
}

func (r *Runtime) ensureDefaultFont() (uint32, error) {
	if r.defaultFont != 0 {
		return r.defaultFont, nil
	}
	var err error
	r.defaultFont, err = r.newJavaInstance(
		"org/kwis/msp/lcdui/Font",
		12,
	)
	if err != nil {
		return 0, err
	}
	fontID, err := r.Services.Text.CreateFont(
		r.ServiceOwner,
		shared.FontDescriptor{
			Family: "aram-fallback",
			Size:   12,
		},
	)
	if err != nil {
		return 0, err
	}
	r.FontServices[r.defaultFont] = fontID
	return r.defaultFont, nil
}

func (r *Runtime) EnsureKTFFont(font JavaFont) (uint32, error) {
	if !font.valid() {
		return 0, fmt.Errorf(
			"invalid KTF font face=%d style=%d size=%d",
			font.Face,
			font.Style,
			font.Size,
		)
	}
	if font == (JavaFont{}) {
		return r.ensureDefaultFont()
	}
	for _, instance := range guest.SortedUint32Keys(r.FontServices) {
		if instance >= ktfWIPICFontServiceKey {
			continue
		}
		current, err := r.ktfFont(instance)
		if err == nil && current == font {
			return instance, nil
		}
	}
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/Font", 12)
	if err != nil {
		return 0, err
	}
	for offset, value := range []uint32{font.Face, font.Style, font.Size} {
		if err := r.WriteJavaFieldWord(
			instance,
			uint32(offset)*4,
			value,
		); err != nil {
			return 0, err
		}
	}
	if _, err := r.ensureKTFFontService(instance); err != nil {
		return 0, err
	}
	return instance, nil
}

func (r *Runtime) ktfFont(instance uint32) (JavaFont, error) {
	if instance == 0 {
		return JavaFont{}, errors.New("KTF Font instance is null")
	}
	if instance == r.defaultFont {
		return JavaFont{}, nil
	}
	var font JavaFont
	values := []*uint32{&font.Face, &font.Style, &font.Size}
	for offset, target := range values {
		value, err := r.readJavaFieldWord(instance, uint32(offset)*4)
		if err != nil {
			return JavaFont{}, err
		}
		*target = value
	}
	if !font.valid() {
		return JavaFont{}, fmt.Errorf(
			"invalid KTF Font object 0x%08x",
			instance,
		)
	}
	return font, nil
}

func (r *Runtime) ensureKTFFontService(
	instance uint32,
) (shared.ServiceID, error) {
	if serviceID := r.FontServices[instance]; serviceID != 0 {
		return serviceID, nil
	}
	font, err := r.ktfFont(instance)
	if err != nil {
		return 0, err
	}
	serviceID, err := r.Services.Text.CreateFont(
		r.ServiceOwner,
		font.descriptor(),
	)
	if err != nil {
		return 0, err
	}
	r.FontServices[instance] = serviceID
	return serviceID, nil
}

func (r *Runtime) handleImageMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "createImage(II)Lorg/kwis/msp/lcdui/Image;":
		width, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		height, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if width == 0 || height == 0 || width > 4096 || height > 4096 {
			return 0, fmt.Errorf("invalid KTF image size %dx%d", width, height)
		}
		return r.newJavaImage(image.NewRGBA(image.Rect(
			0,
			0,
			int(width),
			int(height),
		)))
	case "createImage(Lorg/kwis/msp/lcdui/Image;)Lorg/kwis/msp/lcdui/Image;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		source := r.images[instance]
		if source == nil {
			return r.raiseJavaException("java/lang/NullPointerException", 0)
		}
		bounds := source.Bounds()
		clone := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		draw.Draw(clone, clone.Bounds(), source, bounds.Min, draw.Src)
		return r.newJavaImage(clone)
	case "createImage(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Image;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		resourceName := strings.TrimPrefix(
			strings.ReplaceAll(r.javaStringValue(nameAddress), `\`, "/"),
			"/",
		)
		resourceName = path.Clean(resourceName)
		data, ok := r.findKTFResource(resourceName)
		if !ok {
			r.trace("java_image_missing:" + resourceName)
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		instance, decodeErr := r.newJavaEncodedImage(data)
		if decodeErr != nil {
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		return instance, nil
	case "createImage([BII)Lorg/kwis/msp/lcdui/Image;":
		array, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		offset, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		data, err := r.readJavaByteArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		instance, decodeErr := r.newJavaEncodedImage(data)
		if decodeErr != nil {
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
		}
		return instance, nil
	case "getWidth()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if source := r.images[instance]; source != nil {
			return uint32(source.Bounds().Dx()), nil
		}
		return 0, nil
	case "getHeight()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if source := r.images[instance]; source != nil {
			return uint32(source.Bounds().Dy()), nil
		}
		return 0, nil
	case "getGraphics()Lorg/kwis/msp/lcdui/Graphics;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		target, ok := r.images[instance].(draw.Image)
		if !ok {
			return 0, nil
		}
		graphics, err := r.newJavaInstance(
			"org/kwis/msp/lcdui/Graphics",
			4,
		)
		if err != nil {
			return 0, err
		}
		r.Graphics[graphics] = &ktfGraphics{
			Target: target,
			clip:   target.Bounds(),
			color:  color.RGBA{A: 0xff},
		}
		r.GraphicsServices[graphics] = r.imageServices[instance]
		return graphics, nil
	case "loadImage(Ljava/lang/String;Lorg/kwis/msp/lcdui/ImageObserver;)Lorg/kwis/msp/lcdui/Image;":
		// The observer never fires: the host decodes synchronously, so the
		// image is complete before the reference is returned.
		return r.handleImageMethod(
			"createImage",
			"(Ljava/lang/String;)Lorg/kwis/msp/lcdui/Image;",
		)
	case "isMutable()Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if _, mutable := r.images[instance].(draw.Image); mutable {
			return 1, nil
		}
		return 0, nil
	case "isAnimated()Z":
		// Animated sources decode to their first frame on this host.
		return 0, nil
	case "play(Lorg/kwis/msp/lcdui/ImageObserver;)V", "stop()V",
		"stopImage(Lorg/kwis/msp/lcdui/ImageObserver;)V":
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) newJavaImage(source image.Image) (uint32, error) {
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/Image", 8)
	if err != nil {
		return 0, err
	}
	r.images[instance] = source
	bounds := source.Bounds()
	surface, err := r.Services.Graphics.CreateSurface(
		r.ServiceOwner,
		shared.SurfaceDescriptor{
			Width:  int32(bounds.Dx()),
			Height: int32(bounds.Dy()),
			Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		return 0, err
	}
	if err := r.Services.Graphics.ReplacePixels(
		r.ServiceOwner,
		surface,
		ktfRGBABytes(source),
	); err != nil {
		_ = r.Services.Graphics.DestroySurface(r.ServiceOwner, surface)
		return 0, err
	}
	r.imageServices[instance] = surface
	return instance, nil
}

func (r *Runtime) newJavaEncodedImage(data []byte) (uint32, error) {
	asset, err := r.Services.Assets.Decode(
		r.ServiceOwner,
		data,
		shared.DecodeOptions{},
	)
	if err != nil {
		return 0, err
	}
	info, err := r.Services.Assets.Info(r.ServiceOwner, asset)
	if err != nil || len(info.Frames) == 0 {
		_ = r.Services.Assets.Release(r.ServiceOwner, asset)
		if err == nil {
			err = fmt.Errorf("decoded KTF image has no frames")
		}
		return 0, err
	}
	pixels, err := r.Services.Graphics.RGBA(
		r.ServiceOwner,
		info.Frames[0].Surface,
	)
	if err != nil {
		_ = r.Services.Assets.Release(r.ServiceOwner, asset)
		return 0, err
	}
	// Assets exposes straight-alpha RGBA bytes. Keep them in NRGBA form:
	// image.RGBA expects premultiplied channels, and storing transparent
	// magenta there makes draw.Over leak the RGB color through alpha zero.
	source := image.NewNRGBA(image.Rect(
		0,
		0,
		int(info.Width),
		int(info.Height),
	))
	copy(source.Pix, pixels)
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/Image", 8)
	if err != nil {
		_ = r.Services.Assets.Release(r.ServiceOwner, asset)
		return 0, err
	}
	r.images[instance] = source
	r.imageServices[instance] = info.Frames[0].Surface
	r.javaAssetServices[instance] = asset
	return instance, nil
}

func ktfRGBABytes(source image.Image) []byte {
	bounds := source.Bounds()
	pixels := make([]byte, bounds.Dx()*bounds.Dy()*4)
	offset := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, alpha := source.At(x, y).RGBA()
			pixels[offset+0] = uint8(red >> 8)
			pixels[offset+1] = uint8(green >> 8)
			pixels[offset+2] = uint8(blue >> 8)
			pixels[offset+3] = uint8(alpha >> 8)
			offset += 4
		}
	}
	return pixels
}

func (r *Runtime) findKTFResource(name string) ([]byte, bool) {
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return nil, false
	}
	if data, err := r.Services.Storage.ReadFile(
		shared.NamespacePackage,
		name,
	); err == nil {
		return data, true
	}
	if data, ok := r.Pkg.Resources[name]; ok {
		return data, true
	}
	for candidate, data := range r.Pkg.Resources {
		if strings.EqualFold(candidate, name) ||
			strings.EqualFold(path.Base(candidate), path.Base(name)) {
			if mounted, err := r.Services.Storage.ReadFile(
				shared.NamespacePackage,
				candidate,
			); err == nil {
				return mounted, true
			}
			return data, true
		}
	}
	// A handset exposes a Clet's own written files back through
	// MC_knlGetResource: titles persist a save with MC_fsWrite and then decide
	// on the next launch by looking the same name up as a resource. 에픽크로니클PE
	// writes its speed calibration to gopt.sav, shows the "restart required"
	// notice, and exits; on the relaunch it reloads gopt.sav this way to skip
	// the calibration. Package resources are checked first, so a bundled asset
	// is never shadowed — only names absent from the jar fall through here.
	if data, err := r.Services.Storage.ReadFile(
		shared.NamespacePrivate,
		normalizeKTFFileName(name),
	); err == nil {
		return data, true
	}
	return nil, false
}

type ktfJavaImageDraw struct {
	image  uint32
	x      int
	y      int
	anchor uint32
}

func (r *Runtime) drawKTFJavaImageRaw(
	state *ktfGraphics,
	source image.Image,
	x, y int,
	anchor uint32,
) {
	if state == nil || source == nil {
		return
	}
	if anchor&8 != 0 {
		x -= source.Bounds().Dx()
	} else if anchor&1 != 0 {
		x -= source.Bounds().Dx() / 2
	}
	if anchor&32 != 0 {
		y -= source.Bounds().Dy()
	} else if anchor&2 != 0 {
		y -= source.Bounds().Dy() / 2
	}
	point := image.Pt(x+state.translate.X, y+state.translate.Y)
	targetRect := source.Bounds().Add(point.Sub(source.Bounds().Min))
	clippedRect := targetRect.Intersect(state.clip)
	sourcePoint := source.Bounds().Min.Add(
		clippedRect.Min.Sub(targetRect.Min),
	)
	draw.Draw(
		state.Target,
		clippedRect,
		source,
		sourcePoint,
		draw.Over,
	)
	state.PixelsDirty = true
}
