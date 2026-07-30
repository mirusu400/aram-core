package application

import (
	"bytes"
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"time"
)

func (r *wipiRuntime) dispatchCStdlib(name string) (wipiReturn, bool, error) {
	args, err := r.args(4)
	if err != nil {
		return wipiReturn{}, true, err
	}
	a0, a1, a2 := args[0], args[1], args[2]
	switch name {
	case "strlen":
		value, err := r.readCString(a0)
		return wipiReturn{low: uint32(len(value))}, true, err
	case "strcpy", "strncpy":
		value, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		if name == "strcpy" {
			_, err = r.writeCString(a0, value, -1)
		} else {
			count := int(a2)
			output := make([]byte, count)
			copy(output, value)
			err = r.cpu.WriteMemory(a0, output)
		}
		return wipiReturn{low: a0}, true, err
	case "strcat", "strncat":
		current, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		value, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		if name == "strncat" && uint32(len(value)) > a2 {
			value = value[:a2]
		}
		combined := append(append([]byte(nil), current...), value...)
		_, err = r.writeCString(a0, combined, -1)
		return wipiReturn{low: a0}, true, err
	case "strcmp", "strncmp":
		left, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		right, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		if name == "strncmp" {
			left = left[:min(len(left), int(a2))]
			right = right[:min(len(right), int(a2))]
		}
		return wipiReturn{low: uint32(int32(bytes.Compare(left, right)))}, true, nil
	case "strchr", "strrchr":
		value, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		offset := bytes.IndexByte(value, byte(a1))
		if name == "strrchr" {
			offset = bytes.LastIndexByte(value, byte(a1))
		}
		if offset < 0 {
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: a0 + uint32(offset)}, true, nil
	case "strspn", "strcspn":
		value, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		charset, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		count := 0
		for _, current := range value {
			found := bytes.IndexByte(charset, current) >= 0
			if found != (name == "strspn") {
				break
			}
			count++
		}
		return wipiReturn{low: uint32(count)}, true, nil
	case "strpbrk":
		value, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		charset, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		for index, current := range value {
			if bytes.IndexByte(charset, current) >= 0 {
				return wipiReturn{low: a0 + uint32(index)}, true, nil
			}
		}
		return wipiReturn{}, true, nil
	case "strstr":
		haystack, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		needle, err := r.readCString(a1)
		if err != nil {
			return wipiReturn{}, true, err
		}
		offset := bytes.Index(haystack, needle)
		if offset < 0 {
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: a0 + uint32(offset)}, true, nil
	case "strtok":
		return r.strtok(a0, a1)
	case "memcpy", "memmove":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return wipiReturn{}, true, err
		}
		data := make([]byte, size)
		if err := r.cpu.ReadMemory(a1, data); err != nil {
			return wipiReturn{}, true, err
		}
		if err := r.cpu.WriteMemory(a0, data); err != nil {
			return wipiReturn{}, true, err
		}
		return wipiReturn{low: a0}, true, nil
	case "memcmp":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return wipiReturn{}, true, err
		}
		left := make([]byte, size)
		right := make([]byte, size)
		if err := r.cpu.ReadMemory(a0, left); err != nil {
			return wipiReturn{}, true, err
		}
		if err := r.cpu.ReadMemory(a1, right); err != nil {
			return wipiReturn{}, true, err
		}
		return wipiReturn{low: uint32(int32(bytes.Compare(left, right)))}, true, nil
	case "memchr":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return wipiReturn{}, true, err
		}
		data := make([]byte, size)
		if err := r.cpu.ReadMemory(a0, data); err != nil {
			return wipiReturn{}, true, err
		}
		offset := bytes.IndexByte(data, byte(a1))
		if offset < 0 {
			return wipiReturn{}, true, nil
		}
		return wipiReturn{low: a0 + uint32(offset)}, true, nil
	case "memset":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return wipiReturn{}, true, err
		}
		data := bytes.Repeat([]byte{byte(a1)}, size)
		return wipiReturn{low: a0}, true, r.cpu.WriteMemory(a0, data)
	case "atoi", "atoll", "strtol", "strtoul":
		return r.parseInteger(name, a0, a1, a2)
	case "atof", "strtod":
		value, err := r.readCString(a0)
		if err != nil {
			return wipiReturn{}, true, err
		}
		number, _ := strconv.ParseFloat(strings.TrimSpace(string(value)), 64)
		if name == "strtod" && a1 != 0 {
			if err := r.writeU32(a1, a0+uint32(len(value))); err != nil {
				return wipiReturn{}, true, err
			}
		}
		return wipiU64(math.Float64bits(number)), true, nil
	case "clock":
		return wipiReturn{low: uint32(r.tickMS)}, true, nil
	case "time":
		value := uint64(wipiEpochUnix) + r.tickMS/1000
		if a0 != 0 {
			if err := r.writeU64(a0, value); err != nil {
				return wipiReturn{}, true, err
			}
		}
		return wipiU64(value), true, nil
	case "difftime":
		first := uint64(args[0]) | uint64(args[1])<<32
		second := uint64(args[2]) | uint64(args[3])<<32
		difference := float64(int64(first) - int64(second))
		return wipiU64(math.Float64bits(difference)), true, nil
	case "mktime":
		return r.mktime(a0)
	case "localtime", "gmtime":
		return r.breakDownTime(a0)
	default:
		return wipiReturn{}, false, nil
	}
}

