package ktf

import (
	"context"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sort"
	"strconv"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleIntegerMethod(
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>(I)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		r.integerValues[instance] = int32(value)
		return 0, nil
	case "byteValue()B":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(int32(int8(r.integerValues[instance]))), nil
	case "shortValue()S":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(int32(int16(r.integerValues[instance]))), nil
	case "intValue()I", "longValue()J":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if name == "longValue" {
			return r.javaLongResult(
				uint64(int64(r.integerValues[instance])),
			), nil
		}
		return uint32(r.integerValues[instance]), nil
	case "toString()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(strconv.FormatInt(int64(r.integerValues[instance]), 10))
	case "parseInt(Ljava/lang/String;)I", "parseInt(Ljava/lang/String;I)I":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)I" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return 0, nil
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			32,
		)
		if parseErr != nil {
			return 0, nil
		}
		return uint32(int32(value)), nil
	case "toString(I)Ljava/lang/String;":
		value, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(strconv.FormatInt(int64(int32(value)), 10))
	case "toString(II)Ljava/lang/String;":
		value, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if radix < 2 || radix > 36 {
			radix = 10
		}
		return r.NewJavaString(strconv.FormatInt(
			int64(int32(value)),
			int(radix),
		))
	case "toHexString(I)Ljava/lang/String;",
		"toOctalString(I)Ljava/lang/String;",
		"toBinaryString(I)Ljava/lang/String;":
		value, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := 16
		switch name {
		case "toOctalString":
			radix = 8
		case "toBinaryString":
			radix = 2
		}
		return r.NewJavaString(strconv.FormatUint(uint64(value), radix))
	case "equals(Ljava/lang/Object;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		other, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if value, ok := r.integerValues[other]; ok &&
			value == r.integerValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(r.integerValues[instance]), nil
	case "floatValue()F":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return math.Float32bits(float32(r.integerValues[instance])), nil
	case "doubleValue()D":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.javaLongResult(
			math.Float64bits(float64(r.integerValues[instance])),
		), nil
	case "valueOf(Ljava/lang/String;)Ljava/lang/Integer;",
		"valueOf(Ljava/lang/String;I)Ljava/lang/Integer;":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)Ljava/lang/Integer;" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			32,
		)
		if parseErr != nil {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		boxed, err := r.NewHostJavaObject("java/lang/Integer")
		if err != nil {
			return 0, err
		}
		r.integerValues[boxed] = int32(value)
		return boxed, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleLongMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "<init>(J)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		low, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		high, err := r.parameter(3)
		if err != nil {
			return 0, err
		}
		r.longValues[instance] = int64(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "longValue()J":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.javaLongResult(uint64(r.longValues[instance])), nil
	case "toString()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(strconv.FormatInt(r.longValues[instance], 10))
	case "parseLong(Ljava/lang/String;)J",
		"parseLong(Ljava/lang/String;I)J":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)J" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return r.javaLongResult(0), nil
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			64,
		)
		if parseErr != nil {
			return r.javaLongResult(0), nil
		}
		return r.javaLongResult(uint64(value)), nil
	case "toString(J)Ljava/lang/String;",
		"toString(JI)Ljava/lang/String;":
		low, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		high, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(JI)Ljava/lang/String;" {
			radix, err = r.parameter(3)
			if err != nil {
				return 0, err
			}
			if radix < 2 || radix > 36 {
				radix = 10
			}
		}
		value := int64(uint64(high)<<32 | uint64(low))
		return r.NewJavaString(strconv.FormatInt(value, int(radix)))
	case "equals(Ljava/lang/Object;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		other, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if value, ok := r.longValues[other]; ok &&
			value == r.longValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value := uint64(r.longValues[instance])
		return uint32(value ^ (value >> 32)), nil
	case "floatValue()F":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return math.Float32bits(float32(r.longValues[instance])), nil
	case "doubleValue()D":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.javaLongResult(
			math.Float64bits(float64(r.longValues[instance])),
		), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleThrowableMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		delete(r.throwableMessages, instance)
		return 0, nil
	case "<init>(Ljava/lang/String;)V":
		message, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.throwableMessages[instance] = message
		return 0, nil
	case "getMessage()Ljava/lang/String;":
		return r.throwableMessages[instance], nil
	case "printStackTrace()V":
		message := r.javaStringValue(r.throwableMessages[instance])
		r.tracef(
			"java_stack_trace:instance=0x%08x:message=%q",
			instance,
			message,
		)
		return 0, nil
	case "toString()Ljava/lang/String;":
		className := "java/lang/Throwable"
		if instance != 0 {
			if classAddress, readErr := r.ReadU32(instance + 4); readErr == nil {
				if class, inspectErr := r.InspectJavaClass(classAddress); inspectErr == nil {
					className = class.Name
				}
			}
		}
		text := strings.ReplaceAll(className, "/", ".")
		if message := r.javaStringValue(r.throwableMessages[instance]); message != "" {
			text += ": " + message
		}
		return r.NewJavaString(text)
	default:
		return 0, nil
	}
}

