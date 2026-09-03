package wipi

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// dispatchCStdlib runs a CSTDLIB call and, when it fails, names the arguments
// the guest passed and where it called from. A CSTDLIB failure is nearly
// always a length or a pointer the guest computed somewhere else - 부루마불
// 2009 reached memcpy with a 64 MiB length (issue #139) - and the refused
// value on its own says nothing about where to look for it.
func (r *Runtime) dispatchCStdlib(name string) (guest.WIPIReturn, bool, error) {
	value, handled, err := r.cStdlib(name)
	if err == nil {
		return value, handled, nil
	}
	args, argErr := r.args(4)
	if argErr != nil {
		return value, handled, err
	}
	lr, _ := r.CPU.ReadRegister(cpu.RegisterLR)
	return value, handled, fmt.Errorf(
		"(0x%08x, 0x%08x, 0x%08x, 0x%08x) from lr 0x%08x: %w",
		args[0],
		args[1],
		args[2],
		args[3],
		lr,
		err,
	)
}

func (r *Runtime) cStdlib(name string) (guest.WIPIReturn, bool, error) {
	args, err := r.args(4)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	a0, a1, a2 := args[0], args[1], args[2]
	switch name {
	case "strlen":
		value, err := r.ReadCString(a0)
		return guest.WIPIReturn{Low: uint32(len(value))}, true, err
	case "strcpy", "strncpy":
		value, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if name == "strcpy" {
			_, err = r.writeCString(a0, value, -1)
		} else {
			count := int(a2)
			output := make([]byte, count)
			copy(output, value)
			err = r.CPU.WriteMemory(a0, output)
		}
		return guest.WIPIReturn{Low: a0}, true, err
	case "strcat", "strncat":
		current, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		value, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if name == "strncat" && uint32(len(value)) > a2 {
			value = value[:a2]
		}
		combined := append(append([]byte(nil), current...), value...)
		_, err = r.writeCString(a0, combined, -1)
		return guest.WIPIReturn{Low: a0}, true, err
	case "strcmp", "strncmp":
		left, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		right, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if name == "strncmp" {
			left = left[:min(len(left), int(a2))]
			right = right[:min(len(right), int(a2))]
		}
		return guest.WIPIReturn{Low: uint32(int32(bytes.Compare(left, right)))}, true, nil
	case "strchr", "strrchr":
		value, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		offset := bytes.IndexByte(value, byte(a1))
		if name == "strrchr" {
			offset = bytes.LastIndexByte(value, byte(a1))
		}
		if offset < 0 {
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: a0 + uint32(offset)}, true, nil
	case "strspn", "strcspn":
		value, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		charset, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		count := 0
		for _, current := range value {
			found := bytes.IndexByte(charset, current) >= 0
			if found != (name == "strspn") {
				break
			}
			count++
		}
		return guest.WIPIReturn{Low: uint32(count)}, true, nil
	case "strpbrk":
		value, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		charset, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		for index, current := range value {
			if bytes.IndexByte(charset, current) >= 0 {
				return guest.WIPIReturn{Low: a0 + uint32(index)}, true, nil
			}
		}
		return guest.WIPIReturn{}, true, nil
	case "strstr":
		haystack, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		needle, err := r.ReadCString(a1)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		offset := bytes.Index(haystack, needle)
		if offset < 0 {
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: a0 + uint32(offset)}, true, nil
	case "strtok":
		return r.strtok(a0, a1)
	case "memcpy", "memmove":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		data := make([]byte, size)
		if err := r.CPU.ReadMemory(a1, data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if err := r.CPU.WriteMemory(a0, data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{Low: a0}, true, nil
	case "memcmp":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		left := make([]byte, size)
		right := make([]byte, size)
		if err := r.CPU.ReadMemory(a0, left); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		if err := r.CPU.ReadMemory(a1, right); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		return guest.WIPIReturn{Low: uint32(int32(bytes.Compare(left, right)))}, true, nil
	case "memchr":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		data := make([]byte, size)
		if err := r.CPU.ReadMemory(a0, data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		offset := bytes.IndexByte(data, byte(a1))
		if offset < 0 {
			return guest.WIPIReturn{}, true, nil
		}
		return guest.WIPIReturn{Low: a0 + uint32(offset)}, true, nil
	case "memset":
		size, err := checkedWIPISize(a2)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		data := bytes.Repeat([]byte{byte(a1)}, size)
		return guest.WIPIReturn{Low: a0}, true, r.CPU.WriteMemory(a0, data)
	case "atoi", "atoll", "strtol", "strtoul":
		return r.parseInteger(name, a0, a1, a2)
	case "atof", "strtod":
		value, err := r.ReadCString(a0)
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		number, _ := strconv.ParseFloat(strings.TrimSpace(string(value)), 64)
		if name == "strtod" && a1 != 0 {
			if err := r.WriteU32(a1, a0+uint32(len(value))); err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		return wipiU64(math.Float64bits(number)), true, nil
	case "clock":
		return guest.WIPIReturn{
			Low: uint32(r.Services.Clock.Monotonic() / time.Millisecond),
		}, true, nil
	case "time":
		value := uint64(r.Services.Clock.WallMillis() / 1000)
		if a0 != 0 {
			if err := r.writeU64(a0, value); err != nil {
				return guest.WIPIReturn{}, true, err
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
		return guest.WIPIReturn{}, false, nil
	}
}

func (r *Runtime) strtok(source, delimiterAddress uint32) (guest.WIPIReturn, bool, error) {
	cursor := source
	if cursor == 0 {
		cursor = r.strtokNext
	}
	delimiters, err := r.ReadCString(delimiterAddress)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	if cursor == 0 {
		return guest.WIPIReturn{}, true, nil
	}
	value, err := r.ReadCString(cursor)
	if err != nil {
		return guest.WIPIReturn{}, true, err
	}
	start := 0
	for start < len(value) && bytes.IndexByte(delimiters, value[start]) >= 0 {
		start++
	}
	if start == len(value) {
		r.strtokNext = 0
		return guest.WIPIReturn{}, true, nil
	}
	end := start
	for end < len(value) && bytes.IndexByte(delimiters, value[end]) < 0 {
		end++
	}
	if end < len(value) {
		if err := r.CPU.WriteMemory(cursor+uint32(end), []byte{0}); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		r.strtokNext = cursor + uint32(end) + 1
	} else {
		r.strtokNext = 0
	}
	return guest.WIPIReturn{Low: cursor + uint32(start)}, true, nil
}

func (r *Runtime) parseInteger(name string, source, endPointer, baseValue uint32) (guest.WIPIReturn, bool, error) {
	raw, err := r.ReadCString(source)
	if err != nil {
		return guest.WIPIReturn{}, true, err
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
		if err := r.WriteU32(endPointer, source+uint32(consumed)); err != nil {
			return guest.WIPIReturn{}, true, err
		}
	}
	if name == "atoll" {
		return wipiU64(uint64(value)), true, nil
	}
	return guest.WIPIReturn{Low: uint32(value)}, true, nil
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
