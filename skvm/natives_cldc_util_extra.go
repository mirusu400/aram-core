package skvm

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	vectorCapacityField          = "\x00aram-vector-capacity"
	vectorCapacityIncrementField = "\x00aram-vector-increment"
	calendarTimeZoneField        = "\x00aram-calendar-time-zone"
)

func (vm *VM) installCLDCUtilExtras() {
	vm.installCLDCVectorExtras()
	vm.installCLDCHashtableExtras()
	vm.installCLDCDateExtras()
	vm.installCLDCCalendarExtras()
	vm.installCLDCTimeZoneExtras()
}

func (vm *VM) initializeVector(receiver uint32, capacity, increment int32) error {
	if capacity < 0 {
		return vm.newThrowable("java/lang/IllegalArgumentException", "negative capacity")
	}
	if err := vm.setNative(receiver, &vectorState{values: make([]uint32, 0, capacity)}); err != nil {
		return err
	}
	object, _ := vm.Object(receiver)
	object.Fields[vectorCapacityField] = IntValue(capacity)
	object.Fields[vectorCapacityIncrementField] = IntValue(increment)
	return nil
}

func (vm *VM) vectorCapacity(reference uint32, size int32) int32 {
	object, _ := vm.Object(reference)
	capacity, _ := object.Fields[vectorCapacityField].Int()
	if capacity < size {
		increment, _ := object.Fields[vectorCapacityIncrementField].Int()
		for capacity < size {
			if increment > 0 {
				capacity += increment
			} else if capacity == 0 {
				capacity = max(int32(1), size)
			} else {
				capacity *= 2
			}
		}
		object.Fields[vectorCapacityField] = IntValue(capacity)
	}
	return capacity
}

func (vm *VM) installCLDCVectorExtras() {
	vm.RegisterNative("java/util/Vector", "<init>", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return Value{}, false, vm.initializeVector(receiver, 10, 0)
	})
	vm.RegisterNative("java/util/Vector", "<init>", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		capacity, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.initializeVector(receiver, capacity, 0)
	})
	vm.RegisterNative("java/util/Vector", "<init>", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		capacity, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		increment, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.initializeVector(receiver, capacity, increment)
	})
	vm.RegisterNative("java/util/Vector", "capacity", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(vm.vectorCapacity(receiver, int32(len(state.values)))), true, nil
	})
	vm.RegisterNative("java/util/Vector", "ensureCapacity", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		minimum, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		vm.vectorCapacity(receiver, minimum)
		return Value{}, false, nil
	})
	indexOf := func(reverse, withStart bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			item, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			start := int32(0)
			if reverse {
				start = int32(len(state.values) - 1)
			}
			if withStart {
				start, err = intArgument(args, 1)
				if err != nil {
					return Value{}, false, err
				}
			}
			if reverse {
				if start >= int32(len(state.values)) {
					return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
				}
				for index := start; index >= 0; index-- {
					if vm.referencesEqual(state.values[index], item) {
						return IntValue(index), true, nil
					}
				}
			} else {
				if start < 0 {
					return Value{}, false, vm.newThrowable("java/lang/IndexOutOfBoundsException", "")
				}
				for index := start; int(index) < len(state.values); index++ {
					if vm.referencesEqual(state.values[index], item) {
						return IntValue(index), true, nil
					}
				}
			}
			return IntValue(-1), true, nil
		}
	}
	vm.RegisterNative("java/util/Vector", "indexOf", "(Ljava/lang/Object;)I", indexOf(false, false))
	vm.RegisterNative("java/util/Vector", "indexOf", "(Ljava/lang/Object;I)I", indexOf(false, true))
	vm.RegisterNative("java/util/Vector", "lastIndexOf", "(Ljava/lang/Object;)I", indexOf(true, false))
	vm.RegisterNative("java/util/Vector", "lastIndexOf", "(Ljava/lang/Object;I)I", indexOf(true, true))
	vm.RegisterNative("java/util/Vector", "insertElementAt", "(Ljava/lang/Object;I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		item, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		index, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if index < 0 || int(index) > len(state.values) {
			return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
		}
		state.values = append(state.values, 0)
		copy(state.values[index+1:], state.values[index:])
		state.values[index] = item
		vm.vectorCapacity(receiver, int32(len(state.values)))
		return Value{}, false, nil
	})
	for _, method := range []struct {
		name string
		last bool
	}{{"firstElement", false}, {"lastElement", true}, {"peek", true}} {
		method := method
		class := "java/util/Vector"
		if method.name == "peek" {
			class = "java/util/Stack"
		}
		vm.RegisterNative(class, method.name, "()Ljava/lang/Object;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.vector(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if len(state.values) == 0 {
				return Value{}, false, vm.newThrowable("java/util/EmptyStackException", "")
			}
			index := 0
			if method.last {
				index = len(state.values) - 1
			}
			return ReferenceValue(state.values[index]), true, nil
		})
	}
	vm.RegisterNative("java/util/Vector", "setSize", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		size, err := intArgument(args, 0)
		if err != nil || size < 0 {
			if err != nil {
				return Value{}, false, err
			}
			return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
		}
		if int(size) < len(state.values) {
			state.values = state.values[:size]
		} else {
			state.values = append(state.values, make([]uint32, int(size)-len(state.values))...)
		}
		vm.vectorCapacity(receiver, size)
		return Value{}, false, nil
	})
	vm.RegisterNative("java/util/Vector", "trimToSize", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		object, _ := vm.Object(receiver)
		object.Fields[vectorCapacityField] = IntValue(int32(len(state.values)))
		return Value{}, false, nil
	})
	vm.RegisterNative("java/util/Vector", "copyInto", "([Ljava/lang/Object;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		destination, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		object, ok := vm.Object(destination)
		if !ok || object.Array == nil || !strings.HasPrefix(object.Array.Descriptor, "[L") {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "invalid object array")
		}
		if len(object.Array.Elements) < len(state.values) {
			return Value{}, false, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
		}
		for index, item := range state.values {
			object.Array.Elements[index] = ReferenceValue(item)
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/util/Vector", "elements", "()Ljava/util/Enumeration;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewObject("java/util/Enumeration", &vectorState{values: append([]uint32(nil), state.values...)})), true, nil
	})
	vm.RegisterNative("java/util/Vector", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		parts := make([]string, len(state.values))
		for index, item := range state.values {
			parts[index] = vm.objectString(item)
		}
		return ReferenceValue(vm.NewString("[" + strings.Join(parts, ", ") + "]")), true, nil
	})
	vm.RegisterNative("java/util/Stack", "search", "(Ljava/lang/Object;)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.vector(receiver)
		if err != nil {
			return Value{}, false, err
		}
		item, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		for index := len(state.values) - 1; index >= 0; index-- {
			if vm.referencesEqual(state.values[index], item) {
				return IntValue(int32(len(state.values) - index)), true, nil
			}
		}
		return IntValue(-1), true, nil
	})
}

