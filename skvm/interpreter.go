package skvm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type UnsupportedOpcodeError struct {
	Class      string
	Method     string
	Descriptor string
	PC         int
	Opcode     byte
}

func (e *UnsupportedOpcodeError) Error() string {
	return fmt.Sprintf(
		"SKVM unsupported opcode 0x%02x in %s.%s%s at PC 0x%x",
		e.Opcode,
		e.Class,
		e.Method,
		e.Descriptor,
		e.PC,
	)
}

type stepResult struct {
	value    Value
	hasValue bool
	returned bool
}

func (vm *VM) execute(
	ctx context.Context,
	class *Class,
	method Method,
	receiver uint32,
	args []Value,
	budget *uint64,
) (Value, bool, error) {
	parameters, _, err := parseMethodDescriptor(method.Descriptor)
	if err != nil {
		return Value{}, false, err
	}
	if len(parameters) != len(args) {
		return Value{}, false, fmt.Errorf(
			"SKVM call %s.%s%s got %d arguments, want %d",
			class.Name,
			method.Name,
			method.Descriptor,
			len(args),
			len(parameters),
		)
	}
	if method.Native() || method.Abstract() {
		return Value{}, false, fmt.Errorf(
			"SKVM cannot directly execute %s.%s%s",
			class.Name,
			method.Name,
			method.Descriptor,
		)
	}
	locals := make([]Value, method.MaxLocals)
	localIndex := 0
	if !method.Static() {
		if len(locals) == 0 {
			return Value{}, false, fmt.Errorf("SKVM instance method has no receiver local")
		}
		locals[0] = ReferenceValue(receiver)
		localIndex = 1
	}
	for index, argument := range args {
		if argument.Kind != parameters[index].valueKind() {
			return Value{}, false, fmt.Errorf(
				"SKVM argument %d to %s.%s%s is %s, want %s",
				index,
				class.Name,
				method.Name,
				method.Descriptor,
				argument.Kind,
				parameters[index].valueKind(),
			)
		}
		if localIndex >= len(locals) {
			return Value{}, false, fmt.Errorf("SKVM arguments exceed method locals")
		}
		locals[localIndex] = argument
		localIndex += parameters[index].slots()
	}
	current := &frame{
		class:    class,
		method:   method,
		locals:   locals,
		stack:    make([]Value, 0, method.MaxStack),
		invokePC: -1,
	}
	return vm.executeFrame(ctx, current, budget)
}

func (vm *VM) executeFrame(
	ctx context.Context,
	current *frame,
	budget *uint64,
) (Value, bool, error) {
	vm.frames = append(vm.frames, current)
	defer func() {
		vm.frames = vm.frames[:len(vm.frames)-1]
	}()
	return vm.runFrame(ctx, current, budget)
}

func (vm *VM) runFrame(
	ctx context.Context,
	current *frame,
	budget *uint64,
) (Value, bool, error) {
	_, resultType, err := parseMethodDescriptor(current.method.Descriptor)
	if err != nil {
		return Value{}, false, err
	}
	for {
		opcodePC := current.pc
		result, stepErr := vm.step(ctx, current, budget)
		if stepErr != nil {
			var yielded *threadYield
			if errors.As(stepErr, &yielded) {
				if err := vm.captureThreadContinuation(); err != nil {
					return Value{}, false, err
				}
				return Value{}, false, stepErr
			}
			var exception *thrown
			if !AsThrown(stepErr, &exception) {
				return Value{}, false, stepErr
			}
			current.invokePC = -1
			if vm.handleThrown(current, opcodePC, exception) {
				continue
			}
			return Value{}, false, stepErr
		}
		if !result.returned {
			continue
		}
		if resultType.kind == 'V' {
			if result.hasValue {
				return Value{}, false, fmt.Errorf("SKVM void method returned a value")
			}
			return Value{}, false, nil
		}
		if !result.hasValue || result.value.Kind != resultType.valueKind() {
			return Value{}, false, fmt.Errorf(
				"SKVM %s.%s%s returned %s, want %s",
				current.class.Name,
				current.method.Name,
				current.method.Descriptor,
				result.value.Kind,
				resultType.valueKind(),
			)
		}
		return result.value, true, nil
	}
}

func (vm *VM) handleThrown(current *frame, opcodePC int, exception *thrown) bool {
	for _, handler := range current.method.Handlers {
		if opcodePC < int(handler.StartPC) || opcodePC >= int(handler.EndPC) ||
			!vm.throwableMatches(exception.reference, handler.CatchType) {
			continue
		}
		current.stack = current.stack[:0]
		current.stack = append(current.stack, ReferenceValue(exception.reference))
		current.pc = int(handler.HandlerPC)
		return true
	}
	return false
}

func (vm *VM) captureThreadContinuation() error {
	if vm.runningThread == 0 {
		return nil
	}
	state, err := vm.thread(vm.runningThread)
	if err != nil {
		return err
	}
	if vm.threadFrameBase < 0 || vm.threadFrameBase >= len(vm.frames) {
		return fmt.Errorf("SKVM thread has no resumable frame")
	}
	current := vm.frames[vm.threadFrameBase:]
	if len(current) <= len(state.continuation) {
		return nil
	}
	state.continuation = cloneFrames(current)
	return nil
}