func (r *Runtime) handleByteMethod(name, descriptor string) (uint32, error) {
	switch name + descriptor {
	case "parseByte(Ljava/lang/String;)B", "parseByte(Ljava/lang/String;I)B":
		text, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)B" {
			radix, err = r.parameter(2)
			if err != nil {
				return 0, err
			}
		}
		if radix < 2 || radix > 36 {
			return 0, nil
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(text)),
			int(radix),
			8,
		)
		if parseErr != nil {
			return 0, nil
		}
		return uint32(int32(int8(value))), nil
	case "<init>(B)V":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		value, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		r.integerValues[instance] = int32(int8(value))
		return 0, nil
	case "byteValue()B":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(int32(int8(r.integerValues[instance]))), nil
	case "equals(Ljava/lang/Object;)Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		other, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if value, ok := r.integerValues[other]; ok &&
			value == r.integerValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return uint32(r.integerValues[instance]), nil
	case "toString()Ljava/lang/String;":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		return r.NewJavaString(strconv.FormatInt(
			int64(r.integerValues[instance]),
			10,
		))
	default:
		return 0, nil
	}
}

func (r *Runtime) handleMathMethod(name, descriptor string) (uint32, error) {
	left, err := r.signedParameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "abs(I)I":
		if left < 0 {
			left = -left
		}
		return uint32(left), nil
	case "max(II)I", "min(II)I":
		right, valueErr := r.signedParameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if name == "max" {
			if right > left {
				left = right
			}
		} else if right < left {
			left = right
		}
		return uint32(left), nil
	case "ceil(D)D", "floor(D)D", "toDegrees(D)D", "toRadians(D)D":
		low, valueErr := r.parameter(1)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value := math.Float64frombits(uint64(high)<<32 | uint64(low))
		switch name {
		case "ceil":
			value = math.Ceil(value)
		case "floor":
			value = math.Floor(value)
		case "toDegrees":
			value = value * 180 / math.Pi
		case "toRadians":
			value = value * math.Pi / 180
		}
		return r.javaLongResult(math.Float64bits(value)), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleRandomMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	setSeed := func(seed uint64) {
		r.randomSeeds[instance] = shared.JavaRandomSeed(int64(seed))
	}
	next := func(bits uint8) (uint32, error) {
		state, ok := r.randomSeeds[instance]
		if !ok {
			state = shared.JavaRandomSeed(int64(instance))
		}
		value, err := shared.JavaRandomBits(&state, bits)
		if err == nil {
			r.randomSeeds[instance] = state
		}
		return value, err
	}
	switch name + descriptor {
	case "<init>()V":
		setSeed(uint64(instance))
		return 0, nil
	case "<init>(J)V", "setSeed(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		setSeed(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "nextInt()I":
		return next(32)
	case "nextInt(I)I":
		bound, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if int32(bound) <= 0 {
			return 0, nil
		}
		value, valueErr := next(31)
		if valueErr != nil {
			return 0, valueErr
		}
		return uint32(uint64(value) * uint64(bound) >> 31), nil
	case "nextBoolean()Z":
		return next(1)
	case "next(I)I":
		bits, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if bits == 0 || bits > 32 {
			bits = 32
		}
		return next(uint8(bits))
	case "nextLong()J":
		high, valueErr := next(32)
		if valueErr != nil {
			return 0, valueErr
		}
		low, valueErr := next(32)
		if valueErr != nil {
			return 0, valueErr
		}
		value := int64(high)<<32 + int64(int32(low))
		return r.javaLongResult(uint64(value)), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleDateMethod(name, descriptor string) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V":
		r.dates[instance] = int64(r.TickMS)
		return 0, nil
	case "<init>(J)V", "setTime(J)V":
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
	case "getTime()J":
		return r.javaLongResult(uint64(r.dates[instance])), nil
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
	default:
		return 0, nil
	}
}

