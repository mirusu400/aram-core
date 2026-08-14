package ktf

import (
	"encoding/binary"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
	"strings"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleStringMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	value := r.JavaStrings[instance]
	codeUnits := utf16.Encode([]rune(value))
	switch name + descriptor {
	case "valueOf(I)Ljava/lang/String;":
		return r.NewJavaString(strconv.FormatInt(int64(int32(instance)), 10))
	case "valueOf(J)Ljava/lang/String;":
		high, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		number := int64(uint64(high)<<32 | uint64(instance))
		return r.NewJavaString(strconv.FormatInt(number, 10))
	case "valueOf(Z)Ljava/lang/String;":
		if instance == 0 {
			return r.NewJavaString("false")
		}
		return r.NewJavaString("true")
	case "valueOf(C)Ljava/lang/String;":
		return r.NewJavaString(string(rune(uint16(instance))))
	case "valueOf(Ljava/lang/Object;)Ljava/lang/String;":
		return r.NewJavaString(r.javaObjectString(instance))
	case "valueOf([C)Ljava/lang/String;":
		if instance == 0 {
			return r.NewJavaString("null")
		}
		length, valueErr := r.javaArrayLength(instance)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(instance, 0, length)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.NewJavaString(text)
	case "valueOf([CII)Ljava/lang/String;":
		offset, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(instance, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.NewJavaString(text)
	case "<init>()V":
		return 0, r.materializeJavaString(instance, "")
	case "<init>(Ljava/lang/String;)V":
		source, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.materializeJavaString(instance, r.javaStringValue(source))
	case "<init>([B)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return 0, r.materializeJavaString(instance, "")
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr = r.Services.Text.Decode(data, shared.EncodingEUCKR)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.materializeJavaString(instance, value)
	case "<init>([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return 0, r.materializeJavaString(instance, "")
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr = r.Services.Text.Decode(data, shared.EncodingEUCKR)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.materializeJavaString(instance, value)
	case "<init>([C)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(array, 0, length)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.materializeJavaString(instance, text)
	case "<init>([CII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		text, valueErr := r.readJavaCharArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, r.materializeJavaString(instance, text)
	case "length()I":
		return uint32(len(codeUnits)), nil
	case "hashCode()I":
		var hash int32
		for _, codeUnit := range codeUnits {
			hash = hash*31 + int32(codeUnit)
		}
		return uint32(hash), nil
	case "charAt(I)C":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if index >= uint32(len(codeUnits)) {
			return 0, nil
		}
		return uint32(codeUnits[index]), nil
	case "getChars(II[CI)V":
		// Titles copy a name prefix through getChars before appending an
		// index; leaving it unimplemented silently produced NUL characters
		// and broke every resource lookup built that way (issue #44).
		sourceBegin, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		sourceEnd, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		array, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		destinationBegin, valueErr := r.parameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return 0, r.raiseHostJavaException(
				"java/lang/NullPointerException",
			)
		}
		if sourceBegin > sourceEnd || sourceEnd > uint32(len(codeUnits)) {
			return 0, r.raiseHostJavaException(
				"java/lang/IndexOutOfBoundsException",
			)
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		count := sourceEnd - sourceBegin
		if destinationBegin > length || count > length-destinationBegin {
			return 0, r.raiseHostJavaException(
				"java/lang/ArrayIndexOutOfBoundsException",
			)
		}
		if count == 0 {
			return 0, nil
		}
		fields, valueErr := r.ReadU32(array)
		if valueErr != nil {
			return 0, valueErr
		}
		encoded := make([]byte, count*2)
		for index, codeUnit := range codeUnits[sourceBegin:sourceEnd] {
			binary.LittleEndian.PutUint16(encoded[index*2:], codeUnit)
		}
		return 0, r.CPU.WriteMemory(fields+8+destinationBegin*2, encoded)
	case "substring(I)Ljava/lang/String;":
		start, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if start > uint32(len(codeUnits)) {
			return 0, nil
		}
		return r.NewJavaString(string(utf16.Decode(codeUnits[start:])))
	case "substring(II)Ljava/lang/String;":
		start, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		end, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		if start > end || end > uint32(len(codeUnits)) {
			return 0, nil
		}
		return r.NewJavaString(string(utf16.Decode(codeUnits[start:end])))
	case "trim()Ljava/lang/String;":
		return r.NewJavaString(strings.TrimSpace(value))
	case "getBytes()[B":
		encoded, valueErr := r.Services.Text.Encode(
			value,
			shared.EncodingEUCKR,
		)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.newJavaByteArray(encoded)
	case "toCharArray()[C":
		return r.newJavaCharArray(value)
	case "equals(Ljava/lang/Object;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if instance == other {
			return 1, nil
		}
		if otherValue, ok := r.JavaStrings[other]; ok && value == otherValue {
			return 1, nil
		}
		return 0, nil
	case "compareTo(Ljava/lang/String;)I":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		otherValue := r.JavaStrings[other]
		otherCodeUnits := utf16.Encode([]rune(otherValue))
		limit := min(len(codeUnits), len(otherCodeUnits))
		for index := range limit {
			if codeUnits[index] == otherCodeUnits[index] {
				continue
			}
			return uint32(
				int32(codeUnits[index]) - int32(otherCodeUnits[index]),
			), nil
		}
		return uint32(int32(len(codeUnits) - len(otherCodeUnits))), nil
	case "concat(Ljava/lang/String;)Ljava/lang/String;":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.NewJavaString(value + r.javaStringValue(other))
	case "startsWith(Ljava/lang/String;)Z":
		prefix, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if strings.HasPrefix(value, r.javaStringValue(prefix)) {
			return 1, nil
		}
		return 0, nil
	case "endsWith(Ljava/lang/String;)Z":
		suffix, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if strings.HasSuffix(value, r.javaStringValue(suffix)) {
			return 1, nil
		}
		return 0, nil
	case "indexOf(I)I", "indexOf(II)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		targetUnits, valid := javaCodePointUnits(target)
		if !valid {
			return ^uint32(0), nil
		}
		fromIndex := 0
		if descriptor == "(II)I" {
			from, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			fromIndex = int(int32(from))
		}
		return uint32(int32(indexJavaCodeUnitsFrom(
			codeUnits,
			targetUnits,
			fromIndex,
		))), nil
	case "indexOf(Ljava/lang/String;)I",
		"indexOf(Ljava/lang/String;I)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		targetUnits := utf16.Encode([]rune(r.javaStringValue(target)))
		fromIndex := 0
		if descriptor == "(Ljava/lang/String;I)I" {
			from, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			fromIndex = int(int32(from))
		}
		return uint32(int32(indexJavaCodeUnitsFrom(
			codeUnits,
			targetUnits,
			fromIndex,
		))), nil
	case "toLowerCase()Ljava/lang/String;":
		return r.NewJavaString(strings.ToLower(value))
	case "toUpperCase()Ljava/lang/String;":
		return r.NewJavaString(strings.ToUpper(value))
	case "toString()Ljava/lang/String;":
		return instance, nil
	case "equalsIgnoreCase(Ljava/lang/String;)Z":
		other, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if other == 0 {
			return 0, nil
		}
		if strings.EqualFold(value, r.javaStringValue(other)) {
			return 1, nil
		}
		return 0, nil
	case "lastIndexOf(I)I", "lastIndexOf(II)I":
		target, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		fromIndex := len(codeUnits) - 1
		if descriptor == "(II)I" {
			from, parameterErr := r.parameter(3)
			if parameterErr != nil {
				return 0, parameterErr
			}
			fromIndex = int(int32(from))
		}
		if fromIndex > len(codeUnits)-1 {
			fromIndex = len(codeUnits) - 1
		}
		for index := fromIndex; index >= 0; index-- {
			if uint32(codeUnits[index]) == target {
				return uint32(index), nil
			}
		}
		return ^uint32(0), nil
	case "regionMatches(ZILjava/lang/String;II)Z":
		ignoreCase, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		thisOffset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		other, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		otherOffset, valueErr := r.parameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(6)
		if valueErr != nil {
			return 0, valueErr
		}
		otherUnits := utf16.Encode([]rune(r.javaStringValue(other)))
		if int32(thisOffset) < 0 || int32(otherOffset) < 0 ||
			int32(count) < 0 ||
			uint64(thisOffset)+uint64(count) > uint64(len(codeUnits)) ||
			uint64(otherOffset)+uint64(count) > uint64(len(otherUnits)) {
			return 0, nil
		}
		left := string(utf16.Decode(codeUnits[thisOffset : thisOffset+count]))
		right := string(utf16.Decode(otherUnits[otherOffset : otherOffset+count]))
		if left == right || (ignoreCase != 0 && strings.EqualFold(left, right)) {
			return 1, nil
		}
		return 0, nil
	case "replace(CC)Ljava/lang/String;":
		oldChar, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		newChar, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		replaced := append([]uint16(nil), codeUnits...)
		changed := false
		for index, codeUnit := range replaced {
			if uint32(codeUnit) == oldChar {
				replaced[index] = uint16(newChar)
				changed = true
			}
		}
		if !changed {
			return instance, nil
		}
		return r.NewJavaString(string(utf16.Decode(replaced)))
	default:
		return 0, nil
	}
}

