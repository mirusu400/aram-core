package ktf

import (
	"image"
	"path"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

type ktfAnimateImage struct {
	frames    []uint32
	rates     []int32
	width     int32
	height    int32
	mutable   bool
	repeat    bool
	playing   bool
	observer  uint32
	mediaType string
}

type ktfAnimateImageSnapshot struct {
	Frames    []uint32
	Rates     []int32
	Width     int32
	Height    int32
	Mutable   bool
	Repeat    bool
	Playing   bool
	Observer  uint32
	MediaType string
}

func snapshotKTFAnimateImages(source map[uint32]*ktfAnimateImage) map[uint32]ktfAnimateImageSnapshot {
	result := make(map[uint32]ktfAnimateImageSnapshot, len(source))
	for instance, animated := range source {
		if animated == nil {
			continue
		}
		result[instance] = ktfAnimateImageSnapshot{
			Frames: append([]uint32(nil), animated.frames...),
			Rates:  append([]int32(nil), animated.rates...),
			Width:  animated.width, Height: animated.height,
			Mutable: animated.mutable, Repeat: animated.repeat,
			Playing: animated.playing, Observer: animated.observer,
			MediaType: animated.mediaType,
		}
	}
	return result
}

func restoreKTFAnimateImages(source map[uint32]ktfAnimateImageSnapshot) map[uint32]*ktfAnimateImage {
	result := make(map[uint32]*ktfAnimateImage, len(source))
	for instance, animated := range source {
		result[instance] = &ktfAnimateImage{
			frames: append([]uint32(nil), animated.Frames...),
			rates:  append([]int32(nil), animated.Rates...),
			width:  animated.Width, height: animated.Height,
			mutable: animated.Mutable, repeat: animated.Repeat,
			playing: animated.Playing, observer: animated.Observer,
			mediaType: animated.MediaType,
		}
	}
	return result
}

func (r *Runtime) handleAnimateImageMethod(name, descriptor string) (uint32, error) {
	signature := name + descriptor
	switch signature {
	case "createAnimateImage(Ljava/lang/String;Z)Lorg/kwis/msp/lcdui/AnimateImage;":
		nameAddress, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if nameAddress == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		return r.newKTFAnimateImageResource(r.javaStringValue(nameAddress))
	case "createAnimateImage(III)Lorg/kwis/msp/lcdui/AnimateImage;":
		frameCount, err := r.signedParameter(1)
		if err != nil {
			return 0, err
		}
		width, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		height, err := r.signedParameter(3)
		if err != nil {
			return 0, err
		}
		limits := r.Services.Config.Limits
		if frameCount <= 0 || width <= 0 || height <= 0 ||
			uint64(frameCount) > uint64(limits.Assets.MaxFrames) ||
			width > int(limits.Graphics.MaxWidth) || height > int(limits.Graphics.MaxHeight) ||
			uint64(width)*uint64(height) > limits.Assets.MaxDecodedBytes/4/uint64(frameCount) {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		return r.newKTFAnimateImage(frameCount, width, height, true, "", func(int) (uint32, error) {
			return r.newJavaImage(image.NewRGBA(image.Rect(0, 0, width, height)))
		})
	case "stopImage(Lorg/kwis/msp/lcdui/ImageObserver;)V":
		observer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		for _, animated := range r.animateImages {
			if animated != nil && animated.observer == observer {
				animated.playing = false
				animated.observer = 0
			}
		}
		return 0, nil
	}

	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	animated := r.animateImages[instance]
	if animated == nil {
		return 0, nil
	}
	switch signature {
	case "isMutable()Z":
		return boolWord(animated.mutable), nil
	case "setAnimationRate(II)V":
		delay, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		frame, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if frame >= 0 && frame < len(animated.rates) && delay >= 0 {
			animated.rates[frame] = int32(delay)
		}
		return 0, nil
	case "getAnimationRate()I":
		if len(animated.rates) == 0 {
			return 0, nil
		}
		return uint32(animated.rates[0]), nil
	case "getAnimationRate(I)I":
		frame, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if frame < 0 || frame >= len(animated.rates) {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		return uint32(animated.rates[frame]), nil
	case "getMaxFrame()I":
		return uint32(len(animated.frames)), nil
	case "getWidth()I":
		return uint32(animated.width), nil
	case "getHeight()I":
		return uint32(animated.height), nil
	case "getFrameImage(I)Lorg/kwis/msp/lcdui/Image;":
		frame, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if frame < 0 || frame >= len(animated.frames) {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		return animated.frames[frame], nil
	case "setFrameImage(Lorg/kwis/msp/lcdui/Image;I)V":
		frameImage, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		frame, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		source := r.images[frameImage]
		if !animated.mutable || source == nil || frame < 0 || frame >= len(animated.frames) ||
			source.Bounds().Dx() != int(animated.width) || source.Bounds().Dy() != int(animated.height) {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		animated.frames[frame] = frameImage
		return 0, nil
	case "play(Lorg/kwis/msp/lcdui/Graphics;IIILorg/kwis/msp/lcdui/ImageObserver;)V":
		graphics, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
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
		observer, valueErr := r.parameter(6)
		if valueErr != nil {
			return 0, valueErr
		}
		if valueErr := r.paintKTFAnimateFrame(animated, graphics, 0, x, y, anchor); valueErr != nil {
			return 0, valueErr
		}
		return 0, r.startKTFAnimateImage(animated, observer)
	case "play(Lorg/kwis/msp/lcdui/ImageObserver;)V":
		observer, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.startKTFAnimateImage(animated, observer)
	case "stop()V":
		animated.playing = false
		animated.observer = 0
		return 0, nil
	case "paintFrame(Lorg/kwis/msp/lcdui/Graphics;III)Z",
		"paintFrame(Lorg/kwis/msp/lcdui/Graphics;III)V":
		graphics, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		frame, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		x, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		y, valueErr := r.signedParameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		if valueErr := r.paintKTFAnimateFrame(animated, graphics, frame, x, y, 0); valueErr != nil {
			return 0, valueErr
		}
		if strings.HasSuffix(descriptor, ")Z") {
			return 1, nil
		}
		return 0, nil
	case "setRepeat(Ljava/lang/Boolean;)Z":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		animated.repeat = r.integerValues[value] != 0
		return 1, nil
	case "setRepeat(Z)Z":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		animated.repeat = value != 0
		return 1, nil
	case "isRepeat()Z":
		return boolWord(animated.repeat), nil
	case "getImageType()Ljava/lang/String;":
		if animated.mediaType == "" {
			return 0, nil
		}
		return r.NewJavaString(animated.mediaType)
	}
	return 0, nil
}

func (r *Runtime) newKTFAnimateImage(
	frameCount, width, height int, mutable bool, mediaType string, createFrame func(int) (uint32, error),
) (uint32, error) {
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/AnimateImage", 0)
	if err != nil {
		return 0, err
	}
	animated := &ktfAnimateImage{
		frames: make([]uint32, frameCount),
		rates:  make([]int32, frameCount), width: int32(width), height: int32(height),
		mutable: mutable, mediaType: mediaType,
	}
	r.animateImages[instance] = animated
	// A frame can trigger collection before the guest has received the image.
	defer r.pinJavaConstruction(instance)()
	for index := range animated.frames {
		frame, err := createFrame(index)
		if err != nil {
			delete(r.animateImages, instance)
			return 0, err
		}
		animated.frames[index] = frame
	}
	return instance, nil
}

func (r *Runtime) newKTFAnimateImageResource(resourceName string) (uint32, error) {
	name := path.Clean(strings.TrimPrefix(strings.ReplaceAll(resourceName, `\`, "/"), "/"))
	data, found := r.findKTFResource(name)
	if !found {
		return 0, r.raiseHostJavaException("java/io/IOException")
	}
	mediaType := animateImageMediaType(name)
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		decoded, err := shared.DecodeGIF(data, r.Services.Config.Limits.Assets)
		if err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		instance, err := r.newKTFAnimateImage(
			len(decoded.Image), decoded.Config.Width, decoded.Config.Height, false, mediaType,
			func(index int) (uint32, error) { return r.newJavaImage(decoded.Image[index]) },
		)
		if err != nil {
			return 0, err
		}
		for index, delay := range decoded.Delay {
			r.animateImages[instance].rates[index] = int32(delay * 10)
		}
		return instance, nil
	}

	instance, err := r.newKTFAnimateImage(1, 0, 0, false, mediaType, func(int) (uint32, error) {
		frame, err := r.newJavaEncodedImage(data)
		if err != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		return frame, nil
	})
	if err != nil {
		return 0, err
	}
	animated := r.animateImages[instance]
	bounds := r.images[animated.frames[0]].Bounds()
	animated.width, animated.height = int32(bounds.Dx()), int32(bounds.Dy())
	return instance, nil
}

func animateImageMediaType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".gif":
		return "anim/gif"
	case ".sis":
		return "anim/sis"
	case ".abmp":
		return "anim/abmp"
	default:
		return "application/octet-stream"
	}
}

func (r *Runtime) paintKTFAnimateFrame(animated *ktfAnimateImage, graphics uint32, frame, x, y int, anchor uint32) error {
	if graphics == 0 {
		return r.raiseHostJavaException("java/lang/NullPointerException")
	}
	if frame < 0 || frame >= len(animated.frames) {
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	state := r.Graphics[graphics]
	imageAddress := animated.frames[frame]
	source := r.images[imageAddress]
	if state == nil || source == nil {
		return r.raiseHostJavaException("java/lang/IllegalArgumentException")
	}
	r.drawKTFJavaImage(state, imageAddress, source, x, y, anchor)
	return nil
}

func (r *Runtime) startKTFAnimateImage(animated *ktfAnimateImage, observer uint32) error {
	animated.playing = true
	animated.observer = observer
	if observer == 0 || len(animated.frames) == 0 {
		return nil
	}
	return r.QueueJavaVirtual(
		observer,
		"notify",
		"(Lorg/kwis/msp/lcdui/Image;I)V",
		animated.frames[0],
		1,
	)
}