func (r *Runtime) handleVectorMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	values := r.Vectors[instance]
	switch name + descriptor {
	case "<init>()V", "<init>(I)V", "<init>(II)V":
		r.Vectors[instance] = nil
		return 0, nil
	case "addElement(Ljava/lang/Object;)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.Vectors[instance] = append(values, value)
		return 0, nil
	case "insertElementAt(Ljava/lang/Object;I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		index, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if index > uint32(len(values)) {
			return 0, nil
		}
		values = append(values, 0)
		copy(values[index+1:], values[index:])
		values[index] = value
		r.Vectors[instance] = values
		return 0, nil
	case "push(Ljava/lang/Object;)Ljava/lang/Object;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.Vectors[instance] = append(values, value)
		return value, nil
	case "elementAt(I)Ljava/lang/Object;":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index >= uint32(len(values)) {
			return 0, nil
		}
		return values[index], nil
	case "setElementAt(Ljava/lang/Object;I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		index, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if index < uint32(len(values)) {
			values[index] = value
		}
		return 0, nil
	case "removeElementAt(I)V":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index < uint32(len(values)) {
			r.Vectors[instance] = append(values[:index:index], values[index+1:]...)
		}
		return 0, nil
	case "removeElement(Ljava/lang/Object;)Z":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for index, value := range values {
			if value == target {
				r.Vectors[instance] = append(
					values[:index:index],
					values[index+1:]...,
				)
				return 1, nil
			}
		}
		return 0, nil
	case "removeAllElements()V":
		r.Vectors[instance] = nil
		return 0, nil
	case "size()I", "capacity()I":
		return uint32(len(values)), nil
	case "isEmpty()Z", "empty()Z":
		if len(values) == 0 {
			return 1, nil
		}
		return 0, nil
	case "contains(Ljava/lang/Object;)Z":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for _, value := range values {
			if value == target {
				return 1, nil
			}
		}
		return 0, nil
	case "indexOf(Ljava/lang/Object;)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for index, value := range values {
			if value == target {
				return uint32(index), nil
			}
		}
		return ^uint32(0), nil
	case "copyInto([Ljava/lang/Object;)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return 0, r.raiseHostJavaException("java/lang/NullPointerException")
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		if uint32(len(values)) > length {
			return 0, r.raiseHostJavaException(
				"java/lang/ArrayIndexOutOfBoundsException",
			)
		}
		fields, valueErr := r.ReadU32(array)
		if valueErr != nil {
			return 0, valueErr
		}
		if len(values) != 0 {
			if valueErr = r.writeWords(fields+8, values); valueErr != nil {
				return 0, valueErr
			}
		}
		return 0, nil
	case "pop()Ljava/lang/Object;":
		if len(values) == 0 {
			return 0, nil
		}
		value := values[len(values)-1]
		r.Vectors[instance] = values[:len(values)-1]
		return value, nil
	case "peek()Ljava/lang/Object;":
		if len(values) == 0 {
			return 0, nil
		}
		return values[len(values)-1], nil
	case "elements()Ljava/util/Enumeration;":
		return r.newJavaEnumeration(values)
	case "ensureCapacity(I)V", "trimToSize()V":
		return 0, nil
	case "setSize(I)V":
		size, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if int32(size) < 0 {
			return 0, r.raiseHostJavaException(
				"java/lang/ArrayIndexOutOfBoundsException",
			)
		}
		for uint32(len(values)) < size {
			values = append(values, 0)
		}
		r.Vectors[instance] = values[:size]
		return 0, nil
	case "firstElement()Ljava/lang/Object;":
		if len(values) == 0 {
			return 0, r.raiseHostJavaException(
				"java/util/NoSuchElementException",
			)
		}
		return values[0], nil
	case "lastElement()Ljava/lang/Object;":
		if len(values) == 0 {
			return 0, r.raiseHostJavaException(
				"java/util/NoSuchElementException",
			)
		}
		return values[len(values)-1], nil
	case "lastIndexOf(Ljava/lang/Object;)I",
		"lastIndexOf(Ljava/lang/Object;I)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		fromIndex := len(values) - 1
		if descriptor == "(Ljava/lang/Object;I)I" {
			from, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			fromIndex = int(int32(from))
		}
		if fromIndex > len(values)-1 {
			fromIndex = len(values) - 1
		}
		for index := fromIndex; index >= 0; index-- {
			if values[index] == target {
				return uint32(index), nil
			}
		}
		return ^uint32(0), nil
	case "search(Ljava/lang/Object;)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for index := len(values) - 1; index >= 0; index-- {
			if values[index] == target {
				return uint32(len(values) - index), nil
			}
		}
		return ^uint32(0), nil
	case "toString()Ljava/lang/String;":
		parts := make([]string, len(values))
		for index, value := range values {
			parts[index] = r.javaObjectString(value)
		}
		return r.NewJavaString("[" + strings.Join(parts, ", ") + "]")
	default:
		return 0, nil
	}
}