func cloneFrames(frames []*frame) []*frame {
	cloned := make([]*frame, len(frames))
	for index, current := range frames {
		cloned[index] = &frame{
			class:    current.class,
			method:   current.method,
			locals:   append([]Value(nil), current.locals...),
			stack:    append([]Value(nil), current.stack...),
			pc:       current.pc,
			invokePC: current.invokePC,
		}
	}
	return cloned
}

func (vm *VM) resumeFrames(
	ctx context.Context,
	frames []*frame,
	index int,
	budget *uint64,
) (Value, bool, error) {
	if index < 0 || index >= len(frames) {
		return Value{}, false, fmt.Errorf("SKVM invalid thread continuation")
	}
	current := frames[index]
	vm.frames = append(vm.frames, current)
	defer func() {
		vm.frames = vm.frames[:len(vm.frames)-1]
	}()

	if index+1 < len(frames) {
		value, hasValue, err := vm.resumeFrames(ctx, frames, index+1, budget)
		if err != nil {
			var exception *thrown
			if !AsThrown(err, &exception) ||
				!vm.handleThrown(current, current.invokePC, exception) {
				return Value{}, false, err
			}
		} else if hasValue {
			current.push(value)
		}
	}
	current.invokePC = -1
	return vm.runFrame(ctx, current, budget)
}

func AsThrown(err error, target **thrown) bool {
	exception, ok := err.(*thrown)
	if ok {
		*target = exception
	}
	return ok
}