func (vm *VM) installCLDCHashtableExtras() {
	vm.RegisterNative("java/util/Hashtable", "<init>", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		capacity, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if capacity < 0 {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "negative capacity")
		}
		return Value{}, false, vm.setNative(receiver, &hashtableState{values: make(map[string]uint32, capacity), keys: make(map[string]uint32, capacity)})
	})
	vm.RegisterNative("java/util/Hashtable", "size", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(len(state.values))), true, nil
	})
	vm.RegisterNative("java/util/Hashtable", "isEmpty", "()Z", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return boolValue(len(state.values) == 0), true, nil
	})
	vm.RegisterNative("java/util/Hashtable", "containsKey", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		key, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if key == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "null key")
		}
		_, ok := state.values[vm.objectKey(key)]
		return boolValue(ok), true, nil
	})
	vm.RegisterNative("java/util/Hashtable", "contains", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		value, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if value == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "null value")
		}
		for _, current := range state.values {
			if vm.referencesEqual(current, value) {
				return boolValue(true), true, nil
			}
		}
		return boolValue(false), true, nil
	})
	vm.RegisterNative("java/util/Hashtable", "put", "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		key, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		value, err := referenceArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		if key == 0 || value == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "Hashtable rejects null")
		}
		identity := vm.objectKey(key)
		previous := state.values[identity]
		state.values[identity], state.keys[identity] = value, key
		return ReferenceValue(previous), true, nil
	})
	vm.RegisterNative("java/util/Hashtable", "elements", "()Ljava/util/Enumeration;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		identities := make([]string, 0, len(state.values))
		for identity := range state.values {
			identities = append(identities, identity)
		}
		sort.Strings(identities)
		values := make([]uint32, len(identities))
		for index, identity := range identities {
			values[index] = state.values[identity]
		}
		return ReferenceValue(vm.NewObject("java/util/Enumeration", &vectorState{values: values})), true, nil
	})
	vm.RegisterNative("java/util/Hashtable", "rehash", "()V", nativeVoid)
	vm.RegisterNative("java/util/Hashtable", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.hashtable(receiver)
		if err != nil {
			return Value{}, false, err
		}
		identities := make([]string, 0, len(state.values))
		for identity := range state.values {
			identities = append(identities, identity)
		}
		sort.Strings(identities)
		parts := make([]string, 0, len(identities))
		for _, identity := range identities {
			parts = append(parts, vm.objectString(state.keys[identity])+"="+vm.objectString(state.values[identity]))
		}
		return ReferenceValue(vm.NewString("{" + strings.Join(parts, ", ") + "}")), true, nil
	})
}