func (r *Runtime) javaHashtableKey(instance uint32) string {
	if value, ok := r.JavaStrings[instance]; ok {
		return "string:" + value
	}
	return fmt.Sprintf("object:%08x", instance)
}

func (r *Runtime) handleHashtableMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	table := r.hashtables[instance]
	switch name + descriptor {
	case "<init>()V", "<init>(I)V":
		r.hashtables[instance] = make(map[string]ktfHashtableEntry)
		return 0, nil
	case "put(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;":
		key, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if table == nil {
			table = make(map[string]ktfHashtableEntry)
			r.hashtables[instance] = table
		}
		normalized := r.javaHashtableKey(key)
		previous := table[normalized].value
		table[normalized] = ktfHashtableEntry{key: key, value: value}
		return previous, nil
	case "get(Ljava/lang/Object;)Ljava/lang/Object;",
		"remove(Ljava/lang/Object;)Ljava/lang/Object;",
		"containsKey(Ljava/lang/Object;)Z":
		key, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		normalized := r.javaHashtableKey(key)
		entry, ok := table[normalized]
		if name == "containsKey" {
			if ok {
				return 1, nil
			}
			return 0, nil
		}
		if name == "remove" {
			delete(table, normalized)
		}
		return entry.value, nil
	case "contains(Ljava/lang/Object;)Z":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		for _, entry := range table {
			if entry.value == target {
				return 1, nil
			}
		}
		return 0, nil
	case "size()I":
		return uint32(len(table)), nil
	case "isEmpty()Z":
		if len(table) == 0 {
			return 1, nil
		}
		return 0, nil
	case "clear()V":
		clear(table)
		return 0, nil
	case "keys()Ljava/util/Enumeration;",
		"elements()Ljava/util/Enumeration;":
		values := make([]uint32, 0, len(table))
		for _, entry := range table {
			value := entry.value
			if name == "keys" {
				value = entry.key
			}
			values = append(values, value)
		}
		return r.newJavaEnumeration(values)
	case "rehash()V":
		return 0, nil
	case "toString()Ljava/lang/String;":
		normalized := make([]string, 0, len(table))
		for key := range table {
			normalized = append(normalized, key)
		}
		sort.Strings(normalized)
		parts := make([]string, 0, len(table))
		for _, key := range normalized {
			entry := table[key]
			parts = append(
				parts,
				r.javaObjectString(entry.key)+"="+
					r.javaObjectString(entry.value),
			)
		}
		return r.NewJavaString("{" + strings.Join(parts, ", ") + "}")
	default:
		return 0, nil
	}
}

