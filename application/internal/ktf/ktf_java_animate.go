package ktf

import (
	"bytes"
	"image"
	"image/gif"
	"path"
	"strings"
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
		if frameCount <= 0 || width <= 0 || height <= 0 {
			return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		frames := make([]uint32, frameCount)
		for index := range frames {
			frame, valueErr := r.newJavaImage(image.NewRGBA(image.Rect(0, 0, width, height)))
			if valueErr != nil {
				return 0, valueErr
			}
			frames[index] = frame
		}
		return r.newKTFAnimateImage(frames, width, height, true, "")
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

func (r *Runtime) newKTFAnimateImage(frames []uint32, width, height int, mutable bool, mediaType string) (uint32, error) {
	instance, err := r.newJavaInstance("org/kwis/msp/lcdui/AnimateImage", 0)
	if err != nil {
		return 0, err
	}
	r.animateImages[instance] = &ktfAnimateImage{
		frames: append([]uint32(nil), frames...),
		rates:  make([]int32, len(frames)), width: int32(width), height: int32(height),
		mutable: mutable, mediaType: mediaType,
	}
	return instance, nil
}

func (r *Runtime) newKTFAnimateImageResource(resourceName string) (uint32, error) {
	name := path.Clean(strings.TrimPrefix(strings.ReplaceAll(resourceName, `\`, "/"), "/"))
	data, found := r.findKTFResource(name)
	if !found {
		return 0, r.raiseHostJavaException("java/io/IOException")
	}
	var frames []uint32
	var rates []int32
	width, height := 0, 0
	mediaType := animateImageMediaType(name)
	if decoded, err := gif.DecodeAll(bytes.NewReader(data)); err == nil && len(decoded.Image) != 0 {
		width, height = decoded.Config.Width, decoded.Config.Height
		for index, source := range decoded.Image {
			frame, valueErr := r.newJavaImage(source)
			if valueErr != nil {
				return 0, valueErr
			}
			frames = append(frames, frame)
			delay := int32(0)
			if index < len(decoded.Delay) {
				delay = int32(decoded.Delay[index] * 10)
			}
			rates = append(rates, delay)
		}
	} else {
		frame, valueErr := r.newJavaEncodedImage(data)
		if valueErr != nil {
			return 0, r.raiseHostJavaException("java/io/IOException")
		}
		frames = []uint32{frame}
		source := r.images[frame]
		width, height = source.Bounds().Dx(), source.Bounds().Dy()
		rates = []int32{0}
	}
	instance, err := r.newKTFAnimateImage(frames, width, height, false, mediaType)
	if err == nil {
		r.animateImages[instance].rates = rates
	}
	return instance, err
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