func (vm *VM) step(
	ctx context.Context,
	current *frame,
	budget *uint64,
) (stepResult, error) {
	if *budget == 0 {
		return stepResult{}, ErrInstructionLimit
	}
	if current.pc < 0 || current.pc >= len(current.method.Code) {
		return stepResult{}, fmt.Errorf(
			"SKVM PC 0x%x is outside %s.%s%s",
			current.pc,
			current.class.Name,
			current.method.Name,
			current.method.Descriptor,
		)
	}
	select {
	case <-ctx.Done():
		return stepResult{}, ctx.Err()
	default:
	}
	opcodePC := current.pc
	opcode := current.method.Code[current.pc]
	current.pc++
	*budget--
	vm.Instructions++
	if vm.hook != nil {
		if err := vm.hook(TraceEvent{
			Class:      current.class.Name,
			Method:     current.method.Name,
			Descriptor: current.method.Descriptor,
			PC:         opcodePC,
			Opcode:     opcode,
			Depth:      len(vm.frames),
			Target:     traceTarget(current.class, current.method.Code, opcodePC, opcode),
		}); err != nil {
			return stepResult{}, err
		}
	}

	switch opcode {
	case 0x00: // nop
	case 0x01: // aconst_null
		current.push(ReferenceValue(0))
	case 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08: // iconst_m1..iconst_5
		current.push(IntValue(int32(opcode) - 3))
	case 0x09, 0x0a: // lconst_0..lconst_1
		current.push(LongValue(int64(opcode - 0x09)))
	case 0x0b, 0x0c, 0x0d: // fconst_0..fconst_2
		current.push(FloatValue(float32(opcode - 0x0b)))
	case 0x0e, 0x0f: // dconst_0..dconst_1
		current.push(DoubleValue(float64(opcode - 0x0e)))
	case 0x10: // bipush
		value, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(int32(int8(value))))
	case 0x11: // sipush
		value, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(int32(int16(value))))
	case 0x12, 0x13, 0x14: // ldc, ldc_w, ldc2_w
		var index uint16
		var err error
		if opcode == 0x12 {
			var short byte
			short, err = current.readU1()
			index = uint16(short)
		} else {
			index, err = current.readU2()
		}
		if err != nil {
			return stepResult{}, err
		}
		constant, err := current.class.Constant(index)
		if err != nil {
			return stepResult{}, err
		}
		value, err := vm.constantValue(constant)
		if err != nil {
			return stepResult{}, err
		}
		current.push(value)

	case 0x15, 0x16, 0x17, 0x18, 0x19: // xload
		index, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.local(int(index))
		if err != nil {
			return stepResult{}, err
		}
		current.push(value)
	case 0x1a, 0x1b, 0x1c, 0x1d: // iload_0..3
		return stepResult{}, current.loadLocal(int(opcode - 0x1a))
	case 0x1e, 0x1f, 0x20, 0x21: // lload_0..3
		return stepResult{}, current.loadLocal(int(opcode - 0x1e))
	case 0x22, 0x23, 0x24, 0x25: // fload_0..3
		return stepResult{}, current.loadLocal(int(opcode - 0x22))
	case 0x26, 0x27, 0x28, 0x29: // dload_0..3
		return stepResult{}, current.loadLocal(int(opcode - 0x26))
	case 0x2a, 0x2b, 0x2c, 0x2d: // aload_0..3
		return stepResult{}, current.loadLocal(int(opcode - 0x2a))

	case 0x2e, 0x2f, 0x30, 0x31, 0x32, 0x33, 0x34, 0x35: // xaload
		index, array, err := vm.popArrayIndex(current)
		if err != nil {
			return stepResult{}, err
		}
		value := array.Elements[index]
		switch opcode {
		case 0x33: // baload
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			if array.Descriptor == "[Z" {
				integer &= 1
			} else {
				integer = int32(int8(integer))
			}
			value = IntValue(integer)
		case 0x34: // caload
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			value = IntValue(integer & 0xffff)
		case 0x35: // saload
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			value = IntValue(int32(int16(integer)))
		}
		current.push(value)

	case 0x36, 0x37, 0x38, 0x39, 0x3a: // xstore
		index, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		return stepResult{}, current.setLocal(int(index), value)
	case 0x3b, 0x3c, 0x3d, 0x3e: // istore_0..3
		return stepResult{}, current.storeLocal(int(opcode - 0x3b))
	case 0x3f, 0x40, 0x41, 0x42: // lstore_0..3
		return stepResult{}, current.storeLocal(int(opcode - 0x3f))
	case 0x43, 0x44, 0x45, 0x46: // fstore_0..3
		return stepResult{}, current.storeLocal(int(opcode - 0x43))
	case 0x47, 0x48, 0x49, 0x4a: // dstore_0..3
		return stepResult{}, current.storeLocal(int(opcode - 0x47))
	case 0x4b, 0x4c, 0x4d, 0x4e: // astore_0..3
		return stepResult{}, current.storeLocal(int(opcode - 0x4b))

	case 0x4f, 0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56: // xastore
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		index, array, err := vm.popArrayIndex(current)
		if err != nil {
			return stepResult{}, err
		}
		switch opcode {
		case 0x54: // bastore
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			if array.Descriptor == "[Z" {
				value = IntValue(integer & 1)
			} else {
				value = IntValue(int32(int8(integer)))
			}
		case 0x55: // castore
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			value = IntValue(integer & 0xffff)
		case 0x56: // sastore
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			value = IntValue(int32(int16(integer)))
		}
		array.Elements[index] = value

	case 0x57: // pop
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(value) {
			return stepResult{}, fmt.Errorf("SKVM pop used with category-2 value")
		}
	case 0x58: // pop2
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if !category2(value) {
			second, popErr := current.pop()
			if popErr != nil {
				return stepResult{}, popErr
			}
			if category2(second) {
				return stepResult{}, fmt.Errorf("SKVM pop2 has invalid stack shape")
			}
		}
	case 0x59: // dup
		value, err := current.peek(0)
		if err != nil {
			return stepResult{}, err
		}
		if category2(value) {
			return stepResult{}, fmt.Errorf("SKVM dup used with category-2 value")
		}
		current.push(value)
	case 0x5a: // dup_x1
		first, second, err := current.pop2()
		if err != nil {
			return stepResult{}, err
		}
		if category2(first) || category2(second) {
			return stepResult{}, fmt.Errorf("SKVM dup_x1 has invalid stack shape")
		}
		current.push(first)
		current.push(second)
		current.push(first)
	case 0x5b: // dup_x2
		first, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		second, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(first) {
			return stepResult{}, fmt.Errorf("SKVM dup_x2 has invalid top value")
		}
		if category2(second) {
			current.push(first)
			current.push(second)
			current.push(first)
			break
		}
		third, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(third) {
			return stepResult{}, fmt.Errorf("SKVM dup_x2 has invalid stack shape")
		}
		current.push(first)
		current.push(third)
		current.push(second)
		current.push(first)
	case 0x5c: // dup2
		first, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(first) {
			current.push(first)
			current.push(first)
			break
		}
		second, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(second) {
			return stepResult{}, fmt.Errorf("SKVM dup2 has invalid stack shape")
		}
		current.push(second)
		current.push(first)
		current.push(second)
		current.push(first)
	case 0x5d: // dup2_x1
		first, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(first) {
			below, popErr := current.pop()
			if popErr != nil {
				return stepResult{}, popErr
			}
			if category2(below) {
				return stepResult{}, fmt.Errorf("SKVM dup2_x1 has invalid stack shape")
			}
			current.push(first)
			current.push(below)
			current.push(first)
			break
		}
		second, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		below, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		if category2(second) || category2(below) {
			return stepResult{}, fmt.Errorf("SKVM dup2_x1 has invalid stack shape")
		}
		current.push(second)
		current.push(first)
		current.push(below)
		current.push(second)
		current.push(first)
	case 0x5e: // dup2_x2
		top, err := current.popSlotGroup(2)
		if err != nil {
			return stepResult{}, err
		}
		below, err := current.popSlotGroup(2)
		if err != nil {
			return stepResult{}, err
		}
		current.stack = append(current.stack, top...)
		current.stack = append(current.stack, below...)
		current.stack = append(current.stack, top...)
	case 0x5f: // swap
		first, second, err := current.pop2()
		if err != nil {
			return stepResult{}, err
		}
		if category2(first) || category2(second) {
			return stepResult{}, fmt.Errorf("SKVM swap has invalid stack shape")
		}
		current.push(first)
		current.push(second)

	case 0x60, 0x64, 0x68, 0x6c, 0x70, 0x78, 0x7a, 0x7c, 0x7e, 0x80, 0x82:
		right, left, err := current.popInts()
		if err != nil {
			return stepResult{}, err
		}
		var value int32
		switch opcode {
		case 0x60:
			value = left + right
		case 0x64:
			value = left - right
		case 0x68:
			value = left * right
		case 0x6c:
			if right == 0 {
				return stepResult{}, vm.newThrowable("java/lang/ArithmeticException", "/ by zero")
			}
			if left == math.MinInt32 && right == -1 {
				value = math.MinInt32
			} else {
				value = left / right
			}
		case 0x70:
			if right == 0 {
				return stepResult{}, vm.newThrowable("java/lang/ArithmeticException", "/ by zero")
			}
			if left == math.MinInt32 && right == -1 {
				value = 0
			} else {
				value = left % right
			}
		case 0x78:
			value = left << (uint32(right) & 0x1f)
		case 0x7a:
			value = left >> (uint32(right) & 0x1f)
		case 0x7c:
			value = int32(uint32(left) >> (uint32(right) & 0x1f))
		case 0x7e:
			value = left & right
		case 0x80:
			value = left | right
		case 0x82:
			value = left ^ right
		}
		current.push(IntValue(value))
	case 0x61, 0x65, 0x69, 0x6d, 0x71, 0x7f, 0x81, 0x83:
		rightValue, leftValue, err := current.pop2()
		if err != nil {
			return stepResult{}, err
		}
		right, err := rightValue.Long()
		if err != nil {
			return stepResult{}, err
		}
		left, err := leftValue.Long()
		if err != nil {
			return stepResult{}, err
		}
		var value int64
		switch opcode {
		case 0x61:
			value = left + right
		case 0x65:
			value = left - right
		case 0x69:
			value = left * right
		case 0x6d:
			if right == 0 {
				return stepResult{}, vm.newThrowable("java/lang/ArithmeticException", "/ by zero")
			}
			if left == math.MinInt64 && right == -1 {
				value = math.MinInt64
			} else {
				value = left / right
			}
		case 0x71:
			if right == 0 {
				return stepResult{}, vm.newThrowable("java/lang/ArithmeticException", "/ by zero")
			}
			if left == math.MinInt64 && right == -1 {
				value = 0
			} else {
				value = left % right
			}
		case 0x7f:
			value = left & right
		case 0x81:
			value = left | right
		case 0x83:
			value = left ^ right
		}
		current.push(LongValue(value))
	case 0x79, 0x7b, 0x7d: // long shifts
		distance, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		long, err := value.Long()
		if err != nil {
			return stepResult{}, err
		}
		shift := uint32(distance) & 0x3f
		switch opcode {
		case 0x79:
			long <<= shift
		case 0x7b:
			long >>= shift
		case 0x7d:
			long = int64(uint64(long) >> shift)
		}
		current.push(LongValue(long))
	case 0x74: // ineg
		value, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(-value))
	case 0x75: // lneg
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		long, err := value.Long()
		if err != nil {
			return stepResult{}, err
		}
		current.push(LongValue(-long))
	case 0x84: // iinc
		index, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		increment, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.local(int(index))
		if err != nil {
			return stepResult{}, err
		}
		integer, err := value.Int()
		if err != nil {
			return stepResult{}, err
		}
		return stepResult{}, current.setLocal(int(index), IntValue(integer+int32(int8(increment))))

	case 0x85: // i2l
		value, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		current.push(LongValue(int64(value)))
	case 0x88: // l2i
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		long, err := value.Long()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(int32(long)))
	case 0x91: // i2b
		value, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(int32(int8(value))))
	case 0x92: // i2c
		value, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(value & 0xffff))
	case 0x93: // i2s
		value, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		current.push(IntValue(int32(int16(value))))
	case 0x94: // lcmp
		rightValue, leftValue, err := current.pop2()
		if err != nil {
			return stepResult{}, err
		}
		right, err := rightValue.Long()
		if err != nil {
			return stepResult{}, err
		}
		left, err := leftValue.Long()
		if err != nil {
			return stepResult{}, err
		}
		var comparison int32
		if left < right {
			comparison = -1
		} else if left > right {
			comparison = 1
		}
		current.push(IntValue(comparison))

	case 0x99, 0x9a, 0x9b, 0x9c, 0x9d, 0x9e: // if<cond>
		offset, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		take := opcode == 0x99 && value == 0 ||
			opcode == 0x9a && value != 0 ||
			opcode == 0x9b && value < 0 ||
			opcode == 0x9c && value >= 0 ||
			opcode == 0x9d && value > 0 ||
			opcode == 0x9e && value <= 0
		if take {
			return stepResult{}, current.branch(opcodePC, int16(offset))
		}
	case 0x9f, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4: // if_icmp<cond>
		offset, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		right, left, err := current.popInts()
		if err != nil {
			return stepResult{}, err
		}
		take := opcode == 0x9f && left == right ||
			opcode == 0xa0 && left != right ||
			opcode == 0xa1 && left < right ||
			opcode == 0xa2 && left >= right ||
			opcode == 0xa3 && left > right ||
			opcode == 0xa4 && left <= right
		if take {
			return stepResult{}, current.branch(opcodePC, int16(offset))
		}
	case 0xa5, 0xa6: // if_acmpeq/ne
		offset, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		right, left, err := current.pop2()
		if err != nil {
			return stepResult{}, err
		}
		rightRef, err := right.Reference()
		if err != nil {
			return stepResult{}, err
		}
		leftRef, err := left.Reference()
		if err != nil {
			return stepResult{}, err
		}
		if opcode == 0xa5 && leftRef == rightRef || opcode == 0xa6 && leftRef != rightRef {
			return stepResult{}, current.branch(opcodePC, int16(offset))
		}
	case 0xa7, 0xa8: // goto, jsr
		offset, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		if opcode == 0xa8 {
			current.push(ReturnAddressValue(uint32(current.pc)))
		}
		return stepResult{}, current.branch(opcodePC, int16(offset))
	case 0xa9: // ret
		index, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.local(int(index))
		if err != nil {
			return stepResult{}, err
		}
		target, err := value.ReturnAddress()
		if err != nil {
			return stepResult{}, err
		}
		return stepResult{}, current.jump(int(target))
	case 0xaa: // tableswitch
		for current.pc%4 != 0 {
			if _, err := current.readU1(); err != nil {
				return stepResult{}, err
			}
		}
		defaultOffset, err := current.readI4()
		if err != nil {
			return stepResult{}, err
		}
		low, err := current.readI4()
		if err != nil {
			return stepResult{}, err
		}
		high, err := current.readI4()
		if err != nil {
			return stepResult{}, err
		}
		if high < low || int64(high)-int64(low) > int64(len(current.method.Code)/4) {
			return stepResult{}, fmt.Errorf("SKVM invalid tableswitch range")
		}
		key, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		selected := defaultOffset
		count := int64(high) - int64(low) + 1
		for tableIndex := int64(0); tableIndex < count; tableIndex++ {
			offset, readErr := current.readI4()
			if readErr != nil {
				return stepResult{}, readErr
			}
			if int64(key) == int64(low)+tableIndex {
				selected = offset
			}
		}
		return stepResult{}, current.branch32(opcodePC, selected)
	case 0xab: // lookupswitch
		for current.pc%4 != 0 {
			if _, err := current.readU1(); err != nil {
				return stepResult{}, err
			}
		}
		defaultOffset, err := current.readI4()
		if err != nil {
			return stepResult{}, err
		}
		pairs, err := current.readI4()
		if err != nil {
			return stepResult{}, err
		}
		if pairs < 0 || uint64(pairs) > uint64(len(current.method.Code)/8) {
			return stepResult{}, fmt.Errorf("SKVM invalid lookupswitch pair count")
		}
		key, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		selected := defaultOffset
		var previous int32
		for index := int32(0); index < pairs; index++ {
			match, readErr := current.readI4()
			if readErr != nil {
				return stepResult{}, readErr
			}
			offset, readErr := current.readI4()
			if readErr != nil {
				return stepResult{}, readErr
			}
			if index != 0 && match <= previous {
				return stepResult{}, fmt.Errorf("SKVM unsorted lookupswitch keys")
			}
			previous = match
			if key == match {
				selected = offset
			}
		}
		return stepResult{}, current.branch32(opcodePC, selected)

	case 0xac, 0xad, 0xae, 0xaf, 0xb0: // xreturn
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		return stepResult{value: value, hasValue: true, returned: true}, nil
	case 0xb1: // return
		return stepResult{returned: true}, nil
	case 0xb2, 0xb3, 0xb4, 0xb5: // fields
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		reference, err := current.class.Reference(index)
		if err != nil || reference.Kind != ReferenceField {
			return stepResult{}, fmt.Errorf("SKVM invalid field reference %d", index)
		}
		key := fieldStorageKey(reference.Class, reference.Name, reference.Descriptor)
		switch opcode {
		case 0xb2: // getstatic
			if err := vm.ensureInitialized(ctx, reference.Class, budget); err != nil {
				return stepResult{}, err
			}
			var value Value
			var ok bool
			if runtime, loaded := vm.classes[reference.Class]; loaded {
				value, ok = runtime.static[key]
			} else {
				value, ok = vm.hostStatic[key]
			}
			if !ok {
				return stepResult{}, fmt.Errorf("SKVM static field %s.%s is unavailable", reference.Class, reference.Name)
			}
			current.push(value)
		case 0xb3: // putstatic
			value, popErr := current.pop()
			if popErr != nil {
				return stepResult{}, popErr
			}
			if err := vm.ensureInitialized(ctx, reference.Class, budget); err != nil {
				return stepResult{}, err
			}
			if runtime, loaded := vm.classes[reference.Class]; loaded {
				runtime.static[key] = value
			} else if _, registered := vm.hostStatic[key]; registered {
				vm.hostStatic[key] = value
			} else {
				return stepResult{}, fmt.Errorf(
					"SKVM static field %s.%s is unavailable",
					reference.Class,
					reference.Name,
				)
			}
		case 0xb4: // getfield
			object, _, objectErr := vm.popObject(current)
			if objectErr != nil {
				return stepResult{}, objectErr
			}
			value, ok := object.Fields[key]
			if !ok {
				value, err = zeroValue(reference.Descriptor)
				if err != nil {
					return stepResult{}, err
				}
			}
			current.push(value)
		case 0xb5: // putfield
			value, popErr := current.pop()
			if popErr != nil {
				return stepResult{}, popErr
			}
			object, _, objectErr := vm.popObject(current)
			if objectErr != nil {
				return stepResult{}, objectErr
			}
			object.Fields[key] = value
		}
	case 0xb6, 0xb7, 0xb8, 0xb9: // invokes
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		if opcode == 0xb9 {
			if _, err := current.readU1(); err != nil {
				return stepResult{}, err
			}
			zero, err := current.readU1()
			if err != nil {
				return stepResult{}, err
			}
			if zero != 0 {
				return stepResult{}, fmt.Errorf("SKVM invokeinterface reserved byte is nonzero")
			}
		}
		current.invokePC = opcodePC
		value, hasValue, err := vm.invoke(ctx, current, index, opcode, budget)
		if err != nil {
			return stepResult{}, err
		}
		current.invokePC = -1
		if hasValue {
			current.push(value)
		}
	case 0xbb: // new
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		constant, err := current.class.Constant(index)
		if err != nil || constant.Kind != ConstantClass {
			return stepResult{}, fmt.Errorf("SKVM new has invalid class reference")
		}
		if err := vm.ensureInitialized(ctx, constant.Class, budget); err != nil {
			return stepResult{}, err
		}
		reference, err := vm.allocateObject(constant.Class)
		if err != nil {
			return stepResult{}, err
		}
		current.push(ReferenceValue(reference))
	case 0xbc: // newarray
		arrayType, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		length, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		descriptor, kind, err := primitiveArrayType(arrayType)
		if err != nil {
			return stepResult{}, err
		}
		if length < 0 {
			return stepResult{}, vm.newThrowable("java/lang/NegativeArraySizeException", "")
		}
		elements := make([]Value, int(length))
		for index := range elements {
			elements[index] = zeroForKind(kind)
		}
		current.push(ReferenceValue(vm.newArray(descriptor, elements)))
	case 0xbd: // anewarray
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		constant, err := current.class.Constant(index)
		if err != nil || constant.Kind != ConstantClass {
			return stepResult{}, fmt.Errorf("SKVM anewarray has invalid class reference")
		}
		length, err := current.popInt()
		if err != nil {
			return stepResult{}, err
		}
		if length < 0 {
			return stepResult{}, vm.newThrowable("java/lang/NegativeArraySizeException", "")
		}
		descriptor := "[L" + constant.Class + ";"
		if len(constant.Class) != 0 && constant.Class[0] == '[' {
			descriptor = "[" + constant.Class
		}
		elements := make([]Value, int(length))
		for index := range elements {
			elements[index] = ReferenceValue(0)
		}
		current.push(ReferenceValue(vm.newArray(descriptor, elements)))
	case 0xbe: // arraylength
		object, _, err := vm.popObject(current)
		if err != nil {
			return stepResult{}, err
		}
		if object.Array == nil {
			return stepResult{}, fmt.Errorf("SKVM arraylength used with non-array")
		}
		current.push(IntValue(int32(len(object.Array.Elements))))
	case 0xbf: // athrow
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		reference, err := value.Reference()
		if err != nil {
			return stepResult{}, err
		}
		if reference == 0 {
			return stepResult{}, vm.newThrowable("java/lang/NullPointerException", "")
		}
		object, _ := vm.Object(reference)
		class := "java/lang/Throwable"
		if object != nil {
			class = object.Class
		}
		return stepResult{}, &thrown{reference: reference, class: class}
	case 0xc0, 0xc1: // checkcast, instanceof
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		constant, err := current.class.Constant(index)
		if err != nil || constant.Kind != ConstantClass {
			return stepResult{}, fmt.Errorf("SKVM type operation has invalid class reference")
		}
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		reference, err := value.Reference()
		if err != nil {
			return stepResult{}, err
		}
		if opcode == 0xc1 {
			if vm.IsInstance(reference, constant.Class) {
				current.push(IntValue(1))
			} else {
				current.push(IntValue(0))
			}
			break
		}
		if reference != 0 && !vm.IsInstance(reference, constant.Class) {
			return stepResult{}, vm.newThrowable("java/lang/ClassCastException", "")
		}
		current.push(value)
	case 0xc2, 0xc3: // monitorenter, monitorexit
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		reference, err := value.Reference()
		if err != nil {
			return stepResult{}, err
		}
		if reference == 0 {
			return stepResult{}, vm.newThrowable("java/lang/NullPointerException", "")
		}
	case 0xc4: // wide
		modified, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		switch modified {
		case 0x15, 0x16, 0x17, 0x18, 0x19:
			value, localErr := current.local(int(index))
			if localErr != nil {
				return stepResult{}, localErr
			}
			current.push(value)
		case 0x36, 0x37, 0x38, 0x39, 0x3a:
			value, popErr := current.pop()
			if popErr != nil {
				return stepResult{}, popErr
			}
			return stepResult{}, current.setLocal(int(index), value)
		case 0x84:
			increment, readErr := current.readU2()
			if readErr != nil {
				return stepResult{}, readErr
			}
			value, localErr := current.local(int(index))
			if localErr != nil {
				return stepResult{}, localErr
			}
			integer, intErr := value.Int()
			if intErr != nil {
				return stepResult{}, intErr
			}
			return stepResult{}, current.setLocal(
				int(index),
				IntValue(integer+int32(int16(increment))),
			)
		case 0xa9:
			value, localErr := current.local(int(index))
			if localErr != nil {
				return stepResult{}, localErr
			}
			target, targetErr := value.ReturnAddress()
			if targetErr != nil {
				return stepResult{}, targetErr
			}
			return stepResult{}, current.jump(int(target))
		default:
			return stepResult{}, fmt.Errorf("SKVM invalid wide opcode 0x%02x", modified)
		}
	case 0xc5: // multianewarray
		index, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		dimensions, err := current.readU1()
		if err != nil {
			return stepResult{}, err
		}
		constant, err := current.class.Constant(index)
		if err != nil || constant.Kind != ConstantClass ||
			dimensions == 0 {
			return stepResult{}, fmt.Errorf("SKVM multianewarray has invalid operands")
		}
		sizes := make([]int, dimensions)
		for dimension := int(dimensions) - 1; dimension >= 0; dimension-- {
			size, popErr := current.popInt()
			if popErr != nil {
				return stepResult{}, popErr
			}
			if size < 0 {
				return stepResult{}, vm.newThrowable(
					"java/lang/NegativeArraySizeException",
					"",
				)
			}
			sizes[dimension] = int(size)
		}
		reference, err := vm.newMultiArray(constant.Class, sizes, 0)
		if err != nil {
			return stepResult{}, err
		}
		current.push(ReferenceValue(reference))
	case 0xc6, 0xc7: // ifnull, ifnonnull
		offset, err := current.readU2()
		if err != nil {
			return stepResult{}, err
		}
		value, err := current.pop()
		if err != nil {
			return stepResult{}, err
		}
		reference, err := value.Reference()
		if err != nil {
			return stepResult{}, err
		}
		if opcode == 0xc6 && reference == 0 || opcode == 0xc7 && reference != 0 {
			return stepResult{}, current.branch(opcodePC, int16(offset))
		}
	case 0xc8, 0xc9: // goto_w, jsr_w
		offset, err := current.readI4()
		if err != nil {
			return stepResult{}, err
		}
		if opcode == 0xc9 {
			current.push(ReturnAddressValue(uint32(current.pc)))
		}
		return stepResult{}, current.branch32(opcodePC, offset)
	default:
		return stepResult{}, &UnsupportedOpcodeError{
			Class:      current.class.Name,
			Method:     current.method.Name,
			Descriptor: current.method.Descriptor,
			PC:         opcodePC,
			Opcode:     opcode,
		}
	}
	return stepResult{}, nil
}

