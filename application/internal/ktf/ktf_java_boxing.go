package ktf

import (
	"math"
	"strconv"
	"strings"
)

func (r *Runtime) handleBooleanMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>(Z)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value != 0 {
			value = 1
		}
		r.integerValues[instance] = int32(value)
		return 0, nil
	case "booleanValue()Z":
		if r.integerValues[instance] != 0 {
			return 1, nil
		}
		return 0, nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value, ok := r.integerValues[other]; ok &&
			value == r.integerValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		if r.integerValues[instance] != 0 {
			return 1231, nil
		}
		return 1237, nil
	case "toString()Ljava/lang/String;":
		if r.integerValues[instance] != 0 {
			return r.NewJavaString("true")
		}
		return r.NewJavaString("false")
	default:
		return 0, nil
	}
}

func (r *Runtime) handleCharacterMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	character := rune(uint16(instance))
	switch name + descriptor {
	case "<init>(C)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.integerValues[instance] = int32(uint16(value))
		return 0, nil
	case "charValue()C":
		return uint32(uint16(r.integerValues[instance])), nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value, ok := r.integerValues[other]; ok &&
			value == r.integerValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		return uint32(r.integerValues[instance]), nil
	case "toString()Ljava/lang/String;":
		return r.NewJavaString(
			string(rune(uint16(r.integerValues[instance]))),
		)
	case "isDigit(C)Z":
		return boolWord(character >= '0' && character <= '9'), nil
	case "isLowerCase(C)Z":
		return boolWord(character >= 'a' && character <= 'z'), nil
	case "isUpperCase(C)Z":
		return boolWord(character >= 'A' && character <= 'Z'), nil
	case "toLowerCase(C)C":
		if character >= 'A' && character <= 'Z' {
			character += 'a' - 'A'
		}
		return uint32(character), nil
	case "toUpperCase(C)C":
		if character >= 'a' && character <= 'z' {
			character -= 'a' - 'A'
		}
		return uint32(character), nil
	case "digit(CI)I":
		radix, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value := int32(-1)
		switch {
		case character >= '0' && character <= '9':
			value = int32(character - '0')
		case character >= 'a' && character <= 'z':
			value = int32(character-'a') + 10
		case character >= 'A' && character <= 'Z':
			value = int32(character-'A') + 10
		}
		if radix < 2 || radix > 36 || value >= int32(radix) {
			value = -1
		}
		return uint32(value), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleShortMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>(S)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.integerValues[instance] = int32(int16(value))
		return 0, nil
	case "shortValue()S":
		return uint32(int32(int16(r.integerValues[instance]))), nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value, ok := r.integerValues[other]; ok &&
			value == r.integerValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		return uint32(r.integerValues[instance]), nil
	case "toString()Ljava/lang/String;":
		return r.NewJavaString(strconv.FormatInt(
			int64(r.integerValues[instance]),
			10,
		))
	case "parseShort(Ljava/lang/String;)S",
		"parseShort(Ljava/lang/String;I)S":
		radix := uint32(10)
		if descriptor == "(Ljava/lang/String;I)S" {
			var valueErr error
			radix, valueErr = r.parameter(2)
			if valueErr != nil {
				return 0, valueErr
			}
		}
		if radix < 2 || radix > 36 {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		value, parseErr := strconv.ParseInt(
			strings.TrimSpace(r.javaStringValue(instance)),
			int(radix),
			16,
		)
		if parseErr != nil {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		return uint32(int32(int16(value))), nil
	default:
		return 0, nil
	}
}

// Float instances store raw IEEE-754 bits in integerValues; Double
// instances store them in longValues.
func (r *Runtime) handleFloatMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	value := math.Float32frombits(uint32(r.integerValues[instance]))
	switch name + descriptor {
	case "<init>(F)V":
		bits, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.integerValues[instance] = int32(bits)
		return 0, nil
	case "<init>(D)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		wide := math.Float64frombits(uint64(high)<<32 | uint64(low))
		r.integerValues[instance] = int32(math.Float32bits(float32(wide)))
		return 0, nil
	case "floatValue()F":
		return uint32(r.integerValues[instance]), nil
	case "doubleValue()D":
		return r.javaLongResult(math.Float64bits(float64(value))), nil
	case "intValue()I":
		return uint32(int32(value)), nil
	case "longValue()J":
		return r.javaLongResult(uint64(int64(value))), nil
	case "byteValue()B":
		return uint32(int32(int8(value))), nil
	case "shortValue()S":
		return uint32(int32(int16(value))), nil
	case "floatToIntBits(F)I", "intBitsToFloat(I)F":
		// Both directions are the identity on the raw bit pattern.
		return instance, nil
	case "isNaN()Z":
		return boolWord(math.IsNaN(float64(value))), nil
	case "isNaN(F)Z":
		return boolWord(math.IsNaN(
			float64(math.Float32frombits(instance)),
		)), nil
	case "isInfinite()Z":
		return boolWord(math.IsInf(float64(value), 0)), nil
	case "isInfinite(F)Z":
		return boolWord(math.IsInf(
			float64(math.Float32frombits(instance)),
			0,
		)), nil
	case "parseFloat(Ljava/lang/String;)F":
		parsed, parseErr := strconv.ParseFloat(
			strings.TrimSpace(r.javaStringValue(instance)),
			32,
		)
		if parseErr != nil {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		return math.Float32bits(float32(parsed)), nil
	case "toString()Ljava/lang/String;":
		return r.NewJavaString(strconv.FormatFloat(
			float64(value),
			'g',
			-1,
			32,
		))
	case "toString(F)Ljava/lang/String;":
		return r.NewJavaString(strconv.FormatFloat(
			float64(math.Float32frombits(instance)),
			'g',
			-1,
			32,
		))
	case "valueOf(Ljava/lang/String;)Ljava/lang/Float;":
		parsed, parseErr := strconv.ParseFloat(
			strings.TrimSpace(r.javaStringValue(instance)),
			32,
		)
		if parseErr != nil {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		boxed, valueErr := r.NewHostJavaObject("java/lang/Float")
		if valueErr != nil {
			return 0, valueErr
		}
		r.integerValues[boxed] = int32(math.Float32bits(float32(parsed)))
		return boxed, nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if bits, ok := r.integerValues[other]; ok &&
			bits == r.integerValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		return uint32(r.integerValues[instance]), nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleDoubleMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	value := math.Float64frombits(uint64(r.longValues[instance]))
	switch name + descriptor {
	case "<init>(D)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		r.longValues[instance] = int64(uint64(high)<<32 | uint64(low))
		return 0, nil
	case "doubleValue()D":
		return r.javaLongResult(uint64(r.longValues[instance])), nil
	case "floatValue()F":
		return math.Float32bits(float32(value)), nil
	case "intValue()I":
		return uint32(int32(value)), nil
	case "longValue()J":
		return r.javaLongResult(uint64(int64(value))), nil
	case "byteValue()B":
		return uint32(int32(int8(value))), nil
	case "shortValue()S":
		return uint32(int32(int16(value))), nil
	case "doubleToLongBits(D)J", "longBitsToDouble(J)D":
		high, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.javaLongResult(uint64(high)<<32 | uint64(instance)), nil
	case "isNaN()Z":
		return boolWord(math.IsNaN(value)), nil
	case "isInfinite()Z":
		return boolWord(math.IsInf(value, 0)), nil
	case "isNaN(D)Z", "isInfinite(D)Z":
		high, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		wide := math.Float64frombits(uint64(high)<<32 | uint64(instance))
		if name == "isNaN" {
			return boolWord(math.IsNaN(wide)), nil
		}
		return boolWord(math.IsInf(wide, 0)), nil
	case "parseDouble(Ljava/lang/String;)D",
		"parseDouble0(Ljava/lang/String;)D":
		parsed, parseErr := strconv.ParseFloat(
			strings.TrimSpace(r.javaStringValue(instance)),
			64,
		)
		if parseErr != nil {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		return r.javaLongResult(math.Float64bits(parsed)), nil
	case "toString()Ljava/lang/String;":
		return r.NewJavaString(strconv.FormatFloat(value, 'g', -1, 64))
	case "toString(D)Ljava/lang/String;":
		high, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		wide := math.Float64frombits(uint64(high)<<32 | uint64(instance))
		return r.NewJavaString(strconv.FormatFloat(wide, 'g', -1, 64))
	case "valueOf(Ljava/lang/String;)Ljava/lang/Double;":
		parsed, parseErr := strconv.ParseFloat(
			strings.TrimSpace(r.javaStringValue(instance)),
			64,
		)
		if parseErr != nil {
			return 0, r.raiseHostJavaException(
				"java/lang/NumberFormatException",
			)
		}
		boxed, valueErr := r.NewHostJavaObject("java/lang/Double")
		if valueErr != nil {
			return 0, valueErr
		}
		r.longValues[boxed] = int64(math.Float64bits(parsed))
		return boxed, nil
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if bits, ok := r.longValues[other]; ok &&
			bits == r.longValues[instance] {
			return 1, nil
		}
		return 0, nil
	case "hashCode()I":
		bits := uint64(r.longValues[instance])
		return uint32(bits ^ (bits >> 32)), nil
	default:
		return 0, nil
	}
}
