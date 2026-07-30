package skvm

import (
	"fmt"
	"math"
)

type ValueKind uint8

const (
	ValueTop ValueKind = iota
	ValueInt
	ValueLong
	ValueFloat
	ValueDouble
	ValueReference
	ValueReturnAddress
)

type Value struct {
	Kind ValueKind
	bits uint64
}

func IntValue(value int32) Value {
	return Value{Kind: ValueInt, bits: uint64(uint32(value))}
}

func LongValue(value int64) Value {
	return Value{Kind: ValueLong, bits: uint64(value)}
}

func FloatValue(value float32) Value {
	return Value{Kind: ValueFloat, bits: uint64(math.Float32bits(value))}
}

func DoubleValue(value float64) Value {
	return Value{Kind: ValueDouble, bits: math.Float64bits(value)}
}

func ReferenceValue(reference uint32) Value {
	return Value{Kind: ValueReference, bits: uint64(reference)}
}

func ReturnAddressValue(pc uint32) Value {
	return Value{Kind: ValueReturnAddress, bits: uint64(pc)}
}

func (v Value) Int() (int32, error) {
	if v.Kind != ValueInt {
		return 0, fmt.Errorf("value is %s, not int", v.Kind)
	}
	return int32(uint32(v.bits)), nil
}

func (v Value) Long() (int64, error) {
	if v.Kind != ValueLong {
		return 0, fmt.Errorf("value is %s, not long", v.Kind)
	}
	return int64(v.bits), nil
}

func (v Value) Float() (float32, error) {
	if v.Kind != ValueFloat {
		return 0, fmt.Errorf("value is %s, not float", v.Kind)
	}
	return math.Float32frombits(uint32(v.bits)), nil
}

func (v Value) Double() (float64, error) {
	if v.Kind != ValueDouble {
		return 0, fmt.Errorf("value is %s, not double", v.Kind)
	}
	return math.Float64frombits(v.bits), nil
}

func (v Value) Reference() (uint32, error) {
	if v.Kind != ValueReference {
		return 0, fmt.Errorf("value is %s, not reference", v.Kind)
	}
	return uint32(v.bits), nil
}

func (v Value) ReturnAddress() (uint32, error) {
	if v.Kind != ValueReturnAddress {
		return 0, fmt.Errorf("value is %s, not return address", v.Kind)
	}
	return uint32(v.bits), nil
}

func (v Value) IsNull() bool {
	return v.Kind == ValueReference && v.bits == 0
}

func (v Value) String() string {
	switch v.Kind {
	case ValueTop:
		return "<top>"
	case ValueInt:
		return fmt.Sprintf("%d", int32(uint32(v.bits)))
	case ValueLong:
		return fmt.Sprintf("%dL", int64(v.bits))
	case ValueFloat:
		return fmt.Sprintf("%gF", math.Float32frombits(uint32(v.bits)))
	case ValueDouble:
		return fmt.Sprintf("%gD", math.Float64frombits(v.bits))
	case ValueReference:
		return fmt.Sprintf("ref:%d", uint32(v.bits))
	case ValueReturnAddress:
		return fmt.Sprintf("ret:%d", uint32(v.bits))
	default:
		return fmt.Sprintf("<kind:%d>", v.Kind)
	}
}

func (k ValueKind) String() string {
	switch k {
	case ValueTop:
		return "top"
	case ValueInt:
		return "int"
	case ValueLong:
		return "long"
	case ValueFloat:
		return "float"
	case ValueDouble:
		return "double"
	case ValueReference:
		return "reference"
	case ValueReturnAddress:
		return "return-address"
	default:
		return fmt.Sprintf("kind-%d", k)
	}
}
