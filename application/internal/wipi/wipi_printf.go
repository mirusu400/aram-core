package wipi

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type wipiPrintfFormatter struct {
	runtime  *Runtime
	nextWord int
	output   []byte
}

func (r *Runtime) formatPrintf(format []byte, firstWord int) ([]byte, error) {
	formatter := wipiPrintfFormatter{
		runtime:  r,
		nextWord: firstWord,
		output:   make([]byte, 0, len(format)+32),
	}
	for index := 0; index < len(format); {
		if format[index] != '%' {
			if err := formatter.append(format[index : index+1]); err != nil {
				return nil, err
			}
			index++
			continue
		}
		index++
		if index >= len(format) {
			if err := formatter.append([]byte{'%'}); err != nil {
				return nil, err
			}
			break
		}
		if format[index] == '%' {
			if err := formatter.append([]byte{'%'}); err != nil {
				return nil, err
			}
			index++
			continue
		}

		flags := make(map[byte]bool, 5)
		for index < len(format) && strings.ContainsRune("-+ #0", rune(format[index])) {
			flags[format[index]] = true
			index++
		}
		width := -1
		if index < len(format) && format[index] == '*' {
			value, err := formatter.word()
			if err != nil {
				return nil, err
			}
			width = int(int32(value))
			if width < 0 {
				flags['-'] = true
				width = -width
			}
			index++
		} else {
			width, index = parseWIPIFormatNumber(format, index, -1)
		}
		precision := -1
		if index < len(format) && format[index] == '.' {
			index++
			if index < len(format) && format[index] == '*' {
				value, err := formatter.word()
				if err != nil {
					return nil, err
				}
				precision = int(int32(value))
				if precision < 0 {
					precision = -1
				}
				index++
			} else {
				precision, index = parseWIPIFormatNumber(format, index, 0)
			}
		}
		length := ""
		if index < len(format) && strings.ContainsRune("hljztL", rune(format[index])) {
			length = string(format[index])
			index++
			if index < len(format) &&
				((length == "h" && format[index] == 'h') ||
					(length == "l" && format[index] == 'l')) {
				length += string(format[index])
				index++
			}
		}
		if index >= len(format) {
			if err := formatter.append([]byte{'%'}); err != nil {
				return nil, err
			}
			break
		}
		conversion := format[index]
		index++
		rendered, err := formatter.render(conversion, length, flags, width, precision)
		if err != nil {
			return nil, err
		}
		if err := formatter.append(rendered); err != nil {
			return nil, err
		}
	}
	return formatter.output, nil
}

func (f *wipiPrintfFormatter) render(
	conversion byte,
	length string,
	flags map[byte]bool,
	width int,
	precision int,
) ([]byte, error) {
	switch conversion {
	case 'd', 'i':
		value, err := f.signed(length)
		if err != nil {
			return nil, err
		}
		negative := value < 0
		var magnitude uint64
		if negative {
			magnitude = uint64(-(value + 1))
			magnitude++
		} else {
			magnitude = uint64(value)
		}
		sign := ""
		switch {
		case negative:
			sign = "-"
		case flags['+']:
			sign = "+"
		case flags[' ']:
			sign = " "
		}
		digits := formatWIPIIntegerDigits(magnitude, 10, false, precision)
		return []byte(padWIPINumber(
			sign,
			"",
			digits,
			width,
			flags['-'],
			flags['0'] && precision < 0,
		)), nil
	case 'u', 'o', 'x', 'X':
		value, err := f.unsigned(length)
		if err != nil {
			return nil, err
		}
		base := 10
		switch conversion {
		case 'o':
			base = 8
		case 'x', 'X':
			base = 16
		}
		upper := conversion == 'X'
		digits := formatWIPIIntegerDigits(value, base, upper, precision)
		prefix := ""
		if flags['#'] {
			switch conversion {
			case 'o':
				if digits == "" || digits[0] != '0' {
					prefix = "0"
				}
			case 'x':
				if value != 0 {
					prefix = "0x"
				}
			case 'X':
				if value != 0 {
					prefix = "0X"
				}
			}
		}
		return []byte(padWIPINumber(
			"",
			prefix,
			digits,
			width,
			flags['-'],
			flags['0'] && precision < 0,
		)), nil
	case 'p':
		value, err := f.word()
		if err != nil {
			return nil, err
		}
		digitPrecision := precision
		if digitPrecision < 0 {
			digitPrecision = 8
		}
		return []byte(padWIPINumber(
			"",
			"0x",
			formatWIPIIntegerDigits(uint64(value), 16, false, digitPrecision),
			width,
			flags['-'],
			flags['0'],
		)), nil
	case 'c':
		value, err := f.word()
		if err != nil {
			return nil, err
		}
		return []byte(padWIPIText(
			string([]byte{byte(value)}),
			width,
			flags['-'],
		)), nil
	case 's':
		address, err := f.word()
		if err != nil {
			return nil, err
		}
		value := []byte("(null)")
		if address != 0 {
			value, err = f.runtime.ReadCString(address)
			if err != nil {
				return nil, err
			}
		}
		if precision >= 0 && len(value) > precision {
			value = value[:precision]
		}
		return []byte(padWIPIText(string(value), width, flags['-'])), nil
	case 'f', 'F', 'e', 'E', 'g', 'G', 'a', 'A':
		bits, err := f.wide()
		if err != nil {
			return nil, err
		}
		value := math.Float64frombits(bits)
		format := conversion
		if format == 'a' {
			format = 'x'
		} else if format == 'A' {
			format = 'X'
		}
		if precision < 0 {
			precision = 6
		}
		rendered := strconv.FormatFloat(value, format, precision, 64)
		sign := ""
		if strings.HasPrefix(rendered, "-") {
			sign, rendered = "-", rendered[1:]
		} else if flags['+'] {
			sign = "+"
		} else if flags[' '] {
			sign = " "
		}
		if flags['#'] && !strings.ContainsAny(rendered, ".xX") {
			if exponent := strings.IndexAny(rendered, "eE"); exponent >= 0 {
				rendered = rendered[:exponent] + "." + rendered[exponent:]
			} else {
				rendered += "."
			}
		}
		return []byte(padWIPINumber(
			sign,
			"",
			rendered,
			width,
			flags['-'],
			flags['0'],
		)), nil
	case 'n':
		address, err := f.word()
		if err != nil {
			return nil, err
		}
		if address == 0 {
			return nil, nil
		}
		count := uint64(len(f.output))
		var encoded [8]byte
		switch length {
		case "hh":
			return nil, f.runtime.CPU.WriteMemory(address, []byte{byte(count)})
		case "h":
			binary.LittleEndian.PutUint16(encoded[:2], uint16(count))
			return nil, f.runtime.CPU.WriteMemory(address, encoded[:2])
		case "ll", "j":
			binary.LittleEndian.PutUint64(encoded[:], count)
			return nil, f.runtime.CPU.WriteMemory(address, encoded[:])
		default:
			binary.LittleEndian.PutUint32(encoded[:4], uint32(count))
			return nil, f.runtime.CPU.WriteMemory(address, encoded[:4])
		}
	default:
		return []byte{'%', conversion}, nil
	}
}