func traceTarget(class *Class, code []byte, pc int, opcode byte) string {
	if opcode == 0x12 && pc+1 < len(code) {
		constant, err := class.Constant(uint16(code[pc+1]))
		if err == nil {
			if constant.Kind == ConstantString {
				return fmt.Sprintf("%q", constant.String)
			}
			return string(constant.Kind)
		}
	}
	if pc < 0 || pc+2 >= len(code) {
		return ""
	}
	index := binary.BigEndian.Uint16(code[pc+1 : pc+3])
	if opcode == 0x13 || opcode == 0x14 {
		constant, err := class.Constant(index)
		if err == nil {
			if constant.Kind == ConstantString {
				return fmt.Sprintf("%q", constant.String)
			}
			return string(constant.Kind)
		}
	}
	switch opcode {
	case 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9:
		reference, err := class.Reference(index)
		if err != nil {
			return ""
		}
		return reference.Class + "." + reference.Name + reference.Descriptor
	case 0xbb, 0xbd, 0xc0, 0xc1, 0xc5:
		constant, err := class.Constant(index)
		if err == nil && constant.Kind == ConstantClass {
			return constant.Class
		}
	}
	return ""
}

func (vm *VM) invoke(
	ctx context.Context,
	current *frame,
	index uint16,
	opcode byte,
	budget *uint64,
) (Value, bool, error) {
	reference, err := current.class.Reference(index)
	if err != nil {
		return Value{}, false, err
	}
	parameters, result, err := parseMethodDescriptor(reference.Descriptor)
	if err != nil {
		return Value{}, false, err
	}
	args := make([]Value, len(parameters))
	for position := len(args) - 1; position >= 0; position-- {
		args[position], err = current.pop()
		if err != nil {
			return Value{}, false, err
		}
	}
	var receiver uint32
	if opcode != 0xb8 {
		value, popErr := current.pop()
		if popErr != nil {
			return Value{}, false, popErr
		}
		receiver, popErr = value.Reference()
		if popErr != nil {
			return Value{}, false, popErr
		}
		if receiver == 0 {
			return Value{}, false, vm.newThrowable("java/lang/NullPointerException", "")
		}
	}

	targetClass := reference.Class
	if opcode == 0xb6 || opcode == 0xb9 {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("SKVM invalid receiver %d", receiver)
		}
		targetClass = object.Class
	}
	if opcode == 0xb8 {
		if err := vm.ensureInitialized(ctx, reference.Class, budget); err != nil {
			return Value{}, false, err
		}
	}

	var class *Class
	var method Method
	switch opcode {
	case 0xb7, 0xb8:
		class, method, err = vm.resolveDeclaredMethod(reference.Class, reference.Name, reference.Descriptor)
	default:
		class, method, err = vm.resolveVirtualMethod(targetClass, reference.Name, reference.Descriptor)
	}
	if err == nil {
		return vm.execute(ctx, class, method, receiver, args, budget)
	}

	native, ok := vm.resolveNative(targetClass, reference, opcode)
	if !ok {
		return Value{}, false, fmt.Errorf(
			"SKVM native method %s.%s%s is unavailable",
			reference.Class,
			reference.Name,
			reference.Descriptor,
		)
	}
	value, hasValue, err := native(ctx, vm, receiver, args)
	if err != nil {
		return Value{}, false, err
	}
	if result.kind == 'V' {
		if hasValue {
			return Value{}, false, fmt.Errorf("SKVM native void method returned a value")
		}
		return Value{}, false, nil
	}
	if !hasValue || value.Kind != result.valueKind() {
		return Value{}, false, fmt.Errorf(
			"SKVM native %s.%s%s returned %s, want %s",
			reference.Class,
			reference.Name,
			reference.Descriptor,
			value.Kind,
			result.valueKind(),
		)
	}
	return value, true, nil
}

