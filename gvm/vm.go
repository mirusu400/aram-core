// Package gvm provides a bounded, deterministic subset of GVM bytecode for
// caller-supplied buffers. It is not an SGS loader or a game-startup runtime.
package gvm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	ErrBudget              = errors.New("gvm: instruction budget exhausted")
	ErrTruncated           = errors.New("gvm: truncated instruction")
	ErrStackUnderflow      = errors.New("gvm: operand stack underflow")
	ErrStackOverflow       = errors.New("gvm: operand stack overflow")
	ErrDivideByZero        = errors.New("gvm: division by zero")
	ErrReturnStackOverflow = errors.New("gvm: return stack overflow")
	ErrInvalidTarget       = errors.New("gvm: target outside program")
	// ErrInvalidArraySelector rejects operations outside the verified scalar family.
	ErrInvalidArraySelector = errors.New("gvm: invalid scalar array selector")
)

// UnsupportedOpcodeError identifies the fetched opcode and its buffer offset.
// Unimplemented instructions never silently behave as NOPs.
type UnsupportedOpcodeError struct {
	Opcode byte
	Offset int
}

func (e *UnsupportedOpcodeError) Error() string {
	return fmt.Sprintf("gvm: unsupported opcode 0x%02x at offset %d", e.Opcode, e.Offset)
}

// ExecutionError associates a safety fault with the instruction's opcode offset.
// At end of buffer, Offset is the position of the failed opcode fetch.
type ExecutionError struct {
	Offset int
	Cause  error
}

func (e *ExecutionError) Error() string { return fmt.Sprintf("gvm: offset %d: %v", e.Offset, e.Cause) }
func (e *ExecutionError) Unwrap() error { return e.Cause }

// VM owns its program and stacks. It has no host services and is not safe for
// concurrent use. The zero value is an empty VM, equivalent to New(nil).
// Stack lengths encode the reference's signed top indices (length minus one).
type VM struct {
	code        []byte
	symbols     [][]byte
	address     *addressMemory
	pc          int
	stack       [65]uint16
	depth       int
	returns     [17]int
	returnDepth int
	halted      bool
	fault       error
}

// New copies program and begins at buffer offset zero. An empty program fails
// with ErrTruncated on its first fetch. This is not the SGS entry-zero rule.
func New(program []byte) *VM { return &VM{code: append([]byte(nil), program...)} }

// NewAt copies the entire buffer, selecting an explicit in-bounds entry.
// Absolute branch operands remain relative to the beginning of this buffer,
// not to entry. No header parsing or SGS entry mapping is performed.
func NewAt(program []byte, entry uint32) (*VM, error) {
	if uint64(entry) >= uint64(len(program)) {
		return nil, fmt.Errorf("%w: entry offset %d", ErrInvalidTarget, entry)
	}
	v := New(program)
	v.pc = int(entry)
	return v, nil
}

// PC returns the next buffer-relative fetch position. On handler failure the
// opcode has already been consumed, but no operands or stacks are committed.
func (v *VM) PC() int { return v.pc }

// Stack returns an independent bottom-to-top copy, interpreting slots as raw16.
func (v *VM) Stack() []uint16 { return append([]uint16(nil), v.stack[:v.depth]...) }

// Halted reports successful completion of the current dispatch via guest 0xff.
// Faults are not successful halts.
func (v *VM) Halted() bool { return v.halted }

// Run executes at most budget instructions. Exhaustion is resumable and does
// not set a fault. A zero budget makes no progress, returning ErrBudget unless
// the VM has already halted or faulted. Reaching 0xff on the last step succeeds.
func (v *VM) Run(budget uint64) error {
	if v.fault != nil {
		return v.fault
	}
	if v.halted {
		return nil
	}
	for ; budget > 0; budget-- {
		if err := v.Step(); err != nil {
			return err
		}
		if v.halted {
			return nil
		}
	}
	return ErrBudget
}