const unicodeMaxRune = uint32(0x10ffff)

func indexJavaCodeUnits(value, target []uint16) int {
	return indexJavaCodeUnitsFrom(value, target, 0)
}

func indexJavaCodeUnitsFrom(value, target []uint16, fromIndex int) int {
	if fromIndex < 0 {
		fromIndex = 0
	}
	if len(target) == 0 {
		if fromIndex > len(value) {
			return len(value)
		}
		return fromIndex
	}
	if fromIndex >= len(value) || len(target) > len(value)-fromIndex {
		return -1
	}
	for start := fromIndex; start <= len(value)-len(target); start++ {
		matched := true
		for offset := range target {
			if value[start+offset] != target[offset] {
				matched = false
				break
			}
		}
		if matched {
			return start
		}
	}
	return -1
}

func javaCodePointUnits(value uint32) ([]uint16, bool) {
	if value > unicodeMaxRune {
		return nil, false
	}
	if value < 0x10000 {
		return []uint16{uint16(value)}, true
	}
	return utf16.Encode([]rune{rune(value)}), true
}

func (r *Runtime) newJavaCharArray(value string) (uint32, error) {
	codeUnits := utf16.Encode([]rune(value))
	array, err := r.NewJavaArray("[C", uint32(len(codeUnits)), 2)
	if err != nil {
		return 0, err
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return 0, err
	}
	encoded := make([]byte, len(codeUnits)*2)
	for index, codeUnit := range codeUnits {
		binary.LittleEndian.PutUint16(encoded[index*2:], codeUnit)
	}
	if err := r.CPU.WriteMemory(fields+8, encoded); err != nil {
		return 0, err
	}
	return array, nil
}