func (r *Runtime) newJavaEnumeration(values []uint32) (uint32, error) {
	instance, err := r.NewHostJavaObject("java/util/Enumeration")
	if err != nil {
		return 0, err
	}
	r.enumerations[instance] = &ktfEnumeration{
		values: append([]uint32(nil), values...),
	}
	return instance, nil
}

func (r *Runtime) handleEnumerationMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	enumeration := r.enumerations[instance]
	switch name + descriptor {
	case "hasMoreElements()Z":
		if enumeration != nil && enumeration.index < uint32(len(enumeration.values)) {
			return 1, nil
		}
		return 0, nil
	case "nextElement()Ljava/lang/Object;":
		if enumeration == nil || enumeration.index >= uint32(len(enumeration.values)) {
			return 0, nil
		}
		value := enumeration.values[enumeration.index]
		enumeration.index++
		return value, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleTimerMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	switch name + descriptor {
	case "<init>()V":
		return 0, nil
	case "cancel()V":
		timer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		cancelled := 0
		for instance, task := range r.javaTimerTasks {
			if task == nil || task.timerOwner != timer {
				continue
			}
			task.Done = true
			delete(r.javaTimerTasks, instance)
			r.javaTimerTaskStates[instance] = ktfJavaTimerCancelled
			// Same lifecycle as any other task exit: a sibling started off
			// this one must stop waiting, and this task's pointer must not
			// stay keyed in the deferred-paint maps once its task-table slot
			// is recycled, or the next SaveState rejects it as pointing
			// outside the task table.
			r.releaseStartedThreads(task, "timer-cancel")
			if err := r.releaseDeferredCardPaints(ctx, task); err != nil {
				return 0, err
			}
			cancelled++
		}
		cancelled += r.dropPendingJavaTimers(func(call ktfPendingJavaCall) bool {
			if call.timer.owner != timer {
				return false
			}
			r.javaTimerTaskStates[call.instance] = ktfJavaTimerCancelled
			return true
		})
		r.tracef(
			"java_timer_cancel:timer=0x%08x:tasks=%d",
			timer,
			cancelled,
		)
		return 0, nil
	case "cancel()Z":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		result := uint32(0)
		if r.javaTimerTaskStates[instance] == ktfJavaTimerScheduled {
			result = 1
		}
		if task := r.javaTimerTasks[instance]; task != nil {
			task.Done = true
			delete(r.javaTimerTasks, instance)
			r.releaseStartedThreads(task, "timer-cancel")
			if err := r.releaseDeferredCardPaints(ctx, task); err != nil {
				return 0, err
			}
		}
		r.dropPendingJavaTimers(func(call ktfPendingJavaCall) bool {
			return call.instance == instance
		})
		r.javaTimerTaskStates[instance] = ktfJavaTimerCancelled
		r.tracef(
			"java_timer_task_cancel:task=0x%08x:scheduled=%t",
			instance,
			result != 0,
		)
		return result, nil
	case "schedule(Ljava/util/TimerTask;J)V",
		"schedule(Ljava/util/TimerTask;JJ)V",
		"scheduleAtFixedRate(Ljava/util/TimerTask;JJ)V":
		timer, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		task, err := r.parameter(2)
		if err != nil {
			return 0, err
		}
		if task == 0 {
			return r.raiseJavaException("java/lang/NullPointerException", 0)
		}
		delay, err := r.javaTimerLongParameter(3)
		if err != nil {
			return 0, err
		}
		period := int64(0)
		if descriptor != "(Ljava/util/TimerTask;J)V" {
			period, err = r.javaTimerLongParameter(5)
			if err != nil {
				return 0, err
			}
		}
		if delay < 0 || period < 0 ||
			descriptor != "(Ljava/util/TimerTask;J)V" && period == 0 {
			return r.raiseJavaException("java/lang/IllegalArgumentException", 0)
		}
		if r.javaTimerTaskStates[task] != 0 {
			return r.raiseJavaException("java/lang/IllegalStateException", 0)
		}
		if !r.DeferThreads {
			return r.invokeJavaVirtual(ctx, task, "run", "()V")
		}
		deadline := r.javaTimerDeadline(uint64(delay))
		pending := ktfPendingTimer{
			owner:      timer,
			periodMS:   uint64(period),
			deadlineMS: deadline,
			fixedRate:  name == "scheduleAtFixedRate",
		}
		if delay != 0 {
			pending.wakeAtMS = deadline
		}
		// A java.util.Timer runs its tasks on one thread of its own and queues
		// whatever it cannot start yet, so scheduling can never fail on the
		// handset for want of a thread. Here every scheduled task takes a task
		// slot, and a title whose run() sleeps holds that slot for as long as
		// it sleeps: random key input on 소울카드마스터2 filled all sixteen with
		// sleeping timer tasks and the next schedule killed the title. A
		// schedule with no room now waits for one, the way the queue behind a
		// timer thread does.
		if err := r.queueJavaTimerTask(task, pending); err != nil {
			return 0, err
		}
		r.javaTimerTaskStates[task] = ktfJavaTimerScheduled
		r.tracef(
			"java_timer_schedule:timer=0x%08x:task=0x%08x:"+
				"delay_ms=%d:period_ms=%d:fixed_rate=%t",
			timer,
			task,
			delay,
			period,
			pending.fixedRate,
		)
		return 0, nil
	case "scheduledExecutionTime()J":
		instance, err := r.parameter(1)
		if err != nil {
			return 0, err
		}
		if task := r.javaTimerTasks[instance]; task != nil {
			return r.javaLongResult(task.timerDeadlineMS), nil
		}
		for _, call := range r.PendingJavaCalls {
			if call.timer != nil && call.instance == instance {
				return r.javaLongResult(call.timer.deadlineMS), nil
			}
		}
		return r.javaLongResult(0), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) javaTimerLongParameter(index uint32) (int64, error) {
	low, err := r.parameter(index)
	if err != nil {
		return 0, err
	}
	high, err := r.parameter(index + 1)
	if err != nil {
		return 0, err
	}
	return int64(uint64(high)<<32 | uint64(low)), nil
}

func (r *Runtime) javaTimerDeadline(delay uint64) uint64 {
	if delay > ^uint64(0)-r.TickMS {
		return ^uint64(0)
	}
	return r.TickMS + delay
}

// queueJavaTimerTask starts a scheduled TimerTask, or parks it until a task
// slot frees. A parked schedule keeps its state as scheduled, so cancel() and
// scheduledExecutionTime() see it exactly as they see a started one.
func (r *Runtime) queueJavaTimerTask(task uint32, pending ktfPendingTimer) error {
	if !r.HasJavaTaskCapacity() {
		if len(r.PendingJavaCalls) >= ktfMaxPendingJavaCalls {
			return fmt.Errorf(
				"KTF pending Java call limit %d reached",
				ktfMaxPendingJavaCalls,
			)
		}
		waiting := pending
		r.PendingJavaCalls = append(r.PendingJavaCalls, ktfPendingJavaCall{
			instance:   task,
			name:       "run",
			descriptor: "()V",
			timer:      &waiting,
		})
		r.tracef(
			"java_timer_defer:timer=0x%08x:task=0x%08x:wake_at_ms=%d:pending=%d",
			pending.owner,
			task,
			pending.wakeAtMS,
			len(r.PendingJavaCalls),
		)
		return nil
	}
	queued, err := r.queueJavaVirtualTask(task, "run", "()V")
	if err != nil {
		return err
	}
	r.applyJavaTimer(queued, task, pending)
	return nil
}

func (r *Runtime) applyJavaTimer(
	queued *Task,
	task uint32,
	pending ktfPendingTimer,
) {
	queued.timerTask = task
	queued.timerOwner = pending.owner
	queued.timerPeriodMS = pending.periodMS
	queued.timerDeadlineMS = pending.deadlineMS
	queued.timerFixedRate = pending.fixedRate
	if pending.wakeAtMS != 0 {
		queued.WakeAtMS = pending.wakeAtMS
	}
	r.javaTimerTasks[task] = queued
}

// dropPendingJavaTimers removes parked schedules that match, and answers how
// many it removed. A cancelled schedule must not start later.
func (r *Runtime) dropPendingJavaTimers(match func(ktfPendingJavaCall) bool) int {
	kept := r.PendingJavaCalls[:0]
	dropped := 0
	for _, call := range r.PendingJavaCalls {
		if call.timer != nil && match(call) {
			dropped++
			continue
		}
		kept = append(kept, call)
	}
	for index := len(kept); index < len(r.PendingJavaCalls); index++ {
		r.PendingJavaCalls[index] = ktfPendingJavaCall{}
	}
	r.PendingJavaCalls = kept
	return dropped
}

func (r *Runtime) beginJavaTimerTask(task *Task) {
	if task == nil || task.timerTask == 0 || task.timerPeriodMS != 0 {
		return
	}
	if r.javaTimerTasks[task.timerTask] == task &&
		r.javaTimerTaskStates[task.timerTask] == ktfJavaTimerScheduled {
		delete(r.javaTimerTasks, task.timerTask)
		r.javaTimerTaskStates[task.timerTask] = ktfJavaTimerExecuted
	}
}

func (r *Runtime) completeJavaTimerTask(task *Task) error {
	if task == nil || task.timerTask == 0 {
		return nil
	}
	instance := task.timerTask
	if task.timerPeriodMS == 0 {
		if r.javaTimerTasks[instance] == task {
			delete(r.javaTimerTasks, instance)
			r.javaTimerTaskStates[instance] = ktfJavaTimerExecuted
		}
		return nil
	}
	if r.javaTimerTasks[instance] != task ||
		r.javaTimerTaskStates[instance] != ktfJavaTimerScheduled {
		return nil
	}
	deadline := r.javaTimerDeadline(task.timerPeriodMS)
	if task.timerFixedRate {
		deadline = task.timerDeadlineMS
		if task.timerPeriodMS > ^uint64(0)-deadline {
			deadline = ^uint64(0)
		} else {
			deadline += task.timerPeriodMS
		}
	}
	if err := r.queueJavaTimerTask(instance, ktfPendingTimer{
		owner:      task.timerOwner,
		periodMS:   task.timerPeriodMS,
		deadlineMS: deadline,
		wakeAtMS:   deadline,
		fixedRate:  task.timerFixedRate,
	}); err != nil {
		return fmt.Errorf("reschedule KTF Java TimerTask: %w", err)
	}
	r.tracef(
		"java_timer_reschedule:timer=0x%08x:task=0x%08x:wake_at_ms=%d",
		task.timerOwner,
		instance,
		deadline,
	)
	return nil
}
