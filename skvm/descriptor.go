package skvm

import "fmt"

type valueType struct {
	kind       byte
	class      string
	dimensions int
}

func (t valueType) slots() int {
	if t.dimensions == 0 && (t.kind == 'J' || t.kind == 'D') {
		return 2
	}
	if t.kind == 'V' {
		return 0
	}
	return 1
}

func (t valueType) valueKind() ValueKind {
	if t.dimensions != 0 || t.kind == 'L' {
		return ValueReference
	}
	switch t.kind {
	case 'J':
		return ValueLong
	case 'F':
		return ValueFloat
	case 'D':
		return ValueDouble
	case 'V':
		return ValueTop
	default:
		return ValueInt
	}
}

func parseMethodDescriptor(descriptor string) ([]valueType, valueType, error) {
	if len(descriptor) == 0 || descriptor[0] != '(' {
		return nil, valueType{}, fmt.Errorf("invalid method descriptor %q", descriptor)
	}
	offset := 1
	var parameters []valueType
	for offset < len(descriptor) && descriptor[offset] != ')' {
		parameter, next, err := parseValueType(descriptor, offset, false)
		if err != nil {
			return nil, valueType{}, err
		}
		parameters = append(parameters, parameter)
		offset = next
	}
	if offset >= len(descriptor) || descriptor[offset] != ')' {
		return nil, valueType{}, fmt.Errorf("invalid method descriptor %q", descriptor)
	}
	result, next, err := parseValueType(descriptor, offset+1, true)
	if err != nil {
		return nil, valueType{}, err
	}
	if next != len(descriptor) {
		return nil, valueType{}, fmt.Errorf("trailing method descriptor data %q", descriptor[next:])
	}
	return parameters, result, nil
}

func parseFieldDescriptor(descriptor string) (valueType, error) {
	value, next, err := parseValueType(descriptor, 0, false)
	if err != nil {
		return valueType{}, err
	}
	if next != len(descriptor) {
		return valueType{}, fmt.Errorf("trailing field descriptor data %q", descriptor[next:])
	}
	return value, nil
}

func parseValueType(descriptor string, offset int, allowVoid bool) (valueType, int, error) {
	start := offset
	dimensions := 0
	for offset < len(descriptor) && descriptor[offset] == '[' {
		dimensions++
		offset++
	}
	if offset >= len(descriptor) {
		return valueType{}, start, fmt.Errorf("truncated descriptor %q", descriptor)
	}
	kind := descriptor[offset]
	offset++
	switch kind {
	case 'B', 'C', 'D', 'F', 'I', 'J', 'S', 'Z':
	case 'V':
		if !allowVoid || dimensions != 0 {
			return valueType{}, start, fmt.Errorf("invalid void descriptor %q", descriptor)
		}
	case 'L':
		end := offset
		for end < len(descriptor) && descriptor[end] != ';' {
			end++
		}
		if end == offset || end >= len(descriptor) {
			return valueType{}, start, fmt.Errorf("invalid class descriptor %q", descriptor)
		}
		class := descriptor[offset:end]
		return valueType{kind: kind, class: class, dimensions: dimensions}, end + 1, nil
	default:
		return valueType{}, start, fmt.Errorf("unknown descriptor type %q", kind)
	}
	return valueType{kind: kind, dimensions: dimensions}, offset, nil
}

func zeroValue(descriptor string) (Value, error) {
	valueType, err := parseFieldDescriptor(descriptor)
	if err != nil {
		return Value{}, err
	}
	switch valueType.valueKind() {
	case ValueReference:
		return ReferenceValue(0), nil
	case ValueLong:
		return LongValue(0), nil
	case ValueFloat:
		return FloatValue(0), nil
	case ValueDouble:
		return DoubleValue(0), nil
	default:
		return IntValue(0), nil
	}
}
