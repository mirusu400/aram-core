package ktf

import (
	"errors"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleMediaMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(Ljava/lang/String;)V",
		"<init>(Ljava/lang/String;I)V",
		"<init>(Ljava/lang/String;Ljava/lang/String;)V",
		"<init>(Ljava/lang/String;[B)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		clip := &ktfClip{volume: 100}
		if descriptor == "(Ljava/lang/String;[B)V" {
			array, valueErr := r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			if array != 0 {
				clip.data, valueErr = r.readJavaByteArray(array)
				if valueErr != nil {
					return 0, valueErr
				}
			}
		} else {
			resource, found, valueErr := r.ktfClipConstructorResource(
				descriptor,
			)
			if valueErr != nil {
				return 0, valueErr
			}
			if found {
				clip.data = resource
			}
		}
		r.clips[instance] = clip
		return 0, r.syncKTFClip(instance)
	case "availableDataSize()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(len(r.ensureKTFClip(instance).data)), nil
	case "clearData()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		r.ensureKTFClip(instance).data = nil
		return 0, r.syncKTFClip(instance)
	case "putData([BII)I":
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
		data, err := r.readJavaByteArrayRange(array, offset, count)
		if err != nil {
			return 0, err
		}
		clip := r.ensureKTFClip(instance)
		clip.data = append(clip.data, data...)
		return count, r.syncKTFClip(instance)
	case "getData([BII)I":
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
		clip := r.ensureKTFClip(instance)
		if count > uint32(len(clip.data)) {
			count = uint32(len(clip.data))
		}
		if err := r.writeJavaByteArrayRange(
			array,
			offset,
			clip.data[:count],
		); err != nil {
			return 0, err
		}
		clip.data = append(clip.data[:0], clip.data[count:]...)
		return count, r.syncKTFClip(instance)
	case "setBuffer([BI)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		count, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		data, err := r.readJavaByteArrayRange(array, 0, count)
		if err != nil {
			return 0, err
		}
		r.ensureKTFClip(instance).data = data
		return 1, r.syncKTFClip(instance)
	case "setVolume(I)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		volume, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.volume = int32(volume)
		return 1, r.syncKTFClipGain(instance)
	case "getVolume()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if clip := r.clips[instance]; clip != nil {
			return uint32(clip.volume), nil
		}
		return 0, nil
	case "setListener(Lorg/kwis/msp/media/PlayListener;)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		listener, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.listener = listener
		return 0, nil
	case "play(Lorg/kwis/msp/media/Clip;Z)Z",
		"stop(Lorg/kwis/msp/media/Clip;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.playing = name == "play"
		serviceID, serviceErr := r.ensureKTFClipService(instance)
		if serviceErr != nil {
			return 0, serviceErr
		}
		if clip.playing {
			plays := int32(1)
			if repeat, valueErr := r.parameter(2); valueErr == nil && repeat != 0 {
				plays = -1
			}
			serviceErr = r.Services.Media.Play(
				r.ServiceOwner,
				serviceID,
				plays,
			)
		} else {
			serviceErr = r.Services.Media.Stop(r.ServiceOwner, serviceID)
		}
		return 1, serviceErr
	default:
		return 0, nil
	}
}

