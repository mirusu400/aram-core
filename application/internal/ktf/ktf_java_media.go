package ktf

import (
	"errors"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strings"
	"time"

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
		if mediaType, typeErr := r.parameter(2); typeErr == nil {
			// The declared media type string backs getType().
			r.lwcComponent(instance).text = mediaType
		}
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
	case "getType()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if mediaType := r.lwcComponent(instance).text; mediaType != 0 {
			return mediaType, nil
		}
		return r.NewJavaString("")
	case "setPosition(I)Z", "playStart(Z)Z":
		return 1, nil
	case "playUpdate(II)Z", "recordStart()Z",
		"getPlayerID(Ljava/lang/String;)I",
		"mediaFreeze()I", "mediaReadData()I", "mediaWriteData()I",
		"atomicGetUpdate(I)V", "atomicPutUpdate(I)V",
		"control(IILjava/lang/Object;Ljava/lang/Object;)I":
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
		clip := r.clips[instance]
		if clip == nil {
			clip = &ktfClip{}
			r.clips[instance] = clip
		}
		clip.playing = name == "play" || name == "resume"
		serviceID, serviceErr := r.ensureKTFClipService(instance)
		if serviceErr != nil {
			return 0, serviceErr
		}
		if clip.playing {
			plays := int32(1)
			if name == "play" {
				if repeat, valueErr := r.parameter(2); valueErr == nil &&
					repeat != 0 {
					plays = -1
				}
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
	case "getInstance()Ljava/util/Calendar;",
		"getInstance(Ljava/util/TimeZone;)Ljava/util/Calendar;":
		calendar, valueErr := r.NewHostJavaObject("java/util/Calendar")
		if valueErr == nil {
			r.dates[calendar] = int64(r.TickMS)
		}
		return calendar, valueErr
	case "<init>()V", "<init>(Ljava/util/TimeZone;)V":
		r.dates[instance] = int64(r.TickMS)
		return 0, nil
	case "get(I)I", "internalGet(I)I":
		field, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return uint32(ktfCalendarField(r.dates[instance], field)), nil
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
		)
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
	case "complete()V", "computeFields()V", "computeTime()V",
		"initialize()V", "setTimeZone(Ljava/util/TimeZone;)V":
		// The host models calendar state as epoch milliseconds only, so
		// field/time recomputation is implicit and the zone is fixed GMT.
		return 0, nil
	case "getFirstDayOfWeek()I", "getMinimalDaysInFirstWeek()I":
		return 1, nil
	case "isLenient()Z", "isSet(I)Z":
		return 1, nil
	case "getTimeZone()Ljava/util/TimeZone;":
		return r.NewHostJavaObject("java/util/TimeZone")
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
func ktfCalendarField(ms int64, field uint32) int32 {
	moment := time.UnixMilli(ms).UTC()
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
	}
	return 0
}

func ktfCalendarSetField(ms int64, field uint32, value int32) int64 {
	moment := time.UnixMilli(ms).UTC()
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
		time.UTC,
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
	// Every zone the host models is GMT with no daylight saving.
	switch name + descriptor {
	case "getDefault()Ljava/util/TimeZone;",
		"getTimeZone(Ljava/lang/String;)Ljava/util/TimeZone;":
		return r.NewHostJavaObject("java/util/TimeZone")
	case "getID()Ljava/lang/String;", "toString()Ljava/lang/String;":
		return r.NewJavaString("GMT")
	case "getAvailableIDs()[Ljava/lang/String;":
		value, err := r.NewJavaString("GMT")
		if err != nil {
			return 0, err
		}
		return r.newJavaReferenceArray(
			"[Ljava/lang/String;",
			[]uint32{value},
		)
	case "getRawOffset()I", "getOffset(IIIIII)I", "useDaylightTime()Z",
		"inDaylightTime(Ljava/util/Date;)Z", "hashCode()I":
		return 0, nil
	case "equals(Ljava/lang/Object;)Z":
		// Every zone the host hands out is the same GMT model.
		return 1, nil
	case "<init>()V", "<init>(II)V",
		"<init>(ILjava/lang/String;)V",
		"setID(Ljava/lang/String;)V", "setRawOffset(I)V",
		"setStartYear(I)V", "initialize()V":
		return 0, nil
	default:
		return 0, nil
	}
}