func (f *wipiPrintfFormatter) word() (uint32, error) {
	value, err := f.runtime.arg(f.nextWord)
	if err == nil {
		f.nextWord++
	}
	return value, err
}

func (f *wipiPrintfFormatter) wide() (uint64, error) {
	if f.nextWord&1 != 0 {
		f.nextWord++
	}
	low, err := f.word()
	if err != nil {
		return 0, err
	}
	high, err := f.word()
	if err != nil {
		return 0, err
	}
	return uint64(low) | uint64(high)<<32, nil
}

func (f *wipiPrintfFormatter) signed(length string) (int64, error) {
	if length == "ll" || length == "j" {
		value, err := f.wide()
		return int64(value), err
	}
	value, err := f.word()
	if err != nil {
		return 0, err
	}
	switch length {
	case "hh":
		return int64(int8(value)), nil
	case "h":
		return int64(int16(value)), nil
	default:
		return int64(int32(value)), nil
	}
}

func (f *wipiPrintfFormatter) unsigned(length string) (uint64, error) {
	if length == "ll" || length == "j" {
		return f.wide()
	}
	value, err := f.word()
	if err != nil {
		return 0, err
	}
	switch length {
	case "hh":
		return uint64(uint8(value)), nil
	case "h":
		return uint64(uint16(value)), nil
	default:
		return uint64(value), nil
	}
}

func (f *wipiPrintfFormatter) append(value []byte) error {
	if uint64(len(f.output))+uint64(len(value)) > uint64(maxWIPIString) {
		return fmt.Errorf("formatted WIPI output exceeds %d bytes", maxWIPIString)
	}
	f.output = append(f.output, value...)
	return nil
}

func parseWIPIFormatNumber(value []byte, index int, fallback int) (int, int) {
	start := index
	result := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		result = result*10 + int(value[index]-'0')
		if result > int(maxWIPIString) {
			result = int(maxWIPIString)
		}
		index++
	}
	if index == start {
		return fallback, index
	}
	return result, index
}

func formatWIPIIntegerDigits(value uint64, base int, upper bool, precision int) string {
	digits := strconv.FormatUint(value, base)
	if upper {
		digits = strings.ToUpper(digits)
	}
	if precision == 0 && value == 0 {
		return ""
	}
	if precision > len(digits) {
		digits = strings.Repeat("0", precision-len(digits)) + digits
	}
	return digits
}

func padWIPINumber(
	sign string,
	prefix string,
	digits string,
	width int,
	left bool,
	zero bool,
) string {
	padding := max(0, width-len(sign)-len(prefix)-len(digits))
	if left {
		return sign + prefix + digits + strings.Repeat(" ", padding)
	}
	if zero {
		return sign + prefix + strings.Repeat("0", padding) + digits
	}
	return strings.Repeat(" ", padding) + sign + prefix + digits
}

func padWIPIText(value string, width int, left bool) string {
	padding := max(0, width-len(value))
	if left {
		return value + strings.Repeat(" ", padding)
	}
	return strings.Repeat(" ", padding) + value
}