func (vm *VM) installCLDCDateExtras() {
	vm.RegisterNative("java/util/Date", "equals", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		left, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		right, err := vm.date(other)
		return boolValue(err == nil && left.millis == right.millis), true, nil
	})
	vm.RegisterNative("java/util/Date", "hashCode", "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(int32(state.millis ^ (state.millis >> 32))), true, nil
	})
	vm.RegisterNative("java/util/Date", "toString", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		offset := int(vm.services.Device.Config().TimezoneMins) * 60
		identifier := formatGMTOffset(offset)
		location := time.FixedZone(identifier, offset)
		return ReferenceValue(vm.NewString(time.UnixMilli(state.millis).In(location).Format("Mon Jan 02 15:04:05 MST 2006"))), true, nil
	})
}

func (vm *VM) defaultTimeZone() uint32 {
	return vm.NewObject("java/util/TimeZone", formatGMTOffset(int(vm.services.Device.Config().TimezoneMins)*60))
}

func formatGMTOffset(seconds int) string {
	if seconds == 0 {
		return "GMT"
	}
	sign := '+'
	if seconds < 0 {
		sign = '-'
		seconds = -seconds
	}
	return fmt.Sprintf("GMT%c%02d:%02d", sign, seconds/3600, seconds%3600/60)
}

func (vm *VM) calendarZone(reference uint32) uint32 {
	object, _ := vm.Object(reference)
	value, ok := object.Fields[calendarTimeZoneField]
	if ok {
		zone, err := value.Reference()
		if err == nil && zone != 0 {
			return zone
		}
	}
	zone := vm.defaultTimeZone()
	object.Fields[calendarTimeZoneField] = ReferenceValue(zone)
	return zone
}

func (vm *VM) timeZoneLocation(reference uint32) (*time.Location, string, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Class != "java/util/TimeZone" {
		return nil, "", fmt.Errorf("invalid TimeZone")
	}
	identifier, ok := object.Native.(string)
	if !ok || identifier == "" {
		identifier = "GMT"
	}
	offset, normalized, ok := parseGMTOffset(identifier)
	if !ok {
		offset, normalized = 0, "GMT"
	}
	return time.FixedZone(normalized, offset), normalized, nil
}

func parseGMTOffset(identifier string) (int, string, bool) {
	upper := strings.ToUpper(strings.TrimSpace(identifier))
	if upper == "GMT" || upper == "UTC" {
		return 0, "GMT", true
	}
	if upper == "KST" {
		return 9 * 3600, "GMT+09:00", true
	}
	if !strings.HasPrefix(upper, "GMT+") && !strings.HasPrefix(upper, "GMT-") {
		return 0, "", false
	}
	sign := 1
	if upper[3] == '-' {
		sign = -1
	}
	value := strings.ReplaceAll(upper[4:], ":", "")
	if len(value) == 1 || len(value) == 2 {
		value += "00"
	}
	if len(value) != 4 {
		return 0, "", false
	}
	hour, hourErr := strconv.Atoi(value[:2])
	minute, minuteErr := strconv.Atoi(value[2:])
	if hourErr != nil || minuteErr != nil || hour > 23 || minute > 59 {
		return 0, "", false
	}
	offset := sign * (hour*3600 + minute*60)
	return offset, fmt.Sprintf("GMT%c%02d:%02d", upper[3], hour, minute), true
}

