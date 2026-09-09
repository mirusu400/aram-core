package skvm

import (
	"testing"
	"time"
)

func TestCLDCVectorCapacityAndEnumeration(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	vector := vm.NewObject("java/util/Vector", nil)
	invokeTestNative(t, vm, "java/util/Vector", "<init>", "(I)V", vector, IntValue(1))
	for _, text := range []string{"a", "b"} {
		invokeTestNative(t, vm, "java/util/Vector", "addElement", "(Ljava/lang/Object;)V", vector, ReferenceValue(vm.NewString(text)))
	}
	capacity := invokeTestNative(t, vm, "java/util/Vector", "capacity", "()I", vector)
	if got := mustInt(t, capacity); got < 2 {
		t.Fatalf("Vector.capacity() = %d", got)
	}
	enumeration := invokeTestNative(t, vm, "java/util/Vector", "elements", "()Ljava/util/Enumeration;", vector)
	reference, err := enumeration.Reference()
	check(t, err)
	first := invokeTestNative(t, vm, "java/util/Enumeration", "nextElement", "()Ljava/lang/Object;", reference)
	firstReference, err := first.Reference()
	check(t, err)
	text, err := vm.String(firstReference)
	check(t, err)
	if text != "a" {
		t.Fatalf("first vector element = %q", text)
	}
}

func TestCLDCCalendarUsesConfiguredFixedOffset(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	zone := vm.NewObject("java/util/TimeZone", "GMT+09:00")
	calendar := vm.NewObject("java/util/Calendar", &dateState{millis: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()})
	object, _ := vm.Object(calendar)
	object.Fields[calendarTimeZoneField] = ReferenceValue(zone)
	hour := invokeTestNative(t, vm, "java/util/Calendar", "get", "(I)I", calendar, IntValue(11))
	if got := mustInt(t, hour); got != 9 {
		t.Fatalf("Calendar.HOUR_OF_DAY = %d", got)
	}
	offset := invokeTestNative(t, vm, "java/util/TimeZone", "getRawOffset", "()I", zone)
	if got := mustInt(t, offset); got != 9*60*60*1000 {
		t.Fatalf("TimeZone.getRawOffset() = %d", got)
	}
	defaultZone := mustReference(t, invokeTestNative(t, vm, "java/util/TimeZone", "getDefault", "()Ljava/util/TimeZone;", 0))
	if got := mustStringValue(t, vm, invokeTestNative(t, vm, "java/util/TimeZone", "getID", "()Ljava/lang/String;", defaultZone)); got != "GMT+09:00" {
		t.Fatalf("default timezone = %q", got)
	}
}
