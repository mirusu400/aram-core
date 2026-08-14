package skvm

import (
	"encoding/binary"
	"fmt"
)

func category2(value Value) bool {
	return value.Kind == ValueLong || value.Kind == ValueDouble
}

func (f *frame) push(value Value) {
	f.stack = append(f.stack, value)
}

func (f *frame) pop() (Value, error) {
	if len(f.stack) == 0 {
		return Value{}, fmt.Errorf("SKVM operand stack underflow")
	}
	index := len(f.stack) - 1
	value := f.stack[index]
	f.stack = f.stack[:index]
	return value, nil
}

func (f *frame) pop2() (Value, Value, error) {
	first, err := f.pop()
	if err != nil {
		return Value{}, Value{}, err
	}
	second, err := f.pop()
	if err != nil {
		return Value{}, Value{}, err
	}
	return first, second, nil
}

func (f *frame) popSlotGroup(slots int) ([]Value, error) {
	var reversed []Value
	used := 0
	for used < slots {
		value, err := f.pop()
		if err != nil {
			return nil, err
		}
		reversed = append(reversed, value)
		if category2(value) {
			used += 2
		} else {
			used++
		}
	}
	if used != slots {
		return nil, fmt.Errorf("SKVM stack has invalid category shape")
	}
	values := make([]Value, len(reversed))
	for index := range reversed {
		values[len(reversed)-1-index] = reversed[index]
	}
	return values, nil
}

func (f *frame) popInt() (int32, error) {
	value, err := f.pop()
	if err != nil {
		return 0, err
	}
	return value.Int()
}

func (f *frame) popInts() (int32, int32, error) {
	right, err := f.popInt()
	if err != nil {
		return 0, 0, err
	}
	left, err := f.popInt()
	if err != nil {
		return 0, 0, err
	}
	return right, left, nil
}

func (f *frame) peek(depth int) (Value, error) {
	index := len(f.stack) - 1 - depth
	if index < 0 {
		return Value{}, fmt.Errorf("SKVM operand stack underflow")
	}
	return f.stack[index], nil
}

func (f *frame) local(index int) (Value, error) {
	if index < 0 || index >= len(f.locals) {
		return Value{}, fmt.Errorf("SKVM local index %d is out of range", index)
	}
	if f.locals[index].Kind == ValueTop {
		return Value{}, fmt.Errorf("SKVM local index %d is uninitialized", index)
	}
	return f.locals[index], nil
}

func (f *frame) setLocal(index int, value Value) error {
	if index < 0 || index >= len(f.locals) {
		return fmt.Errorf("SKVM local index %d is out of range", index)
	}
	f.locals[index] = value
	if category2(value) {
		if index+1 >= len(f.locals) {
			return fmt.Errorf("SKVM category-2 local exceeds method locals")
		}
		f.locals[index+1] = Value{Kind: ValueTop}
	}
	return nil
}

func (f *frame) loadLocal(index int) error {
	value, err := f.local(index)
	if err != nil {
		return err
	}
	f.push(value)
	return nil
}

func (f *frame) storeLocal(index int) error {
	value, err := f.pop()
	if err != nil {
		return err
	}
	return f.setLocal(index, value)
}

func (f *frame) readU1() (byte, error) {
	if f.pc >= len(f.method.Code) {
		return 0, fmt.Errorf("SKVM bytecode operand is truncated")
	}
	value := f.method.Code[f.pc]
	f.pc++
	return value, nil
}

func (f *frame) readU2() (uint16, error) {
	if f.pc > len(f.method.Code)-2 {
		return 0, fmt.Errorf("SKVM bytecode operand is truncated")
	}
	value := binary.BigEndian.Uint16(f.method.Code[f.pc : f.pc+2])
	f.pc += 2
	return value, nil
}

func (f *frame) readI4() (int32, error) {
	if f.pc > len(f.method.Code)-4 {
		return 0, fmt.Errorf("SKVM bytecode operand is truncated")
	}
	value := int32(binary.BigEndian.Uint32(f.method.Code[f.pc : f.pc+4]))
	f.pc += 4
	return value, nil
}

func (f *frame) branch(base int, offset int16) error {
	return f.jump(base + int(offset))
}

func (f *frame) branch32(base int, offset int32) error {
	target := int64(base) + int64(offset)
	if target < 0 || target > int64(len(f.method.Code)) {
		return fmt.Errorf("SKVM branch target 0x%x is out of range", target)
	}
	return f.jump(int(target))
}

func (f *frame) jump(target int) error {
	if target < 0 || target >= len(f.method.Code) {
		return fmt.Errorf("SKVM branch target 0x%x is out of range", target)
	}
	f.pc = target
	return nil
}