func (vm *VM) installCLDCCalendarExtras() {
	vm.RegisterNative("java/util/Calendar", "getInstance", "()Ljava/util/Calendar;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		calendar := vm.NewObject("java/util/Calendar", &dateState{millis: vm.services.Clock.WallMillis()})
		object, _ := vm.Object(calendar)
		object.Fields[calendarTimeZoneField] = ReferenceValue(vm.defaultTimeZone())
		return ReferenceValue(calendar), true, nil
	})
	vm.RegisterNative("java/util/Calendar", "getInstance", "(Ljava/util/TimeZone;)Ljava/util/Calendar;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		zone, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if _, _, err = vm.timeZoneLocation(zone); err != nil {
			return Value{}, false, err
		}
		calendar := vm.NewObject("java/util/Calendar", &dateState{millis: vm.services.Clock.WallMillis()})
		object, _ := vm.Object(calendar)
		object.Fields[calendarTimeZoneField] = ReferenceValue(zone)
		return ReferenceValue(calendar), true, nil
	})
	vm.RegisterNative("java/util/Calendar", "<init>", "(Ljava/util/TimeZone;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		zone, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if _, _, err = vm.timeZoneLocation(zone); err != nil {
			return Value{}, false, err
		}
		if err = vm.setNative(receiver, &dateState{millis: vm.services.Clock.WallMillis()}); err != nil {
			return Value{}, false, err
		}
		object, _ := vm.Object(receiver)
		object.Fields[calendarTimeZoneField] = ReferenceValue(zone)
		return Value{}, false, nil
	})
	vm.RegisterNative("java/util/Calendar", "getTime", "()Ljava/util/Date;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewObject("java/util/Date", &dateState{millis: state.millis})), true, nil
	})
	vm.RegisterNative("java/util/Calendar", "getTimeInMillis", "()J", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return LongValue(state.millis), true, nil
	})
	vm.RegisterNative("java/util/Calendar", "setTimeInMillis", "(J)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		state, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		state.millis, err = args[0].Long()
		return Value{}, false, err
	})
	vm.RegisterNative("java/util/Calendar", "getTimeZone", "()Ljava/util/TimeZone;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.calendarZone(receiver)), true, nil
	})
	vm.RegisterNative("java/util/Calendar", "setTimeZone", "(Ljava/util/TimeZone;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		zone, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if _, _, err = vm.timeZoneLocation(zone); err != nil {
			return Value{}, false, err
		}
		object, _ := vm.Object(receiver)
		object.Fields[calendarTimeZoneField] = ReferenceValue(zone)
		return Value{}, false, nil
	})
	compare := func(after bool) NativeFunc {
		return func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			left, err := vm.date(receiver)
			if err != nil {
				return Value{}, false, err
			}
			other, err := referenceArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			right, err := vm.date(other)
			if err != nil {
				return boolValue(false), true, nil
			}
			if after {
				return boolValue(left.millis > right.millis), true, nil
			}
			return boolValue(left.millis < right.millis), true, nil
		}
	}
	vm.RegisterNative("java/util/Calendar", "after", "(Ljava/lang/Object;)Z", compare(true))
	vm.RegisterNative("java/util/Calendar", "before", "(Ljava/lang/Object;)Z", compare(false))
	vm.RegisterNative("java/util/Calendar", "equals", "(Ljava/lang/Object;)Z", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		left, err := vm.date(receiver)
		if err != nil {
			return Value{}, false, err
		}
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		right, err := vm.date(other)
		if err != nil || left.millis != right.millis {
			return boolValue(false), true, nil
		}
		_, leftID, _ := vm.timeZoneLocation(vm.calendarZone(receiver))
		_, rightID, _ := vm.timeZoneLocation(vm.calendarZone(other))
		return boolValue(leftID == rightID), true, nil
	})
	vm.RegisterNative("java/util/Calendar", "set", "(II)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		field, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		value, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		return Value{}, false, vm.calendarSetField(receiver, field, value)
	})
	vm.RegisterNative("java/util/Calendar", "set", "(III)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		for index, field := range []int32{1, 2, 5} {
			value, err := intArgument(args, index)
			if err != nil {
				return Value{}, false, err
			}
			if err = vm.calendarSetField(receiver, field, value); err != nil {
				return Value{}, false, err
			}
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("java/util/Calendar", "get", "(I)I", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		field, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		value, err := vm.calendarField(receiver, field)
		return IntValue(value), err == nil, err
	})
}

func (vm *VM) calendarTime(reference uint32) (time.Time, error) {
	state, err := vm.date(reference)
	if err != nil {
		return time.Time{}, err
	}
	location, _, err := vm.timeZoneLocation(vm.calendarZone(reference))
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(state.millis).In(location), nil
}

func (vm *VM) calendarField(reference uint32, field int32) (int32, error) {
	value, err := vm.calendarTime(reference)
	if err != nil {
		return 0, err
	}
	switch field {
	case 0:
		return 1, nil
	case 1:
		return int32(value.Year()), nil
	case 2:
		return int32(value.Month()) - 1, nil
	case 3:
		_, week := value.ISOWeek()
		return int32(week), nil
	case 4, 8:
		return int32((value.Day()-1)/7 + 1), nil
	case 5:
		return int32(value.Day()), nil
	case 6:
		return int32(value.YearDay()), nil
	case 7:
		return int32(value.Weekday()) + 1, nil
	case 9:
		return int32(value.Hour() / 12), nil
	case 10:
		return int32(value.Hour() % 12), nil
	case 11:
		return int32(value.Hour()), nil
	case 12:
		return int32(value.Minute()), nil
	case 13:
		return int32(value.Second()), nil
	case 14:
		return int32(value.Nanosecond() / int(time.Millisecond)), nil
	default:
		return 0, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "calendar field")
	}
}

