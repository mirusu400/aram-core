package ktf

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"strconv"
	"strings"
)

func (r *Runtime) handleWIPI2MediaMethod(ctx context.Context, name, descriptor string) (uint32, bool, error) {
	signature := name + descriptor
	if strings.Contains(descriptor, "Lorg/kwis/msp/media/BaseClip;") {
		compatDescriptor := strings.ReplaceAll(
			descriptor,
			"Lorg/kwis/msp/media/BaseClip;",
			"Lorg/kwis/msp/media/Clip;",
		)
		value, err := r.handleMediaMethodContext(ctx, name, compatDescriptor)
		return value, true, err
	}
	instance, err := r.parameter(1)
	if err != nil {
		return 0, true, err
	}
	clip := r.ensureKTFClip(instance)
	switch signature {
	case "allocPlayer()I":
		_, err := r.ensureKTFClipService(instance)
		if err != nil {
			return ^uint32(0), true, nil
		}
		return 0, true, nil
	case "freePlayer()I":
		return r.freeWIPI2ClipPlayer(instance), true, nil
	case "free()V":
		r.freeWIPI2ClipPlayer(instance)
		delete(r.clips, instance)
		return 0, true, nil
	case "setWaterMark(I)V":
		percent, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, true, valueErr
		}
		if percent < 0 || percent > 100 {
			return 0, true, r.raiseHostJavaException("java/lang/IllegalArgumentException")
		}
		clip.waterMark = int32(percent)
		clip.waterMarkActive = true
		return 0, true, nil
	case "reactiveWaterMark()V":
		clip.waterMarkActive = true
		return 0, true, nil
	case "mediaControl(I[I[I)I", "mediaControl(I[I[B)I",
		"mediaControl(ILjava/lang/String;[I)I":
		value, valueErr := r.controlWIPI2Media(instance, descriptor)
		return value, true, valueErr
	case "mediaModeControl(Ljava/lang/String;II[I)I":
		value, valueErr := r.controlWIPI2MediaMode(instance)
		return value, true, valueErr
	case "detect()Z":
		return 1, true, nil
	case "getModel()Ljava/lang/String;":
		value, valueErr := r.NewJavaString("ARAM Virtual Camera")
		return value, true, valueErr
	case "setMode(I)Z":
		mode, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, true, valueErr
		}
		if mode != 0 {
			return 0, true, nil
		}
		clip.cameraMode = int32(mode)
		return 1, true, nil
	case "getModeCount()I":
		return 1, true, nil
	case "setProperty(I)Z":
		property, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, true, valueErr
		}
		if property < 0 || property > 6 {
			return 0, true, nil
		}
		clip.cameraProperty = int32(property)
		return 1, true, nil
	case "setSize(IIII)Z":
		for index := range clip.cameraRect {
			value, valueErr := r.signedParameter(uint32(index + 2))
			if valueErr != nil {
				return 0, true, valueErr
			}
			if value < 0 {
				return 0, true, nil
			}
			clip.cameraRect[index] = int32(value)
		}
		return 1, true, nil
	case "enableOEMDisplayArea()V":
		clip.oemDisplay = true
		return 0, true, nil
	case "disableOEMDisplayArea()V":
		clip.oemDisplay = false
		return 0, true, nil
	case "previewStart()V":
		clip.preview = true
		return 0, true, nil
	case "previewStop()V":
		clip.preview = false
		return 0, true, nil
	case "snapshot(Lorg/kwis/msp/media/PlayListener;)Z",
		"record(Lorg/kwis/msp/media/PlayListener;)Z":
		listener, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, true, valueErr
		}
		data, valueErr := r.captureWIPI2CameraFrame()
		if valueErr != nil {
			return 0, true, valueErr
		}
		clip.data = data
		clip.capacity = max(clip.capacity, len(data))
		clip.bufferSet = true
		clip.listener = listener
		if valueErr := r.syncKTFClip(instance); valueErr != nil {
			return 0, true, valueErr
		}
		if valueErr := r.queueWIPI2MediaListener(instance, listener, 7); valueErr != nil {
			return 0, true, valueErr
		}
		return 1, true, nil
	case "view(Lorg/kwis/msp/media/PlayListener;)Z",
		"play(Lorg/kwis/msp/media/PlayListener;)Z":
		listener, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, true, valueErr
		}
		clip.listener = listener
		serviceID, valueErr := r.ensureKTFClipService(instance)
		if valueErr != nil {
			return 0, true, nil
		}
		if valueErr = r.Services.Media.Play(r.ServiceOwner, serviceID, 1); valueErr != nil {
			return 0, true, nil
		}
		clip.playing = true
		if valueErr := r.queueWIPI2MediaListener(instance, listener, 1); valueErr != nil {
			return 0, true, valueErr
		}
		return 1, true, nil
	case "getData()[B":
		if len(clip.data) == 0 {
			return 0, true, nil
		}
		data := append([]byte(nil), clip.data...)
		clip.data = nil
		if serviceID := r.clipServices[instance]; serviceID != 0 {
			_ = r.Services.Media.Clear(r.ServiceOwner, serviceID)
		}
		value, valueErr := r.newJavaByteArray(data)
		return value, true, valueErr
	case "pause()Z", "resume()Z", "stop()Z":
		serviceID := r.clipServices[instance]
		if serviceID == 0 {
			return 0, true, nil
		}
		var valueErr error
		switch name {
		case "pause":
			valueErr = r.Services.Media.Pause(r.ServiceOwner, serviceID)
		case "resume":
			valueErr = r.Services.Media.Resume(r.ServiceOwner, serviceID)
		case "stop":
			valueErr = r.Services.Media.Stop(r.ServiceOwner, serviceID)
		}
		if valueErr != nil {
			return 0, true, nil
		}
		clip.playing = name != "stop"
		return 1, true, nil
	}
	return 0, false, nil
}

