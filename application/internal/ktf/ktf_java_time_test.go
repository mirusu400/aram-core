package ktf

import (
	"bytes"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFTimeZoneUsesDeviceDefaultAndParsesGMTOffsets(t *testing.T) {
	runtime := newTestRuntime(t)
	zone, err := runtime.handleTimeZoneMethod("getDefault", "()Ljava/util/TimeZone;")
	check(t, err)
	if got := runtime.timeZones[zone]; got.id != "GMT+09:00" ||
		got.rawOffset != 9*60*60*1000 {
		t.Fatalf("default time zone = %+v", got)
	}

	id := newJavaString(t, runtime, "GMT-05:30")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, id))
	parsed, err := runtime.handleTimeZoneMethod(
		"getTimeZone", "(Ljava/lang/String;)Ljava/util/TimeZone;",
	)
	check(t, err)
	if got := runtime.timeZones[parsed]; got.id != "GMT-05:30" ||
		got.rawOffset != -(5*60+30)*60*1000 {
		t.Fatalf("parsed time zone = %+v", got)
	}
}

func TestKTFCalendarFieldsRespectAssignedTimeZone(t *testing.T) {
	runtime := newTestRuntime(t)
	calendar := newHostObject(t, runtime, "java/util/Calendar")
	zone, err := runtime.newKTFTimeZone(ktfTimeZone{
		id: "GMT+09:00", rawOffset: 9 * 60 * 60 * 1000,
	})
	check(t, err)
	runtime.dates[calendar] = time.Date(2024, 1, 2, 0, 30, 0, 0, time.UTC).UnixMilli()
	runtime.calendarZones[calendar] = zone
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, calendar))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 11))
	hour, err := runtime.handleCalendarMethod("get", "(I)I")
	check(t, err)
	if hour != 9 {
		t.Fatalf("GMT+09 calendar hour = %d, want 9", hour)
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 15))
	offset, err := runtime.handleCalendarMethod("get", "(I)I")
	check(t, err)
	if int32(offset) != 9*60*60*1000 {
		t.Fatalf("calendar zone offset = %d", int32(offset))
	}

	gmt, err := runtime.newKTFTimeZone(ktfTimeZone{id: "GMT"})
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, gmt))
	_, err = runtime.handleCalendarMethod(
		"setTimeZone", "(Ljava/util/TimeZone;)V",
	)
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 11))
	hour, err = runtime.handleCalendarMethod("get", "(I)I")
	check(t, err)
	if hour != 0 {
		t.Fatalf("GMT calendar hour = %d, want 0", hour)
	}
}

func TestKTFSimpleTimeZoneAppliesDaylightRule(t *testing.T) {
	runtime := newTestRuntime(t)
	zone := newHostObject(t, runtime, "java/util/SimpleTimeZone")
	id := newJavaString(t, runtime, "Test/DST")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, zone))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, 60*60*1000))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR3, id))
	stack := allocWords(t, runtime, 8)
	check(t, runtime.writeWords(stack, []uint32{
		2, 2, 1, 2 * 60 * 60 * 1000,
		10, 1, 1, 2 * 60 * 60 * 1000,
	}))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, stack))
	_, err := runtime.handleTimeZoneMethod(
		"<init>", "(ILjava/lang/String;IIIIIIII)V",
	)
	check(t, err)
	state := runtime.timeZones[zone]
	if !state.daylight {
		t.Fatal("SimpleTimeZone did not retain daylight rules")
	}
	jan := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	jul := time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	if got := state.offsetAt(jan); got != 60*60*1000 {
		t.Fatalf("winter offset = %d", got)
	}
	if got := state.offsetAt(jul); got != 2*60*60*1000 {
		t.Fatalf("summer offset = %d", got)
	}
}

func TestKTFTimeZoneAvailableIDsFilterByOffset(t *testing.T) {
	runtime := newTestRuntime(t)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 9*60*60*1000))
	array, err := runtime.handleTimeZoneMethod(
		"getAvailableIDs", "(I)[Ljava/lang/String;",
	)
	check(t, err)
	if array == 0 {
		t.Fatal("default-offset time zone list is null")
	}
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, 1234))
	array, err = runtime.handleTimeZoneMethod(
		"getAvailableIDs", "(I)[Ljava/lang/String;",
	)
	check(t, err)
	if array != 0 {
		t.Fatalf("unsupported-offset time zone list = 0x%08x", array)
	}
}

func TestKTFTimeZoneStateSurvivesSaveRestore(t *testing.T) {
	runtime := newTestRuntime(t)
	zone, err := runtime.newKTFTimeZone(ktfTimeZone{
		id: "Test/DST", rawOffset: 60 * 60 * 1000, daylight: true,
		startYear: 2000, startMonth: 2, startWeek: 2, startDayOfWeek: 1,
		startTime: 2 * 60 * 60 * 1000, endMonth: 10, endWeek: 1,
		endDayOfWeek: 1, endTime: 2 * 60 * 60 * 1000,
	})
	check(t, err)
	calendar := newHostObject(t, runtime, "java/util/Calendar")
	runtime.calendarZones[calendar] = zone

	var buffer bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&buffer)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(buffer.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	runtime.timeZones = nil
	runtime.calendarZones = nil
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	if runtime.calendarZones[calendar] != zone ||
		runtime.timeZones[zone].id != "Test/DST" ||
		!runtime.timeZones[zone].daylight {
		t.Fatalf("restored zone/calendar = %+v/%v", runtime.timeZones[zone], runtime.calendarZones)
	}
}