func (r *Runtime) handleStringBufferMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V", "<init>(I)V":
		r.stringBuffers[instance] = ""
		return 0, nil
	case "<init>(Ljava/lang/String;)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] = r.javaStringValue(value)
		return 0, nil
	case "append(Ljava/lang/String;)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += r.javaStringValue(value)
		return instance, nil
	case "append(Ljava/lang/Object;)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += r.javaObjectString(value)
		return instance, nil
	case "append(Z)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value == 0 {
			r.stringBuffers[instance] += "false"
		} else {
			r.stringBuffers[instance] += "true"
		}
		return instance, nil
	case "append(I)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += fmt.Sprintf("%d", int32(value))
		return instance, nil
	case "append(J)Ljava/lang/StringBuffer;":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		value := int64(uint64(high)<<32 | uint64(low))
		r.stringBuffers[instance] += fmt.Sprintf("%d", value)
		return instance, nil
	case "append(C)Ljava/lang/StringBuffer;":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += string(rune(uint16(value)))
		return instance, nil
	case "append([CII)Ljava/lang/StringBuffer;":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.readJavaCharArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		r.stringBuffers[instance] += value
		return instance, nil
	case "delete(II)Ljava/lang/StringBuffer;":
		start, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		end, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if start > end || start > uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer delete range [%d,%d) exceeds %d",
				start,
				end,
				len(runes),
			)
		}
		if end > uint32(len(runes)) {
			end = uint32(len(runes))
		}
		r.stringBuffers[instance] = string(
			append(runes[:start:start], runes[end:]...),
		)
		return instance, nil
	case "toString()Ljava/lang/String;":
		return r.NewJavaString(r.stringBuffers[instance])
	case "setLength(I)V":
		length, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		switch {
		case length < uint32(len(runes)):
			runes = runes[:length]
		case length > uint32(len(runes)):
			runes = append(runes, make([]rune, length-uint32(len(runes)))...)
		}
		r.stringBuffers[instance] = string(runes)
		return 0, nil
	case "length()I":
		return uint32(len([]rune(r.stringBuffers[instance]))), nil
	case "capacity()I":
		return uint32(len([]rune(r.stringBuffers[instance]))), nil
	case "ensureCapacity(I)V":
		return 0, nil
	case "charAt(I)C":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if index >= uint32(len(runes)) {
			return 0, nil
		}
		return uint32(runes[index]), nil
	case "getChars(II[CI)V":
		sourceBegin, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		sourceEnd, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		array, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		destinationBegin, valueErr := r.parameter(5)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return 0, r.raiseHostJavaException(
				"java/lang/NullPointerException",
			)
		}
		codeUnits := utf16.Encode([]rune(r.stringBuffers[instance]))
		if sourceBegin > sourceEnd || sourceEnd > uint32(len(codeUnits)) {
			return 0, r.raiseHostJavaException(
				"java/lang/IndexOutOfBoundsException",
			)
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		count := sourceEnd - sourceBegin
		if destinationBegin > length || count > length-destinationBegin {
			return 0, r.raiseHostJavaException(
				"java/lang/ArrayIndexOutOfBoundsException",
			)
		}
		if count == 0 {
			return 0, nil
		}
		fields, valueErr := r.ReadU32(array)
		if valueErr != nil {
			return 0, valueErr
		}
		encoded := make([]byte, count*2)
		for index, codeUnit := range codeUnits[sourceBegin:sourceEnd] {
			binary.LittleEndian.PutUint16(encoded[index*2:], codeUnit)
		}
		return 0, r.CPU.WriteMemory(fields+8+destinationBegin*2, encoded)
	case "setCharAt(IC)V":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		character, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if index >= uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer setCharAt index %d exceeds %d",
				index,
				len(runes),
			)
		}
		runes[index] = rune(uint16(character))
		r.stringBuffers[instance] = string(runes)
		return 0, nil
	case "deleteCharAt(I)Ljava/lang/StringBuffer;":
		index, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		runes := []rune(r.stringBuffers[instance])
		if index >= uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer deleteCharAt index %d exceeds %d",
				index,
				len(runes),
			)
		}
		r.stringBuffers[instance] = string(
			append(runes[:index:index], runes[index+1:]...),
		)
		return instance, nil
	case "reverse()Ljava/lang/StringBuffer;":
		runes := []rune(r.stringBuffers[instance])
		for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
			runes[left], runes[right] = runes[right], runes[left]
		}
		r.stringBuffers[instance] = string(runes)
		return instance, nil
	case "insert(ILjava/lang/String;)Ljava/lang/StringBuffer;",
		"insert(ILjava/lang/Object;)Ljava/lang/StringBuffer;",
		"insert(IC)Ljava/lang/StringBuffer;",
		"insert(II)Ljava/lang/StringBuffer;",
		"insert(IZ)Ljava/lang/StringBuffer;",
		"insert(IJ)Ljava/lang/StringBuffer;":
		offset, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		value, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		var inserted string
		switch descriptor {
		case "(ILjava/lang/String;)Ljava/lang/StringBuffer;":
			inserted = r.javaStringValue(value)
		case "(ILjava/lang/Object;)Ljava/lang/StringBuffer;":
			inserted = r.javaObjectString(value)
		case "(IC)Ljava/lang/StringBuffer;":
			inserted = string(rune(uint16(value)))
		case "(II)Ljava/lang/StringBuffer;":
			inserted = fmt.Sprintf("%d", int32(value))
		case "(IZ)Ljava/lang/StringBuffer;":
			inserted = "false"
			if value != 0 {
				inserted = "true"
			}
		default: // (IJ)
			high, highErr := r.parameter(4)
			if highErr != nil {
				return 0, highErr
			}
			inserted = fmt.Sprintf(
				"%d",
				int64(uint64(high)<<32|uint64(value)),
			)
		}
		runes := []rune(r.stringBuffers[instance])
		if offset > uint32(len(runes)) {
			return 0, fmt.Errorf(
				"KTF StringBuffer insert offset %d exceeds %d",
				offset,
				len(runes),
			)
		}
		r.stringBuffers[instance] = string(runes[:offset]) +
			inserted +
			string(runes[offset:])
		return instance, nil
	default:
		r.recordUnimplementedJava("java/lang/StringBuffer", name, descriptor)
		return 0, nil
	}
}