func (vm *VM) resolveNative(
	targetClass string,
	reference Reference,
	opcode byte,
) (NativeFunc, bool) {
	if opcode == 0xb7 || opcode == 0xb8 {
		native, ok := vm.natives[nativeKey{
			reference.Class,
			reference.Name,
			reference.Descriptor,
		}]
		return native, ok
	}
	for current := targetClass; current != ""; current = vm.superName(current) {
		if native, ok := vm.natives[nativeKey{
			current,
			reference.Name,
			reference.Descriptor,
		}]; ok {
			return native, true
		}
	}
	native, ok := vm.natives[nativeKey{
		reference.Class,
		reference.Name,
		reference.Descriptor,
	}]
	return native, ok
}

func (vm *VM) popObject(current *frame) (*Object, uint32, error) {
	value, err := current.pop()
	if err != nil {
		return nil, 0, err
	}
	reference, err := value.Reference()
	if err != nil {
		return nil, 0, err
	}
	if reference == 0 {
		return nil, 0, vm.newThrowable("java/lang/NullPointerException", "")
	}
	object, ok := vm.Object(reference)
	if !ok {
		return nil, 0, fmt.Errorf("SKVM invalid object reference %d", reference)
	}
	return object, reference, nil
}

func (vm *VM) popArrayIndex(current *frame) (int, *Array, error) {
	indexValue, err := current.popInt()
	if err != nil {
		return 0, nil, err
	}
	object, _, err := vm.popObject(current)
	if err != nil {
		return 0, nil, err
	}
	if object.Array == nil {
		return 0, nil, fmt.Errorf("SKVM array operation used with non-array")
	}
	if indexValue < 0 || int64(indexValue) >= int64(len(object.Array.Elements)) {
		return 0, nil, vm.newThrowable("java/lang/ArrayIndexOutOfBoundsException", "")
	}
	return int(indexValue), object.Array, nil
}

