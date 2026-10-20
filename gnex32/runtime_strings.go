package gnex32

import (
	"bytes"
	"fmt"
	"strconv"
)

const maxGNEX32FormattedBytes = 256

func (r *Runtime) readString(index int) ([]byte, error) {
	data, _, err := r.vm.media.Read(index)
	if err != nil {
		return nil, err
	}
	if end := bytes.IndexByte(data, 0); end >= 0 {
		data = data[:end]
	}
	return data, nil
}

func gnex32ByteOffset(ref gnex32SymbolRef, index uint32) (int, error) {
	offset := int64(ref.element)*4 + int64(int32(index))
	if offset < 0 || int64(int(offset)) != offset {
		return 0, fmt.Errorf("gnex32: symbol byte offset %d is out of range", offset)
	}
	return int(offset), nil
}

func formatGNEX32Integers(format []byte, values []uint32) ([]byte, error) {
	result := make([]byte, 0, min(len(format)+16*len(values), maxGNEX32FormattedBytes))
	valueIndex := 0
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			result = append(result, format[index])
			if len(result)+1 > maxGNEX32FormattedBytes {
				return nil, fmt.Errorf("gnex32: formatted string exceeds %d bytes", maxGNEX32FormattedBytes)
			}
			continue
		}
		index++
		if index >= len(format) {
			return nil, fmt.Errorf("gnex32: incomplete MakeStr format")
		}
		if format[index] == '%' {
			result = append(result, '%')
			if len(result)+1 > maxGNEX32FormattedBytes {
				return nil, fmt.Errorf("gnex32: formatted string exceeds %d bytes", maxGNEX32FormattedBytes)
			}
			continue
		}
		zeroPad := format[index] == '0'
		width := 0
		for index < len(format) && format[index] >= '0' && format[index] <= '9' {
			width = width*10 + int(format[index]-'0')
			if width > maxGNEX32FormattedBytes {
				return nil, fmt.Errorf("gnex32: MakeStr width exceeds limit")
			}
			index++
		}
		if index >= len(format) || valueIndex >= len(values) {
			return nil, fmt.Errorf("gnex32: MakeStr argument count mismatch")
		}
		value := values[valueIndex]
		valueIndex++
		var field []byte
		switch format[index] {
		case 'd':
			field = strconv.AppendInt(nil, int64(int32(value)), 10)
		case 'x':
			field = strconv.AppendUint(nil, uint64(value), 16)
		case 'X':
			field = bytes.ToUpper(strconv.AppendUint(nil, uint64(value), 16))
		case 'c':
			field = []byte{byte(value)}
		default:
			return nil, fmt.Errorf("gnex32: unsupported MakeStr format %%%c", format[index])
		}
		padding := max(0, width-len(field))
		if zeroPad && len(field) != 0 && field[0] == '-' {
			result = append(result, '-')
			field = field[1:]
		}
		pad := byte(' ')
		if zeroPad {
			pad = '0'
		}
		result = append(result, bytes.Repeat([]byte{pad}, padding)...)
		result = append(result, field...)
		if len(result)+1 > maxGNEX32FormattedBytes {
			return nil, fmt.Errorf("gnex32: formatted string exceeds %d bytes", maxGNEX32FormattedBytes)
		}
	}
	if valueIndex != len(values) {
		return nil, fmt.Errorf("gnex32: MakeStr argument count mismatch")
	}
	return append(result, 0), nil
}

func formatGNEX32String(format, value []byte) ([]byte, error) {
	result := make([]byte, 0, min(len(format)+len(value), maxGNEX32FormattedBytes))
	replaced := false
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			result = append(result, format[index])
		} else if index+1 < len(format) && format[index+1] == '%' {
			result = append(result, '%')
			index++
		} else if index+1 < len(format) && format[index+1] == 's' && !replaced {
			result = append(result, value...)
			replaced = true
			index++
		} else {
			return nil, fmt.Errorf("gnex32: unsupported MakeStrStr format")
		}
		if len(result)+1 > maxGNEX32FormattedBytes {
			return nil, fmt.Errorf("gnex32: formatted string exceeds %d bytes", maxGNEX32FormattedBytes)
		}
	}
	if !replaced {
		return nil, fmt.Errorf("gnex32: MakeStrStr requires one %%s")
	}
	return append(result, 0), nil
}

func parseGNEX32Decimal(data []byte) int32 {
	index := 0
	for index < len(data) && (data[index] == ' ' || data[index] == '\t' || data[index] == '\r' || data[index] == '\n') {
		index++
	}
	start := index
	if index < len(data) && (data[index] == '+' || data[index] == '-') {
		index++
	}
	digits := index
	for index < len(data) && data[index] >= '0' && data[index] <= '9' {
		index++
	}
	if index == digits {
		return 0
	}
	value, err := strconv.ParseInt(string(data[start:index]), 10, 32)
	if err != nil {
		if data[start] == '-' {
			return -1 << 31
		}
		return 1<<31 - 1
	}
	return int32(value)
}