func (r *Runtime) javaStringValue(instance uint32) string {
	if instance == 0 {
		return "null"
	}
	if value, ok := r.JavaStrings[instance]; ok {
		return value
	}
	if value, ok := r.readGuestJavaString(instance); ok {
		return value
	}
	return r.javaObjectString(instance)
}

// javaText returns the contents of a java/lang/String argument, or the empty
// string when the reference is null or is not a string. Unlike
// javaStringValue it never substitutes a diagnostic placeholder, so callers
// that use the result as a resource, class or database name see an absent name
// rather than a fabricated one.
func (r *Runtime) javaText(instance uint32) string {
	if instance == 0 {
		return ""
	}
	if value, ok := r.JavaStrings[instance]; ok {
		return value
	}
	value, _ := r.readGuestJavaString(instance)
	return value
}

// readGuestJavaString decodes a java/lang/String the guest built for itself.
// Host-created strings are memoised in javaStrings, but a title that assembles
// a name through StringBuffer, substring or concat produces an instance the
// host has never seen. Reading its value/offset/count fields is the only way
// those names reach APIs like Class.getResourceAsStream.
func (r *Runtime) readGuestJavaString(instance uint32) (string, bool) {
	words, err := r.ReadWords(instance, 2)
	if err != nil {
		return "", false
	}
	class, err := r.InspectJavaClass(words[1])
	if err != nil || class.Name != "java/lang/String" {
		return "", false
	}
	characters, err := r.readJavaFieldWord(instance, 0)
	if err != nil || characters == 0 {
		return "", false
	}
	offset, err := r.readJavaFieldWord(instance, 4)
	if err != nil {
		return "", false
	}
	count, err := r.readJavaFieldWord(instance, 8)
	if err != nil {
		return "", false
	}
	value, err := r.readJavaCharArrayRange(characters, offset, count)
	if err != nil {
		return "", false
	}
	return value, true
}