func primitiveArrayType(arrayType byte) (string, ValueKind, error) {
	switch arrayType {
	case 4:
		return "[Z", ValueInt, nil
	case 5:
		return "[C", ValueInt, nil
	case 6:
		return "[F", ValueFloat, nil
	case 7:
		return "[D", ValueDouble, nil
	case 8:
		return "[B", ValueInt, nil
	case 9:
		return "[S", ValueInt, nil
	case 10:
		return "[I", ValueInt, nil
	case 11:
		return "[J", ValueLong, nil
	default:
		return "", ValueTop, fmt.Errorf("SKVM invalid newarray type %d", arrayType)
	}
}

func (vm *VM) newMultiArray(
	descriptor string,
	sizes []int,
	depth int,
) (uint32, error) {
	if depth >= len(sizes) || depth >= len(descriptor) || descriptor[depth] != '[' {
		return 0, fmt.Errorf("SKVM invalid multidimensional array descriptor %q", descriptor)
	}
	elements := make([]Value, sizes[depth])
	if depth+1 == len(sizes) {
		component := descriptor[depth+1:]
		kind := ValueReference
		if len(component) == 1 {
			switch component[0] {
			case 'J':
				kind = ValueLong
			case 'F':
				kind = ValueFloat
			case 'D':
				kind = ValueDouble
			case 'B', 'C', 'I', 'S', 'Z':
				kind = ValueInt
			}
		}
		for index := range elements {
			elements[index] = zeroForKind(kind)
		}
		return vm.newArray(descriptor[depth:], elements), nil
	}
	for index := range elements {
		child, err := vm.newMultiArray(descriptor, sizes, depth+1)
		if err != nil {
			return 0, err
		}
		elements[index] = ReferenceValue(child)
	}
	return vm.newArray(descriptor[depth:], elements), nil
}

func zeroForKind(kind ValueKind) Value {
	switch kind {
	case ValueLong:
		return LongValue(0)
	case ValueFloat:
		return FloatValue(0)
	case ValueDouble:
		return DoubleValue(0)
	case ValueReference:
		return ReferenceValue(0)
	default:
		return IntValue(0)
	}
}