// Step executes one instruction. A successful halted VM is inert. Safety
// faults are sticky: subsequent Step/Run calls return the same error without
// further mutation. Bounds/underflow checks are emulator policy, not native
// behavior. Every fetched opcode advances PC before handler execution.
func (v *VM) Step() error {
	if v.fault != nil {
		return v.fault
	}
	if v.halted {
		return nil
	}
	offset := v.pc
	fail := func(cause error) error { v.fault = &ExecutionError{Offset: offset, Cause: cause}; return v.fault }
	if v.pc >= len(v.code) {
		return fail(ErrTruncated)
	}
	op := v.code[v.pc]
	v.pc++
	switch op {
	case 0x00:
	case 0xff:
		v.halted = true
	case 0x03:
		// Host policy eagerly requires both unsigned operands before capacity.
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		index, element := int(v.code[v.pc]), int(v.code[v.pc+1])
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		region := v.symbols[index]
		// As for 31, exact span length must represent a uint8 word count.
		// This is host shape policy, not native descriptor validation.
		if len(region)%2 != 0 || len(region) > 510 {
			return fail(ErrInvalidSymbolRegion)
		}
		if element >= len(region)/2 {
			return fail(ErrInvalidElement)
		}
		// The validated element guarantees a full word within this bound span.
		value := binary.LittleEndian.Uint16(region[2*element : 2*element+2])
		v.stack[v.depth] = value
		v.depth++
		v.pc += 2
	case 0x04:
		if len(v.code)-v.pc < 1 {
			return fail(ErrTruncated)
		}
		index := int(v.code[v.pc])
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		if len(v.symbols[index]) < 2 {
			return fail(ErrInvalidSymbolRegion)
		}
		v.stack[v.depth] = binary.LittleEndian.Uint16(v.symbols[index][:2])
		v.depth++
		v.pc++
	case 0x09:
		// Host policy caches both operands before guards or aliasing writes.
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		index, element := int(v.code[v.pc]), int(v.code[v.pc+1])
		// The native upper guard rejects depth65 even though success pops.
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		region := v.symbols[index]
		// Exact len/2 is the restricted uint8 count representation of 03/31.
		if len(region)%2 != 0 || len(region) > 510 {
			return fail(ErrInvalidSymbolRegion)
		}
		if element >= len(region)/2 {
			return fail(ErrInvalidElement)
		}
		if v.depth == 0 {
			return fail(ErrStackUnderflow)
		}
		value := v.stack[v.depth-1]
		binary.LittleEndian.PutUint16(region[2*element:2*element+2], value)
		v.depth--
		v.stack[v.depth] = 0 // Host hygiene, not native backing-slot behavior.
		v.pc += 2
	case 0x0a:
		if len(v.code)-v.pc < 1 {
			return fail(ErrTruncated)
		}
		index := int(v.code[v.pc])
		// Native signed top>=64 rejects depth65 even though this handler pops.
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		region := v.symbols[index]
		if v.address != nil {
			binding := v.address.bindings[index]
			region = v.address.ram
			if binding.region == AddressFile {
				region = v.address.file
			}
			// Host policy requires the whole word inside the global region,
			// not inside the descriptor. Widen before adding for 32-bit hosts.
			if uint64(binding.offset)+2 > uint64(len(region)) {
				return fail(ErrInvalidSymbolRegion)
			}
			region = region[int(binding.offset):]
		} else if len(region) < 2 {
			// Legacy independent bindings cannot represent bytes beyond their span.
			return fail(ErrInvalidSymbolRegion)
		}
		if v.depth == 0 {
			return fail(ErrStackUnderflow)
		}
		// Cache operands before a write that may alias this operand or future code.
		value := v.stack[v.depth-1]
		binary.LittleEndian.PutUint16(region[:2], value)
		v.depth--
		v.stack[v.depth] = 0
		v.pc++
	case 0xb4:
		if v.address == nil {
			v.fault = &UnsupportedOpcodeError{Opcode: op, Offset: offset}
			return v.fault
		}
		if v.depth < 4 {
			return fail(ErrStackUnderflow)
		}
		// Cache all four arguments before writes that may alias program bytes.
		ref, scalar := v.stack[v.depth-4], v.stack[v.depth-3]
		count, selector := int16(v.stack[v.depth-2]), v.stack[v.depth-1]
		region, index := v.address.ram, ref
		if index&0x4000 != 0 {
			region = v.address.file
			index &^= 0x4000
		}
		// Host policy requires a full starting word even for nonpositive count.
		start := uint64(index) * 2
		if int16(index) < 0 || start+2 > uint64(len(region)) {
			return fail(ErrInvalidAddress)
		}
		// Only these twelve table entries have a verified bounded contract.
		if selector > 11 {
			return fail(ErrInvalidArraySelector)
		}
		// Both native operations check zero before count. Our shared sticky
		// fault preserves memory/stack, not native diagnostics, redirection/pop.
		if (selector == 4 || selector == 5) && scalar == 0 {
			return fail(ErrDivideByZero)
		}
		if count > 0 {
			end := start + 2*uint64(count)
			// Preflight the entire global arena span before any partial store.
			if end > uint64(len(region)) {
				return fail(ErrInvalidAddress)
			}
			for pos := start; pos < end; pos += 2 {
				x := binary.LittleEndian.Uint16(region[pos : pos+2])
				var value uint16
				switch selector {
				case 0:
					value = scalar
				case 1:
					value = x + scalar
				case 2:
					value = x - scalar
				case 3:
					value = x * scalar
				case 4:
					value = uint16(int32(int16(x)) / int32(int16(scalar)))
				case 5:
					value = uint16(int32(int16(x)) % int32(int16(scalar)))
				case 6:
					value = x & scalar
				case 7:
					value = x | scalar
				case 8:
					value = ^scalar // Assignment, not complement of x.
				case 9:
					value = x ^ scalar
				case 10:
					value = uint16(int16(x) >> (scalar & 31))
				case 11:
					value = x << (scalar & 31)
				}
				binary.LittleEndian.PutUint16(region[pos:pos+2], value)
			}
		}
		v.depth -= 4
		clear(v.stack[v.depth : v.depth+4]) // Host hygiene, not native behavior.
	case 0x4f:
		if v.address == nil {
			v.fault = &UnsupportedOpcodeError{Opcode: op, Offset: offset}
			return v.fault
		}
		if v.depth < 2 {
			return fail(ErrStackUnderflow)
		}
		// This is a direct signed RAM word index, not a tagged address.
		index := int16(v.stack[v.depth-2])
		if index < 0 || uint64(index)*2+2 > uint64(len(v.address.ram)) {
			return fail(ErrInvalidAddress)
		}
		// Cache both operands before writing, then publish the two-word pop.
		start, value := uint64(index)*2, v.stack[v.depth-1]
		binary.LittleEndian.PutUint16(v.address.ram[start:start+2], value)
		v.depth -= 2
		v.stack[v.depth], v.stack[v.depth+1] = 0, 0 // Host hygiene only.
	case 0x4c:
		if v.address == nil {
			v.fault = &UnsupportedOpcodeError{Opcode: op, Offset: offset}
			return v.fault
		}
		// Host policy requires both unsigned operands before semantic guards.
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		index, element := int(v.code[v.pc]), int(v.code[v.pc+1])
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		if index >= len(v.address.bindings) {
			return fail(ErrInvalidSymbol)
		}
		// As for 03/31, an exact even span represents a uint8 element count.
		// This is restricted host metadata, not inferred native validation.
		length := len(v.symbols[index])
		if length%2 != 0 || length > 510 {
			return fail(ErrInvalidSymbolRegion)
		}
		if element >= length/2 {
			return fail(ErrInvalidElement)
		}
		binding := v.address.bindings[index]
		region := v.address.ram
		if binding.region == AddressFile {
			region = v.address.file
		}
		// Construction validated the whole descriptor in its matched region.
		// Require a full selected word too, without reading its payload.
		start := uint64(binding.offset) + 2*uint64(element)
		if start+2 > uint64(len(region)) {
			return fail(ErrInvalidSymbolRegion)
		}
		// Low16(SAR32(x,1)) equals low16(x>>1), even with bit31 set.
		// Preserve odd-offset rounding, truncation and tag collisions.
		value := uint16(start >> 1)
		if binding.region == AddressFile {
			value |= 0x4000
		}
		v.stack[v.depth] = value
		v.depth++
		v.pc += 2
	case 0x4d:
		if v.address == nil {
			v.fault = &UnsupportedOpcodeError{Opcode: op, Offset: offset}
			return v.fault
		}
		if len(v.code)-v.pc < 1 {
			return fail(ErrTruncated)
		}
		index := int(v.code[v.pc])
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		if index >= len(v.address.bindings) {
			return fail(ErrInvalidSymbol)
		}
		binding := v.address.bindings[index]
		region := v.address.ram
		if binding.region == AddressFile {
			region = v.address.file
		}
		// Membership is half-open and does not require a nonempty symbol or
		// a full word. ReadWord separately validates any later dereference.
		if uint64(binding.offset) >= uint64(len(region)) {
			return fail(ErrInvalidSymbolRegion)
		}
		value := uint16(binding.offset >> 1)
		if binding.region == AddressFile {
			value |= 0x4000
		}
		v.stack[v.depth] = value
		v.depth++
		v.pc++
	case 0x05, 0x06:
		// Reference top>=0x40 before increment permits 65 slots from top=-1.
		if v.depth >= len(v.stack) {
			return fail(ErrStackOverflow)
		}
		width := 1
		if op == 0x06 {
			width = 2
		}
		if len(v.code)-v.pc < width {
			return fail(ErrTruncated)
		}
		var value uint16
		if op == 0x05 {
			value = uint16(int16(int8(v.code[v.pc])))
		} else {
			value = binary.BigEndian.Uint16(v.code[v.pc : v.pc+2])
		}
		v.stack[v.depth] = value
		v.depth++
		v.pc += width
	case 0x12, 0x13, 0x14, 0x15, 0x1f:
		if v.depth < 2 {
			return fail(ErrStackUnderflow)
		}
		a, b := v.stack[v.depth-2], v.stack[v.depth-1]
		var value uint16
		switch op {
		case 0x12:
			value = a + b
		case 0x13:
			value = a - b
		case 0x14:
			value = a * b
		case 0x15:
			// Safe emulator policy is a sticky transactional fault. Native
			// helper cleanup followed by a pop is intentionally not modeled.
			if b == 0 {
				return fail(ErrDivideByZero)
			}
			// Native operands are sign-extended before 32-bit division, so
			// -32768/-1 yields 32768 then truncates to the raw16 slot.
			value = uint16(int32(int16(a)) / int32(int16(b)))
		case 0x1f:
			if int16(a) >= int16(b) {
				value = 1
			}
		}
		v.depth--
		v.stack[v.depth-1] = value
		v.stack[v.depth] = 0
	case 0x45:
		if v.returnDepth == 0 {
			v.fault = &UnsupportedOpcodeError{Opcode: op, Offset: offset}
			return v.fault
		}
		target := v.returns[v.returnDepth-1]
		if target < 0 || target >= len(v.code) {
			return fail(ErrInvalidTarget)
		}
		v.returnDepth--
		v.returns[v.returnDepth] = 0
		v.pc = target
	case 0x96, 0x97:
		// In the hash-qualified reference build both callees are empty returns.
		// Their wrappers pop one signed16 argument. No host service is invented.
		if v.depth == 0 {
			return fail(ErrStackUnderflow)
		}
		v.depth--
		v.stack[v.depth] = 0
	case 0x31:
		// Both index bytes precede native symbol validation.
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		index, element := int(v.code[v.pc]), int(v.code[v.pc+1])
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		region := v.symbols[index]
		// The host binding must exactly model a uint8 descriptor word count.
		if len(region)%2 != 0 || len(region) > 510 {
			return fail(ErrInvalidSymbolRegion)
		}
		if element >= len(region)/2 {
			return fail(ErrInvalidElement)
		}
		if len(v.code)-v.pc < 3 {
			return fail(ErrTruncated)
		}
		value := uint16(int16(int8(v.code[v.pc+2])))
		binary.LittleEndian.PutUint16(region[2*element:2*element+2], value)
		v.pc += 3
	case 0x36:
		if len(v.code)-v.pc < 1 {
			return fail(ErrTruncated)
		}
		index := int(v.code[v.pc])
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		if len(v.symbols[index]) < 2 {
			return fail(ErrInvalidSymbolRegion)
		}
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		// Read before writing: the symbol can alias this instruction itself.
		value := uint16(int16(int8(v.code[v.pc+1])))
		binary.LittleEndian.PutUint16(v.symbols[index][:2], value)
		v.pc += 2
	case 0x3a:
		// Host policy eagerly validates both operands before symbol lookup.
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		index, delta := int(v.code[v.pc]), int8(v.code[v.pc+1])
		if index >= len(v.symbols) {
			return fail(ErrInvalidSymbol)
		}
		region := v.symbols[index]
		if v.address != nil {
			binding := v.address.bindings[index]
			region = v.address.ram
			if binding.region == AddressFile {
				region = v.address.file
			}
			// Require a full global word, not a descriptor span or shape.
			if uint64(binding.offset)+2 > uint64(len(region)) {
				return fail(ErrInvalidSymbolRegion)
			}
			region = region[int(binding.offset):]
		} else if len(region) < 2 {
			return fail(ErrInvalidSymbolRegion)
		}
		// Cache the immediate and old word before any aliased file/code write.
		old := binary.LittleEndian.Uint16(region[:2])
		binary.LittleEndian.PutUint16(region[:2], old+uint16(int16(delta)))
		v.pc += 2
	case 0x3c, 0x3d, 0x3e:
		// Host safety policy eagerly requires all operands, even on fallthrough,
		// and validates before committing the pop. These differ from lazy target
		// reads and a pre-target pop; they are not native error-order claims.
		if len(v.code)-v.pc < 3 {
			return fail(ErrTruncated)
		}
		if v.depth == 0 {
			return fail(ErrStackUnderflow)
		}
		taken := int16(v.stack[v.depth-1]) < int16(int8(v.code[v.pc]))
		if op == 0x3d {
			taken = int16(v.stack[v.depth-1]) >= int16(int8(v.code[v.pc]))
		}
		if op == 0x3e {
			taken = int16(v.stack[v.depth-1]) <= int16(int8(v.code[v.pc]))
		}
		target := int(binary.BigEndian.Uint16(v.code[v.pc+1 : v.pc+3]))
		if taken && target >= len(v.code) {
			return fail(ErrInvalidTarget)
		}
		v.depth--
		v.stack[v.depth] = 0 // Host hygiene for the inaccessible popped slot.
		if taken {
			v.pc = target
		} else {
			v.pc += 3
		}
	case 0x41, 0x42, 0x43, 0x44:
		if op == 0x44 && v.returnDepth >= len(v.returns) {
			return fail(ErrReturnStackOverflow)
		}
		if len(v.code)-v.pc < 2 {
			return fail(ErrTruncated)
		}
		conditional := op == 0x42 || op == 0x43
		if conditional && v.depth == 0 {
			return fail(ErrStackUnderflow)
		}
		taken := true
		if conditional {
			nonzero := v.stack[v.depth-1] != 0
			taken = (op == 0x42 && nonzero) || (op == 0x43 && !nonzero)
		}
		target := int(binary.BigEndian.Uint16(v.code[v.pc : v.pc+2]))
		if taken && target >= len(v.code) {
			return fail(ErrInvalidTarget)
		}
		if conditional {
			v.depth--
			v.stack[v.depth] = 0
		}
		if op == 0x44 {
			v.returns[v.returnDepth] = v.pc + 2
			v.returnDepth++
		}
		if taken {
			v.pc = target
		} else {
			v.pc += 2
		}
	default:
		v.fault = &UnsupportedOpcodeError{Opcode: op, Offset: offset}
		return v.fault
	}
	return nil
}