func (r *Runtime) ktfClipConstructorResource(
	descriptor string,
) ([]byte, bool, error) {
	stringParameters := []uint32{2}
	if descriptor == "(Ljava/lang/String;Ljava/lang/String;)V" {
		stringParameters = append(stringParameters, 3)
	}
	for _, parameter := range stringParameters {
		address, err := r.parameter(parameter)
		if err != nil {
			return nil, false, err
		}
		name := strings.TrimPrefix(
			strings.ReplaceAll(r.javaStringValue(address), `\`, "/"),
			"/",
		)
		name = path.Clean(name)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") {
			continue
		}
		data, ok := r.findKTFResource(name)
		r.tracef("java_clip_resource:%s:found=%t:size=%d", name, ok, len(data))
		if ok {
			return append([]byte(nil), data...), true, nil
		}
	}
	return nil, false, nil
}

func (r *Runtime) ensureKTFClip(instance uint32) *ktfClip {
	clip := r.clips[instance]
	if clip == nil {
		clip = &ktfClip{volume: 100}
		r.clips[instance] = clip
	}
	return clip
}

func (r *Runtime) ensureKTFClipService(
	instance uint32,
) (shared.ServiceID, error) {
	if serviceID := r.clipServices[instance]; serviceID != 0 {
		return serviceID, nil
	}
	serviceID, err := r.Services.Media.CreateClip(
		r.ServiceOwner,
		"",
		0,
	)
	if errors.Is(err, shared.ErrLimitExceeded) && r.recycleKTFClipService() {
		serviceID, err = r.Services.Media.CreateClip(
			r.ServiceOwner,
			"",
			0,
		)
	}
	if err != nil {
		return 0, err
	}
	r.clipServices[instance] = serviceID
	return serviceID, nil
}

// recycleKTFClipService frees the host media service backing the oldest Java
// clip and reports whether it freed one. The KTF runtime has no Java
// collector, so a title that constructs a Clip per sound effect would
// otherwise exhaust the bounded media pool and fault. Instances are numbered
// in allocation order, so the lowest handle is the oldest clip and the choice
// stays deterministic.
//
// Idle clips are retired first. When every clip is playing the oldest one is
// stopped and taken anyway, which is what a handset mixer does when a title
// asks for more simultaneous voices than the device has. The Java-side sample
// data lives in ktfClip, so a recycled clip reallocates and refills its
// service the next time the guest touches it.
func (r *Runtime) recycleKTFClipService() bool {
	idle, playing := uint32(0), uint32(0)
	for instance, serviceID := range r.clipServices {
		if serviceID == 0 {
			continue
		}
		if clip := r.clips[instance]; clip != nil && clip.playing {
			if playing == 0 || instance < playing {
				playing = instance
			}
			continue
		}
		if idle == 0 || instance < idle {
			idle = instance
		}
	}
	victim := idle
	if victim == 0 {
		victim = playing
	}
	if victim == 0 {
		return false
	}
	if err := r.Services.Media.DestroyClip(
		r.ServiceOwner,
		r.clipServices[victim],
		r.Services.Events,
	); err != nil {
		return false
	}
	if clip := r.clips[victim]; clip != nil {
		clip.playing = false
	}
	delete(r.clipServices, victim)
	return true
}

func (r *Runtime) syncKTFClip(instance uint32) error {
	clip := r.ensureKTFClip(instance)
	serviceID, err := r.ensureKTFClipService(instance)
	if err != nil {
		return err
	}
	if err := r.Services.Media.Clear(r.ServiceOwner, serviceID); err != nil {
		return err
	}
	if _, err := r.Services.Media.Append(
		r.ServiceOwner,
		serviceID,
		clip.data,
	); err != nil {
		return err
	}
	return r.syncKTFClipGain(instance)
}

func (r *Runtime) syncKTFClipGain(instance uint32) error {
	clip := r.ensureKTFClip(instance)
	serviceID, err := r.ensureKTFClipService(instance)
	if err != nil {
		return err
	}
	volume := max(int32(0), min(int32(100), clip.volume))
	return r.Services.Media.SetClipGain(
		r.ServiceOwner,
		serviceID,
		uint8(volume),
		false,
		0,
	)
}

func (r *Runtime) handleCalendarMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "getInstance()Ljava/util/Calendar;":
		calendar, valueErr := r.NewHostJavaObject("java/util/Calendar")
		if valueErr == nil {
			r.dates[calendar] = int64(r.TickMS)
		}
		return calendar, valueErr
	case "get(I)I":
		return 0, nil
	case "set(II)V":
		return 0, nil
	case "setTime(Ljava/util/Date;)V":
		date, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = r.dates[date]
		return 0, nil
	case "getTime()Ljava/util/Date;":
		date, valueErr := r.NewHostJavaObject("java/util/Date")
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[date] = r.dates[instance]
		return date, nil
	case "setTimeInMillis(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = int64(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "getTimeInMillis()J":
		return r.javaLongResult(uint64(r.dates[instance])), nil
	default:
		return 0, nil
	}
}