func (r *Runtime) freeWIPI2ClipPlayer(instance uint32) uint32 {
	serviceID := r.clipServices[instance]
	if serviceID == 0 {
		return 0
	}
	if err := r.Services.Media.DestroyClip(r.ServiceOwner, serviceID, r.Services.Events); err != nil {
		return ^uint32(0)
	}
	delete(r.clipServices, instance)
	if clip := r.clips[instance]; clip != nil {
		clip.playing = false
	}
	return 0
}

func (r *Runtime) controlWIPI2Media(instance uint32, descriptor string) (uint32, error) {
	command, err := r.signedParameter(2)
	if err != nil {
		return 0, err
	}
	clip := r.ensureKTFClip(instance)
	switch command {
	case 0: // GET_MEDIA_TIME
		var millis int32
		if serviceID := r.clipServices[instance]; serviceID != 0 {
			if info, infoErr := r.Services.Media.Info(r.ServiceOwner, serviceID); infoErr == nil {
				millis = int32(info.Position.Milliseconds())
			}
		}
		output, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.writeWIPI2IntArrayFirst(output, millis)
	case 3: // GET_STOP_TIME
		output, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.writeWIPI2IntArrayFirst(output, clip.stopTime)
	case 4: // SET_STOP_TIME
		input, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.readWIPI2IntArrayFirst(input)
		if valueErr != nil {
			return 0, valueErr
		}
		clip.stopTime = value
		return 0, nil
	case 7: // PREVIEW_START
		clip.preview = true
		return 0, nil
	case 8: // PREVIEW_STOP
		clip.preview = false
		return 0, nil
	case 9: // SET_MODE
		if descriptor != "(ILjava/lang/String;[I)I" {
			return ^uint32(0), nil
		}
		mode, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.lwcComponent(instance).text = mode
		return 0, nil
	default:
		return ^uint32(0), nil
	}
}

func (r *Runtime) controlWIPI2MediaMode(instance uint32) (uint32, error) {
	modeAddress, err := r.parameter(2)
	if err != nil {
		return 0, err
	}
	command, err := r.signedParameter(3)
	if err != nil {
		return 0, err
	}
	propertyID, err := r.signedParameter(4)
	if err != nil {
		return 0, err
	}
	buffer, err := r.parameter(5)
	if err != nil {
		return 0, err
	}
	clip := r.ensureKTFClip(instance)
	if clip.mediaModeValues == nil {
		clip.mediaModeValues = make(map[string]int32)
	}
	key := r.javaStringValue(modeAddress) + "\x00" + strconv.Itoa(propertyID)
	if command == 0 {
		return 0, r.writeWIPI2IntArrayFirst(buffer, clip.mediaModeValues[key])
	}
	if command != 1 {
		return ^uint32(0), nil
	}
	value, err := r.readWIPI2IntArrayFirst(buffer)
	if err != nil {
		return 0, err
	}
	clip.mediaModeValues[key] = value
	return 0, nil
}

func (r *Runtime) readWIPI2IntArrayFirst(array uint32) (int32, error) {
	values, err := r.readWIPI2IntArray(array)
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		return 0, r.raiseHostJavaException("java/lang/ArrayIndexOutOfBoundsException")
	}
	return int32(values[0]), nil
}

func (r *Runtime) writeWIPI2IntArrayFirst(array uint32, value int32) error {
	if array == 0 {
		return r.raiseHostJavaException("java/lang/NullPointerException")
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return err
	}
	if length == 0 {
		return r.raiseHostJavaException("java/lang/ArrayIndexOutOfBoundsException")
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return err
	}
	return r.WriteU32(fields+8, uint32(value))
}

func (r *Runtime) captureWIPI2CameraFrame() ([]byte, error) {
	frame := r.frame
	if frame == nil || frame.Bounds().Empty() {
		frame = image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, frame, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func (r *Runtime) queueWIPI2MediaListener(instance, listener uint32, event uint32) error {
	if listener == 0 {
		return nil
	}
	return r.QueueJavaVirtual(
		listener,
		"playUpdate",
		"(Lorg/kwis/msp/media/Clip;II)V",
		instance,
		event,
		0,
	)
}