func (vm *VM) calendarSetField(reference uint32, field, set int32) error {
	current, err := vm.calendarTime(reference)
	if err != nil {
		return err
	}
	year, month, day := current.Year(), current.Month(), current.Day()
	hour, minute, second, nanos := current.Hour(), current.Minute(), current.Second(), current.Nanosecond()
	switch field {
	case 1:
		year = int(set)
	case 2:
		month = time.Month(set + 1)
	case 5:
		day = int(set)
	case 9:
		hour = hour%12 + int(set)*12
	case 10:
		hour = hour/12*12 + int(set)
	case 11:
		hour = int(set)
	case 12:
		minute = int(set)
	case 13:
		second = int(set)
	case 14:
		nanos = int(set) * int(time.Millisecond)
	default:
		return vm.newThrowable("java/lang/IllegalArgumentException", "unsupported calendar field")
	}
	state, _ := vm.date(reference)
	state.millis = time.Date(year, month, day, hour, minute, second, nanos, current.Location()).UnixMilli()
	return nil
}

func (vm *VM) installCLDCTimeZoneExtras() {
	vm.RegisterNative("java/util/TimeZone", "getAvailableIDs", "()[Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		identifiers := []string{"GMT", "GMT+09:00", formatGMTOffset(int(vm.services.Device.Config().TimezoneMins) * 60)}
		seen := make(map[string]struct{})
		values := make([]Value, 0, len(identifiers))
		for _, identifier := range identifiers {
			if _, ok := seen[identifier]; ok {
				continue
			}
			seen[identifier] = struct{}{}
			values = append(values, ReferenceValue(vm.NewString(identifier)))
		}
		return ReferenceValue(vm.newArray("[Ljava/lang/String;", values)), true, nil
	})
	vm.RegisterNative("java/util/TimeZone", "getAvailableIDs", "(I)[Ljava/lang/String;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		offset, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		var values []Value
		if offset == 0 {
			values = append(values, ReferenceValue(vm.NewString("GMT")))
		} else if offset == 9*60*60*1000 {
			values = append(values, ReferenceValue(vm.NewString("GMT+09:00")))
		}
		return ReferenceValue(vm.newArray("[Ljava/lang/String;", values)), true, nil
	})
	vm.RegisterNative("java/util/TimeZone", "getDefault", "()Ljava/util/TimeZone;", func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
		return ReferenceValue(vm.defaultTimeZone()), true, nil
	})
	vm.RegisterNative("java/util/TimeZone", "getTimeZone", "(Ljava/lang/String;)Ljava/util/TimeZone;", func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
		identifier, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		_, normalized, ok := parseGMTOffset(identifier)
		if !ok {
			normalized = "GMT"
		}
		return ReferenceValue(vm.NewObject("java/util/TimeZone", normalized)), true, nil
	})
	vm.RegisterNative("java/util/TimeZone", "getID", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		_, identifier, err := vm.timeZoneLocation(receiver)
		if err != nil {
			return Value{}, false, err
		}
		return ReferenceValue(vm.NewString(identifier)), true, nil
	})
	vm.RegisterNative("java/util/TimeZone", "setID", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		identifier, err := vm.stringArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		_, normalized, ok := parseGMTOffset(identifier)
		if !ok {
			normalized = identifier
		}
		return Value{}, false, vm.setNative(receiver, normalized)
	})
	offset := func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		location, _, err := vm.timeZoneLocation(receiver)
		if err != nil {
			return Value{}, false, err
		}
		_, seconds := time.Unix(0, 0).In(location).Zone()
		return IntValue(int32(seconds * 1000)), true, nil
	}
	vm.RegisterNative("java/util/TimeZone", "getRawOffset", "()I", offset)
	vm.RegisterNative("java/util/TimeZone", "getOffset", "(IIIIII)I", offset)
	vm.RegisterNative("java/util/TimeZone", "useDaylightTime", "()Z", func(_ context.Context, _ *VM, _ uint32, _ []Value) (Value, bool, error) {
		return boolValue(false), true, nil
	})
}
