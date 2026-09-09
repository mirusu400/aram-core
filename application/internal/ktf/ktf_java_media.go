package ktf

import (
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleMediaMethod(
	name, descriptor string,
) (uint32, error) {
	return r.handleMediaMethodContext(context.Background(), name, descriptor)
}

func (r *Runtime) handleMediaMethodContext(
	ctx context.Context,
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
		if mediaType, typeErr := r.parameter(2); typeErr == nil {
			// The declared media type string backs getType().
			r.lwcComponent(instance).text = mediaType
		}
		if descriptor == "(Ljava/lang/String;I)V" {
			size, valueErr := r.signedParameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			if size < 0 {
				return 0, r.raiseHostJavaException("java/lang/IllegalArgumentException")
			}
			clip.capacity = size
			clip.bufferSet = true
		} else if descriptor == "(Ljava/lang/String;[B)V" {
			array, valueErr := r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			if array != 0 {
				clip.data, valueErr = r.readJavaByteArray(array)
				if valueErr != nil {
					return 0, valueErr
				}
				clip.capacity = len(clip.data)
				clip.bufferSet = true
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
				clip.capacity = len(resource)
				clip.bufferSet = true
			}
		}
		r.clips[instance] = clip
		return 0, r.syncKTFClip(instance)
	case "availableDataSize()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return 0, err
		}
		available, err := r.Services.Media.AvailableBytes(r.ServiceOwner, serviceID)
		if err != nil {
			return 0, nil
		}
		return uint32(available), nil
	case "clearData()V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return 0, err
		}
		if err := r.Services.Media.Clear(r.ServiceOwner, serviceID); err != nil {
			return 0, nil
		}
		r.ensureKTFClip(instance).data = nil
		return 0, nil
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
		if clip.capacity > 0 {
			remaining := max(clip.capacity-len(clip.data), 0)
			if len(data) > remaining {
				data = data[:remaining]
			}
		}
		if len(data) == 0 {
			return 0, nil
		}
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return 0, err
		}
		if _, err := r.Services.Media.Append(r.ServiceOwner, serviceID, data); err != nil {
			return ^uint32(0), nil
		}
		clip.data = append(clip.data, data...)
		return uint32(len(data)), nil
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
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return 0, err
		}
		data, err := r.Services.Media.TakeBuffered(r.ServiceOwner, serviceID, uint64(count))
		if err != nil {
			return ^uint32(0), nil
		}
		if err := r.writeJavaByteArrayRange(
			array,
			offset,
			data,
		); err != nil {
			return 0, err
		}
		clip := r.ensureKTFClip(instance)
		clip.data, _ = r.Services.Media.Source(r.ServiceOwner, serviceID)
		return uint32(len(data)), nil
	case "setBuffer([BI)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		array, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if array == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		count, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		clip := r.ensureKTFClip(instance)
		if clip.bufferSet {
			return 0, nil
		}
		length, err := r.javaArrayLength(array)
		if err != nil {
			return 0, err
		}
		if count > length {
			return 0, r.raiseHostJavaException("java/lang/ArrayIndexOutOfBoundsException")
		}
		data, err := r.readJavaByteArrayRange(array, 0, count)
		if err != nil {
			return 0, err
		}
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return 0, err
		}
		if err := r.Services.Media.ReplaceSource(r.ServiceOwner, serviceID, data); err != nil {
			return 0, nil
		}
		clip.data = data
		clip.capacity = int(length)
		clip.bufferSet = true
		return 1, nil
	case "setVolume(I)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		volume, err := r.signedParameter(2)
		if err != nil {
			return 0, err
		}
		if volume < 0 || volume > 100 {
			return 0, nil
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
	case "getType()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if mediaType := r.lwcComponent(instance).text; mediaType != 0 {
			return mediaType, nil
		}
		return r.NewJavaString("")
	case "setPosition(I)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		position, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return 0, nil
		}
		if err := r.Services.Media.Seek(
			r.ServiceOwner,
			serviceID,
			time.Duration(position)*time.Millisecond,
		); err != nil {
			return 0, nil
		}
		return 1, nil
	case "playStart(Z)Z", "recordStart()Z", "playUpdate(II)Z":
		// Base implementation of the protected guest override hook.
		return 1, nil
	case "getPlayerID(Ljava/lang/String;)I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return ^uint32(0), nil
		}
		return serviceID.Slot(), nil
	case "mediaWriteData()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		clip := r.ensureKTFClip(instance)
		serviceID, err := r.ensureKTFClipService(instance)
		if err != nil {
			return ^uint32(0), nil
		}
		if err := r.Services.Media.ReplaceSource(
			r.ServiceOwner, serviceID, clip.data,
		); err != nil {
			return ^uint32(0), nil
		}
		return uint32(len(clip.data)), nil
	case "mediaReadData()I":
		// No capture device is attached, so a successful read contains no data.
		return 0, nil
	case "mediaFreeze()I":
		r.trace("java_media_record_freeze_unsupported")
		return ^uint32(0), nil
	case "control(IILjava/lang/Object;Ljava/lang/Object;)I":
		playerID, err := r.parameter(1)
		if err != nil {
			return ^uint32(0), err
		}
		for _, serviceID := range r.clipServices {
			if uint32(serviceID) == playerID {
				r.tracef("java_media_control:player=%d", playerID)
				return 0, nil
			}
		}
		return ^uint32(0), nil
	case "atomicGetUpdate(I)V", "atomicPutUpdate(I)V":
		return 0, nil
	case "record(Lorg/kwis/msp/media/Clip;)Z":
		// Recording hardware is absent.
		return 0, nil
	case "play(Lorg/kwis/msp/media/Clip;Z)Z",
		"stop(Lorg/kwis/msp/media/Clip;)Z",
		"pause(Lorg/kwis/msp/media/Clip;)Z",
		"resume(Lorg/kwis/msp/media/Clip;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if instance == 0 {
			return 0, nil
		}
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		serviceID, serviceErr := r.ensureKTFClipService(instance)
		if serviceErr != nil {
			return 0, serviceErr
		}
		switch name {
		case "play":
			plays := int32(1)
			repeat, valueErr := r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
			if repeat != 0 {
				plays = -1
			}
			if overridden, overrideErr := r.hasKTFPlayStartOverride(instance); overrideErr != nil {
				return 0, overrideErr
			} else if overridden {
				allowed, invokeErr := r.invokeJavaVirtual(
					ctx,
					instance,
					"playStart",
					"(Z)Z",
					repeat,
				)
				if invokeErr != nil {
					return 0, invokeErr
				}
				if allowed == 0 {
					return 0, nil
				}
			}
			serviceErr = r.Services.Media.Play(
				r.ServiceOwner,
				serviceID,
				plays,
			)
		case "stop":
			serviceErr = r.Services.Media.Stop(r.ServiceOwner, serviceID)
		case "pause":
			serviceErr = r.Services.Media.Pause(r.ServiceOwner, serviceID)
		case "resume":
			serviceErr = r.Services.Media.Resume(r.ServiceOwner, serviceID)
		}
		if serviceErr == nil {
			info, infoErr := r.Services.Media.Info(r.ServiceOwner, serviceID)
			if infoErr != nil {
				return 0, infoErr
			}
			// A paused clip still owns live playback state and must not be
			// mistaken for an idle recycling candidate.
			clip.playing = info.State != shared.ClipStopped
			event := guest.WIPIMediaStart
			switch name {
			case "stop":
				event = guest.WIPIMediaStop
			case "pause":
				event = guest.WIPIMediaPause
			case "resume":
				event = guest.WIPIMediaResume
			}
			if err := r.queueKTFPlayListener(instance, event); err != nil {
				return 0, err
			}
		}
		if serviceErr != nil {
			return 0, nil
		}
		return 1, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) hasKTFPlayStartOverride(instance uint32) (bool, error) {
	words, err := r.ReadWords(instance, 2)
	if err != nil {
		return false, err
	}
	actual, err := r.resolveJavaMethod(words[1], "playStart", "(Z)Z")
	if err != nil {
		return false, nil
	}
	baseClass, err := r.EnsureJavaClass("org/kwis/msp/media/Clip")
	if err != nil {
		return false, err
	}
	base, err := r.resolveJavaMethod(baseClass, "playStart", "(Z)Z")
	if err != nil {
		return false, nil
	}
	return actual != base, nil
}