func (r *wipiRuntime) strtok(source, delimiterAddress uint32) (wipiReturn, bool, error) {
	cursor := source
	if cursor == 0 {
		cursor = r.strtokNext
	}
	delimiters, err := r.readCString(delimiterAddress)
	if err != nil {
		return wipiReturn{}, true, err
	}
	if cursor == 0 {
		return wipiReturn{}, true, nil
	}
	value, err := r.readCString(cursor)
	if err != nil {
		return wipiReturn{}, true, err
	}
	start := 0
	for start < len(value) && bytes.IndexByte(delimiters, value[start]) >= 0 {
		start++
	}
	if start == len(value) {
		r.strtokNext = 0
		return wipiReturn{}, true, nil
	}
	end := start
	for end < len(value) && bytes.IndexByte(delimiters, value[end]) < 0 {
		end++
	}
	if end < len(value) {
		if err := r.cpu.WriteMemory(cursor+uint32(end), []byte{0}); err != nil {
			return wipiReturn{}, true, err
		}
		r.strtokNext = cursor + uint32(end) + 1
	} else {
		r.strtokNext = 0
	}
	return wipiReturn{low: cursor + uint32(start)}, true, nil
}

func (r *wipiRuntime) parseInteger(name string, source, endPointer, baseValue uint32) (wipiReturn, bool, error) {
	raw, err := r.readCString(source)
	if err != nil {
		return wipiReturn{}, true, err
	}
	base := 10
	if name == "strtol" || name == "strtoul" {
		base = int(int32(baseValue))
	}
	token, consumed, base := integerToken(string(raw), base)
	var value int64
	if token != "" && token != "+" && token != "-" {
		value, _ = strconv.ParseInt(token, base, 64)
		if name == "strtoul" {
			unsigned, parseErr := strconv.ParseUint(strings.TrimPrefix(token, "+"), base, 64)
			if parseErr == nil {
				value = int64(unsigned)
			}
		}
	}
	if (name == "strtol" || name == "strtoul") && endPointer != 0 {
		if err := r.writeU32(endPointer, source+uint32(consumed)); err != nil {
			return wipiReturn{}, true, err
		}
	}
	if name == "atoll" {
		return wipiU64(uint64(value)), true, nil
	}
	return wipiReturn{low: uint32(value)}, true, nil
}

func integerToken(raw string, base int) (string, int, int) {
	leading := len(raw) - len(strings.TrimLeft(raw, " \t\r\n\v\f"))
	text := raw[leading:]
	signLength := 0
	if strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") {
		signLength = 1
	}
	if base == 0 {
		switch {
		case strings.HasPrefix(strings.ToLower(text[signLength:]), "0x"):
			base = 16
		case strings.HasPrefix(text[signLength:], "0"):
			base = 8
		default:
			base = 10
		}
	}
	if base < 2 || base > 36 {
		base = 10
	}
	prefixLength := signLength
	if base == 16 && strings.HasPrefix(strings.ToLower(text[signLength:]), "0x") {
		prefixLength += 2
	}
	end := prefixLength
	for end < len(text) {
		digit := strings.IndexByte("0123456789abcdefghijklmnopqrstuvwxyz", byte(strings.ToLower(text[end : end+1])[0]))
		if digit < 0 || digit >= base {
			break
		}
		end++
	}
	return text[:end], leading + end, base
}

func (r *wipiRuntime) breakDownTime(pointer uint32) (wipiReturn, bool, error) {
	seconds := uint64(wipiEpochUnix) + r.tickMS/1000
	if pointer != 0 {
		low, err := r.readU32(pointer)
		if err != nil {
			return wipiReturn{}, true, err
		}
		high, err := r.readU32(pointer + 4)
		if err != nil {
			return wipiReturn{}, true, err
		}
		seconds = uint64(low) | uint64(high)<<32
	}
	value := time.Unix(int64(seconds), 0).UTC()
	target, err := r.heap.allocate(9*4, true)
	if err != nil || target == 0 {
		return wipiReturn{}, true, err
	}
	yearDay := value.YearDay() - 1
	weekDay := int(value.Weekday())
	fields := [...]int32{
		int32(value.Second()),
		int32(value.Minute()),
		int32(value.Hour()),
		int32(value.Day()),
		int32(value.Month() - 1),
		int32(value.Year() - 1900),
		int32(weekDay),
		int32(yearDay),
		0,
	}
	var encoded [9 * 4]byte
	for index, field := range fields {
		binary.LittleEndian.PutUint32(encoded[index*4:], uint32(field))
	}
	return wipiReturn{low: target}, true, r.cpu.WriteMemory(target, encoded[:])
}

func (r *wipiRuntime) mktime(pointer uint32) (wipiReturn, bool, error) {
	var encoded [9 * 4]byte
	if err := r.cpu.ReadMemory(pointer, encoded[:]); err != nil {
		return wipiReturn{}, true, err
	}
	field := func(index int) int {
		return int(int32(binary.LittleEndian.Uint32(encoded[index*4:])))
	}
	value := time.Date(
		field(5)+1900,
		time.Month(field(4)+1),
		field(3),
		field(2),
		field(1),
		field(0),
		0,
		time.UTC,
	).Unix()
	return wipiU64(uint64(value)), true, nil
}