func (r *Runtime) javaObjectString(instance uint32) string {
	if instance == 0 {
		return "null"
	}
	if value, ok := r.JavaStrings[instance]; ok {
		return value
	}
	words, err := r.ReadWords(instance, 2)
	if err != nil {
		return fmt.Sprintf("Object@%08x", instance)
	}
	class, err := r.InspectJavaClass(words[1])
	if err != nil {
		return fmt.Sprintf("Object@%08x", instance)
	}
	return fmt.Sprintf("%s@%08x", class.Name, instance)
}

func (r *Runtime) readJavaCharArrayRange(
	instance, offset, count uint32,
) (string, error) {
	if instance == 0 {
		return "", errors.New("KTF Java char array is null")
	}
	fields, err := r.ReadU32(instance)
	if err != nil {
		return "", err
	}
	length, err := r.ReadU32(fields + 4)
	if err != nil {
		return "", err
	}
	if offset > length || count > length-offset {
		return "", fmt.Errorf(
			"KTF Java char array range [%d,%d) exceeds length %d",
			offset,
			offset+count,
			length,
		)
	}
	encoded := make([]byte, count*2)
	if err := r.CPU.ReadMemory(fields+8+offset*2, encoded); err != nil {
		return "", err
	}
	codeUnits := make([]uint16, count)
	for index := range codeUnits {
		codeUnits[index] = binary.LittleEndian.Uint16(encoded[index*2:])
	}
	return string(utf16.Decode(codeUnits)), nil
}