func (r *Runtime) queueKTFPlayListener(instance uint32, event int32) error {
	clip := r.clips[instance]
	if clip == nil || clip.listener == 0 {
		return nil
	}
	return r.QueueJavaVirtual(
		clip.listener,
		"playUpdate",
		"(Lorg/kwis/msp/media/Clip;II)V",
		instance,
		uint32(event),
		0,
	)
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
	var idle, playing uint32
	var haveIdle, havePlaying bool
	for instance, serviceID := range r.clipServices {
		// A Clip method reached on a null receiver files itself under
		// instance 0. It is not a clip anyone can play, and it used to end the
		// scan: 0 doubled as "no victim yet", so whenever Go's randomized map
		// order put that entry last the recycler reported it had nothing to
		// free and the next Clip faulted on the full pool (issue #130).
		if instance == 0 || serviceID == 0 {
			continue
		}
		if clip := r.clips[instance]; clip != nil && clip.playing {
			if !havePlaying || instance < playing {
				playing, havePlaying = instance, true
			}
			continue
		}
		if !haveIdle || instance < idle {
			idle, haveIdle = instance, true
		}
	}
	victim := idle
	if !haveIdle {
		victim = playing
		if !havePlaying {
			return false
		}
	}
	serviceID := r.clipServices[victim]
	if info, err := r.Services.Media.Info(r.ServiceOwner, serviceID); err == nil &&
		info.State != shared.ClipStopped {
		if err := r.Services.Media.Stop(r.ServiceOwner, serviceID); err != nil {
			return false
		}
	}
	if err := r.Services.Media.DestroyClip(
		r.ServiceOwner,
		serviceID,
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
	if err := r.Services.Media.ReplaceSource(
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
	switch name + descriptor {
	case "getInstance()Ljava/util/Calendar;":
		calendar, valueErr := r.NewHostJavaObject("java/util/Calendar")
		if valueErr == nil {
			r.dates[calendar] = int64(r.TickMS)
			zone, zoneErr := r.newKTFTimeZone(r.defaultKTFTimeZone())
			if zoneErr != nil {
				return 0, zoneErr
			}
			r.calendarZones[calendar] = zone
		}
		return calendar, valueErr
	case "getInstance(Ljava/util/TimeZone;)Ljava/util/Calendar;":
		zone, valueErr := r.parameter(1)
		if valueErr != nil {
			return 0, valueErr
		}
		if zone == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		calendar, valueErr := r.NewHostJavaObject("java/util/Calendar")
		if valueErr == nil {
			r.dates[calendar] = int64(r.TickMS)
			r.calendarZones[calendar] = zone
		}
		return calendar, valueErr
	}
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		r.dates[instance] = int64(r.TickMS)
		zone, valueErr := r.newKTFTimeZone(r.defaultKTFTimeZone())
		if valueErr != nil {
			return 0, valueErr
		}
		r.calendarZones[instance] = zone
		return 0, nil
	case "<init>(Ljava/util/TimeZone;)V":
		zone, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = int64(r.TickMS)
		r.calendarZones[instance] = zone
		return 0, nil
	case "<init>(III)V":
		year, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		month, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		day, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		zone, valueErr := r.newKTFTimeZone(r.defaultKTFTimeZone())
		if valueErr != nil {
			return 0, valueErr
		}
		r.calendarZones[instance] = zone
		r.dates[instance] = time.Date(
			year, time.Month(month+1), day, 0, 0, 0, 0,
			time.FixedZone("", int(r.timeZones[zone].rawOffset/1000)),
		).UnixMilli()
		return 0, nil
	case "get(I)I", "internalGet(I)I":
		field, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		zone := r.calendarTimeZone(instance)
		return uint32(ktfCalendarField(r.dates[instance], field, zone)), nil
	case "set(II)V":
		field, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.dates[instance] = ktfCalendarSetField(
			r.dates[instance],
			field,
			int32(value),
			r.calendarTimeZone(instance),
		)
		return 0, nil
	case "set(III)V":
		year, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		month, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		day, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		zone := r.calendarTimeZone(instance)
		moment := ktfCalendarMoment(r.dates[instance], zone)
		r.dates[instance] = time.Date(
			int(year), time.Month(month+1), int(day),
			moment.Hour(), moment.Minute(), moment.Second(),
			moment.Nanosecond(), moment.Location(),
		).UnixMilli()
		return 0, nil
	case "after(Ljava/lang/Object;)Z", "before(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		otherTime, ok := r.dates[other]
		if !ok {
			return 0, nil
		}
		if name == "after" && r.dates[instance] > otherTime ||
			name == "before" && r.dates[instance] < otherTime {
			return 1, nil
		}
		return 0, nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if otherTime, ok := r.dates[other]; ok &&
			otherTime == r.dates[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		value := uint64(r.dates[instance])
		return uint32(value ^ (value >> 32)), nil
	case "setTimeZone(Ljava/util/TimeZone;)V":
		zone, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if zone == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		r.calendarZones[instance] = zone
		return 0, nil
	case "complete()V", "computeFields()V", "computeTime()V", "initialize()V":
		// Field/time recomputation is implicit in the epoch representation.
		return 0, nil
	case "getFirstDayOfWeek()I", "getMinimalDaysInFirstWeek()I":
		return 1, nil
	case "isLenient()Z", "isSet(I)Z":
		return 1, nil
	case "getTimeZone()Ljava/util/TimeZone;":
		if zone := r.calendarZones[instance]; zone != 0 {
			return zone, nil
		}
		zone, valueErr := r.newKTFTimeZone(r.defaultKTFTimeZone())
		if valueErr == nil {
			r.calendarZones[instance] = zone
		}
		return zone, valueErr
	case "getMaximum(I)I", "getLeastMaximum(I)I":
		field, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return uint32(ktfCalendarMaximum(field, name == "getLeastMaximum")), nil
	case "getMinimum(I)I", "getGreatestMinimum(I)I":
		field, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		switch field {
		case 1, 3, 5, 6, 7, 8:
			return 1, nil
		}
		return 0, nil
	case "isLeapYear(I)Z":
		year, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 1, nil
		}
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

// Java Calendar field indices for the epoch-milliseconds calendar model.
func ktfCalendarField(ms int64, field uint32, zone ktfTimeZone) int32 {
	moment := ktfCalendarMoment(ms, zone)
	switch field {
	case 0: // ERA
		return 1
	case 1: // YEAR
		return int32(moment.Year())
	case 2: // MONTH
		return int32(moment.Month()) - 1
	case 3: // WEEK_OF_YEAR
		_, week := moment.ISOWeek()
		return int32(week)
	case 4, 8: // WEEK_OF_MONTH, DAY_OF_WEEK_IN_MONTH
		return int32((moment.Day()-1)/7 + 1)
	case 5: // DAY_OF_MONTH
		return int32(moment.Day())
	case 6: // DAY_OF_YEAR
		return int32(moment.YearDay())
	case 7: // DAY_OF_WEEK, SUNDAY=1
		return int32(moment.Weekday()) + 1
	case 9: // AM_PM
		if moment.Hour() >= 12 {
			return 1
		}
		return 0
	case 10: // HOUR
		return int32(moment.Hour() % 12)
	case 11: // HOUR_OF_DAY
		return int32(moment.Hour())
	case 12: // MINUTE
		return int32(moment.Minute())
	case 13: // SECOND
		return int32(moment.Second())
	case 14: // MILLISECOND
		return int32(moment.Nanosecond() / 1e6)
	case 15: // ZONE_OFFSET
		return zone.rawOffset
	case 16: // DST_OFFSET
		if zone.inDaylightTime(ms) {
			return 60 * 60 * 1000
		}
	}
	return 0
}

func ktfCalendarSetField(ms int64, field uint32, value int32, zone ktfTimeZone) int64 {
	moment := ktfCalendarMoment(ms, zone)
	year, month, day := moment.Date()
	hour, minute, second := moment.Clock()
	millis := moment.Nanosecond() / 1e6
	switch field {
	case 1:
		year = int(value)
	case 2:
		month = time.Month(value + 1)
	case 5:
		day = int(value)
	case 9: // AM_PM
		if value == 1 && hour < 12 {
			hour += 12
		} else if value == 0 && hour >= 12 {
			hour -= 12
		}
	case 10: // HOUR keeps the AM/PM half
		hour = hour/12*12 + int(value)
	case 11:
		hour = int(value)
	case 12:
		minute = int(value)
	case 13:
		second = int(value)
	case 14:
		millis = int(value)
	default:
		return ms
	}
	return time.Date(
		year,
		month,
		day,
		hour,
		minute,
		second,
		millis*1e6,
		moment.Location(),
	).UnixMilli()
}

func ktfCalendarMaximum(field uint32, least bool) int32 {
	maxima := map[uint32]int32{
		0: 1, 1: 9999, 2: 11, 3: 53, 4: 6, 5: 31, 6: 366, 7: 7,
		8: 6, 9: 1, 10: 11, 11: 23, 12: 59, 13: 59, 14: 999,
	}
	if least {
		for field, value := range map[uint32]int32{
			3: 52, 4: 4, 5: 28, 6: 365, 8: 4,
		} {
			maxima[field] = value
		}
	}
	return maxima[field]
}

func (r *Runtime) handleTimeZoneMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "getDefault()Ljava/util/TimeZone;":
		return r.newKTFTimeZone(r.defaultKTFTimeZone())
	case "getTimeZone(Ljava/lang/String;)Ljava/util/TimeZone;":
		id, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.newKTFTimeZone(r.parseKTFTimeZone(r.javaStringValue(id)))
	case "getAvailableIDs()[Ljava/lang/String;":
		return r.newKTFTimeZoneIDArray(r.availableKTFTimeZoneIDs())
	case "getAvailableIDs(I)[Ljava/lang/String;":
		offset, err := r.signedParameter(1)
		if err != nil {
			return 0, err
		}
		ids := make([]string, 0, 2)
		for _, id := range r.availableKTFTimeZoneIDs() {
			if r.parseKTFTimeZone(id).rawOffset == int32(offset) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return 0, nil
		}
		return r.newKTFTimeZoneIDArray(ids)
	}
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		r.timeZones[instance] = r.defaultKTFTimeZone()
		return 0, nil
	case "<init>(ILjava/lang/String;)V":
		offset, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		id, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.timeZones[instance] = ktfTimeZone{
			id: r.javaStringValue(id), rawOffset: int32(offset),
		}
		return 0, nil
	case "<init>(ILjava/lang/String;IIIIIIII)V":
		offset, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		id, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		values := [8]int32{}
		for index := range values {
			value, parameterErr := r.signedParameter(uint32(index + 4))
			if parameterErr != nil {
				return 0, parameterErr
			}
			values[index] = int32(value)
		}
		r.timeZones[instance] = ktfTimeZone{
			id: r.javaStringValue(id), rawOffset: int32(offset), daylight: true,
			startMonth: values[0], startWeek: values[1],
			startDayOfWeek: values[2], startTime: values[3],
			endMonth: values[4], endWeek: values[5],
			endDayOfWeek: values[6], endTime: values[7],
		}
		return 0, nil
	case "getID()Ljava/lang/String;", "toString()Ljava/lang/String;":
		return r.NewJavaString(r.ensureKTFTimeZone(instance).id)
	case "getRawOffset()I":
		return uint32(r.ensureKTFTimeZone(instance).rawOffset), nil
	case "getOffset(IIIIII)I":
		zone := r.ensureKTFTimeZone(instance)
		year, valueErr := r.signedParameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		month, valueErr := r.signedParameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		day, valueErr := r.signedParameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		millis, valueErr := r.signedParameter(7)
		if valueErr != nil {
			return 0, valueErr
		}
		location := time.FixedZone("", int(zone.rawOffset/1000))
		moment := time.Date(
			year, time.Month(month+1), day, 0, 0, 0, millis*1e6, location,
		)
		return uint32(zone.offsetAt(moment.UnixMilli())), nil
	case "useDaylightTime()Z":
		return boolWord(r.ensureKTFTimeZone(instance).daylight), nil
	case "inDaylightTime(Ljava/util/Date;)Z":
		date, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return boolWord(r.ensureKTFTimeZone(instance).inDaylightTime(r.dates[date])), nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if other == 0 {
			return 0, nil
		}
		left, right := r.ensureKTFTimeZone(instance), r.ensureKTFTimeZone(other)
		return boolWord(left == right), nil
	case "hashCode()I":
		zone := r.ensureKTFTimeZone(instance)
		return uint32(zone.rawOffset) ^ uint32(len(zone.id)<<16), nil
	case "setID(Ljava/lang/String;)V":
		id, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		zone := r.ensureKTFTimeZone(instance)
		zone.id = r.javaStringValue(id)
		r.timeZones[instance] = zone
		return 0, nil
	case "setRawOffset(I)V":
		offset, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		zone := r.ensureKTFTimeZone(instance)
		zone.rawOffset = int32(offset)
		r.timeZones[instance] = zone
		return 0, nil
	case "setStartYear(I)V":
		year, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		zone := r.ensureKTFTimeZone(instance)
		zone.startYear = int32(year)
		r.timeZones[instance] = zone
		return 0, nil
	case "initialize()V":
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) defaultKTFTimeZone() ktfTimeZone {
	minutes := r.Services.Device.Config().TimezoneMins
	return ktfTimeZone{
		id: ktfFixedTimeZoneID(minutes), rawOffset: minutes * 60 * 1000,
	}
}

func ktfFixedTimeZoneID(minutes int32) string {
	if minutes == 0 {
		return "GMT"
	}
	sign := '+'
	if minutes < 0 {
		sign = '-'
		minutes = -minutes
	}
	return fmt.Sprintf("GMT%c%02d:%02d", sign, minutes/60, minutes%60)
}

func (r *Runtime) parseKTFTimeZone(id string) ktfTimeZone {
	id = strings.TrimSpace(id)
	if id == "GMT" || id == "UTC" {
		return ktfTimeZone{id: id}
	}
	if len(id) == 9 && strings.HasPrefix(id, "GMT") &&
		(id[3] == '+' || id[3] == '-') && id[6] == ':' {
		hours, hourErr := strconv.Atoi(id[4:6])
		minutes, minuteErr := strconv.Atoi(id[7:9])
		if hourErr == nil && minuteErr == nil && hours <= 23 && minutes <= 59 {
			offset := int32((hours*60 + minutes) * 60 * 1000)
			if id[3] == '-' {
				offset = -offset
			}
			return ktfTimeZone{id: id, rawOffset: offset}
		}
	}
	return ktfTimeZone{id: "GMT"}
}

func (r *Runtime) availableKTFTimeZoneIDs() []string {
	defaultID := r.defaultKTFTimeZone().id
	if defaultID == "GMT" {
		return []string{"GMT"}
	}
	return []string{"GMT", defaultID}
}

func (r *Runtime) newKTFTimeZone(zone ktfTimeZone) (uint32, error) {
	object, err := r.NewHostJavaObject("java/util/SimpleTimeZone")
	if err == nil {
		r.timeZones[object] = zone
	}
	return object, err
}

func (r *Runtime) newKTFTimeZoneIDArray(ids []string) (uint32, error) {
	values := make([]uint32, len(ids))
	for index, id := range ids {
		value, err := r.NewJavaString(id)
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	return r.newJavaReferenceArray("[Ljava/lang/String;", values)
}

func (r *Runtime) ensureKTFTimeZone(instance uint32) ktfTimeZone {
	if zone, ok := r.timeZones[instance]; ok {
		return zone
	}
	zone := r.defaultKTFTimeZone()
	r.timeZones[instance] = zone
	return zone
}

func (r *Runtime) calendarTimeZone(calendar uint32) ktfTimeZone {
	if zone := r.calendarZones[calendar]; zone != 0 {
		return r.ensureKTFTimeZone(zone)
	}
	return r.defaultKTFTimeZone()
}

func ktfCalendarMoment(ms int64, zone ktfTimeZone) time.Time {
	offset := zone.offsetAt(ms)
	return time.UnixMilli(ms).In(time.FixedZone(zone.id, int(offset/1000)))
}

func (zone ktfTimeZone) offsetAt(ms int64) int32 {
	if zone.inDaylightTime(ms) {
		return zone.rawOffset + 60*60*1000
	}
	return zone.rawOffset
}

func (zone ktfTimeZone) inDaylightTime(ms int64) bool {
	if !zone.daylight {
		return false
	}
	location := time.FixedZone(zone.id, int(zone.rawOffset/1000))
	moment := time.UnixMilli(ms).In(location)
	if zone.startYear != 0 && int32(moment.Year()) < zone.startYear {
		return false
	}
	start := ktfTimeZoneRuleDate(
		moment.Year(), zone.startMonth, zone.startWeek,
		zone.startDayOfWeek, zone.startTime, location,
	)
	end := ktfTimeZoneRuleDate(
		moment.Year(), zone.endMonth, zone.endWeek,
		zone.endDayOfWeek, zone.endTime, location,
	)
	if start.Before(end) {
		return !moment.Before(start) && moment.Before(end)
	}
	return !moment.Before(start) || moment.Before(end)
}

func ktfTimeZoneRuleDate(
	year int, month, week, dayOfWeek, millis int32, location *time.Location,
) time.Time {
	first := time.Date(year, time.Month(month+1), 1, 0, 0, 0, 0, location)
	wanted := time.Weekday((dayOfWeek + 6) % 7)
	day := 1
	if week >= 0 {
		day += (int(wanted)-int(first.Weekday())+7)%7 + (int(week)-1)*7
	} else {
		last := first.AddDate(0, 1, -1)
		day = last.Day() - (int(last.Weekday())-int(wanted)+7)%7 + (int(week)+1)*7
	}
	return time.Date(
		year, time.Month(month+1), day, 0, 0, 0, int(millis)*1e6, location,
	)
}
