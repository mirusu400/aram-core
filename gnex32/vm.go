// Package gnex32 executes the observed GNEX version-4 SGS subset. Opcode and
// service semantics are inferred from SDK assembler output and the Virus image;
// full gameplay still needs comparison with a reference runtime.
package gnex32

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/loader/gnex"
)

var (
	ErrBudget             = errors.New("gnex32: instruction budget exhausted")
	ErrCallStackOverflow  = errors.New("gnex32: call stack overflow")
	ErrStackOverflow      = errors.New("gnex32: value stack overflow")
	ErrStackUnderflow     = errors.New("gnex32: value stack underflow")
	ErrTruncated          = errors.New("gnex32: truncated instruction")
	ErrInvalidLValue      = errors.New("gnex32: invalid lvalue store")
	ErrServiceStackEffect = errors.New("gnex32: invalid service stack effect")
	ErrDivisionByZero     = errors.New("gnex32: division by zero")
	ErrStackMarkUnderflow = errors.New("gnex32: stack mark underflow")
	ErrEventUnavailable   = errors.New("gnex32: event entry unavailable")
	ErrEventRunning       = errors.New("gnex32: cannot enter event while VM is running")
)

type UnsupportedOpcodeError struct {
	Opcode uint16
	Offset int // byte offset in the code span
}

func (err *UnsupportedOpcodeError) Error() string {
	return fmt.Sprintf("gnex32: unsupported opcode %#x at code offset %d", err.Opcode, err.Offset)
}

type UnsupportedServiceError struct {
	ID     uint16
	Offset int // byte offset in the code span
}

func (err *UnsupportedServiceError) Error() string {
	return fmt.Sprintf("gnex32: unsupported service %d at code offset %d", err.ID, err.Offset)
}

// ServiceResult describes an explicit stack change by an external service.
// The service IDs and their native effects remain unverified.
type ServiceResult struct {
	Pop  int
	Push []uint32
}

type ServiceHandler func(id uint16, stack []uint32) (ServiceResult, error)

type gnex32SymbolRef struct {
	symbol  int
	element int
}

// VM tests the observed 0x85 call, 0x86 return, and 0x04 symbol-read layout.
// These effects are candidates inferred from the supplied version-4 image,
// not a claim of complete or reference-validated GNEX execution.
type VM struct {
	image        gnex.GNEX32Image
	memory       *gnex.GNEX32SymbolMemory
	media        *gnex.GNEX32MediaMemory
	service      ServiceHandler
	copyImage    func(x, y int32, index int) error
	copyImageDir func(x, y int32, index int, dir uint32) error
	copyImageEx  func(x, y int32, index int, alpha, dir uint32) error
	pc           int
	calls        []int
	marks        []int
	stack        []uint32
	refs         map[int]gnex32SymbolRef // candidate lvalues keyed by value-stack slot
	halted       bool
	fault        error
}

func New(image gnex.GNEX32Image) (*VM, error) {
	entry := int(image.Entry) - image.Header.BodyOffset
	if entry < 0 || entry&1 != 0 || entry >= len(image.Code) || len(image.Code)&1 != 0 {
		return nil, fmt.Errorf("gnex32: entry outside aligned code")
	}
	memory, err := image.NewSymbolMemory()
	if err != nil {
		return nil, err
	}
	media, err := image.NewMediaMemory()
	if err != nil {
		return nil, err
	}
	image.Code = bytes.Clone(image.Code)
	return &VM{image: image, memory: memory, media: media, pc: entry,
		refs: make(map[int]gnex32SymbolRef)}, nil
}

// NewWithServices enables explicit, caller-supplied behavior for candidate
// opcode 0x92. New leaves every service unsupported by default.
func NewWithServices(image gnex.GNEX32Image, service ServiceHandler) (*VM, error) {
	vm, err := New(image)
	if err != nil {
		return nil, err
	}
	vm.service = service
	return vm, nil
}

func (vm *VM) PC() int         { return vm.pc }
func (vm *VM) Halted() bool    { return vm.halted }
func (vm *VM) StackDepth() int { return len(vm.stack) }
func (vm *VM) StackTop() uint32 {
	if len(vm.stack) == 0 {
		return 0
	}
	return vm.stack[len(vm.stack)-1]
}

// BeginEvent enters one validated header event slot after a completed dispatch.
// The supplied version-4 image's event callbacks read swData from symbol zero.
func (vm *VM) BeginEvent(slot int, eventData uint32) error {
	if vm.fault != nil {
		return vm.fault
	}
	if !vm.halted {
		return ErrEventRunning
	}
	if slot <= 0 || slot >= len(vm.image.CodePointers) || vm.image.CodePointers[slot] == 0 {
		return ErrEventUnavailable
	}
	pc := int(vm.image.CodePointers[slot]) - vm.image.Header.BodyOffset
	if pc < 0 || pc&1 != 0 || pc >= len(vm.image.Code) {
		return ErrEventUnavailable
	}
	if err := vm.memory.WriteWord(0, 0, eventData); err != nil {
		return err
	}
	vm.pc = pc
	vm.stack = vm.stack[:0]
	vm.calls = vm.calls[:0]
	vm.marks = vm.marks[:0]
	clear(vm.refs)
	vm.halted = false
	return nil
}

func (vm *VM) Step() error {
	if vm.fault != nil {
		return vm.fault
	}
	if vm.halted {
		return nil
	}
	offset := vm.pc
	fail := func(err error) error {
		vm.fault = fmt.Errorf("gnex32: code offset %d: %w", offset, err)
		return vm.fault
	}
	if offset < 0 || len(vm.image.Code)-offset < 2 {
		return fail(ErrTruncated)
	}
	opcode := binary.LittleEndian.Uint16(vm.image.Code[offset:])
	switch opcode {
	case 0xff: // end: SDK assembler's main-program terminator
		vm.halted = true
		vm.pc += 2
		return nil
	case 0x81: // ujp: SDK assembler's unconditional absolute branch
		instruction, err := vm.image.DecodeAddressInstruction(offset)
		if err != nil {
			return fail(err)
		}
		vm.pc = instruction.Target
	case 0x85: // candidate call: far targets and function-like entry patterns
		instruction, err := vm.image.DecodeAddressInstruction(offset)
		if err != nil {
			return fail(err)
		}
		if len(vm.calls) >= 1024 {
			return fail(ErrCallStackOverflow)
		}
		vm.calls = append(vm.calls, instruction.Next)
		vm.pc = instruction.Target
	case 0x86: // candidate return: appears at function ends, including code[0]
		vm.pc += 2
		if len(vm.calls) == 0 {
			vm.halted = true
			return nil
		}
		vm.pc = vm.calls[len(vm.calls)-1]
		vm.calls = vm.calls[:len(vm.calls)-1]
	case 0x04: // candidate 32-bit form of GVM symbol-word push
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		index := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		value, err := vm.memory.ReadWord(index, 0)
		if err != nil {
			return fail(err)
		}
		vm.stack = append(vm.stack, value)
		vm.pc += 4
	case 0x02: // pushi: symbol element selected by another symbol
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		index, err := vm.memory.ReadWord(indexSymbol, 0)
		if err != nil {
			return fail(err)
		}
		var value uint32
		if int32(index) >= 0 {
			value, err = vm.memory.ReadWord(symbol, int(index))
			if err != nil {
				return fail(err)
			}
		}
		vm.stack = append(vm.stack, value)
		vm.pc += 6
	case 0x01: // pushx: symbol element indexed by a constant element of another symbol
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		indexElement := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		index, err := vm.memory.ReadWord(indexSymbol, indexElement)
		if err != nil {
			return fail(err)
		}
		value, err := vm.memory.ReadWord(symbol, int(int32(index)))
		if err != nil {
			return fail(err)
		}
		vm.stack = append(vm.stack, value)
		vm.pc += 8
	case 0x03: // candidate indexed symbol-word push
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		index := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		value, err := vm.memory.ReadWord(index, element)
		if err != nil {
			return fail(err)
		}
		vm.stack = append(vm.stack, value)
		vm.pc += 6
	case 0x05: // candidate sign-extended 16-bit immediate push
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		value := uint32(int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))))
		vm.stack = append(vm.stack, value)
		vm.pc += 4
	case 0x06: // pushmi: SDK assembler emits a 16-bit media index
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		index := uint32(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		vm.stack = append(vm.stack, index)
		vm.pc += 4
	case 0x07: // pushw: SDK assembler emits the high word before the low word
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		hi := uint32(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		lo := uint32(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		vm.stack = append(vm.stack, hi<<16|lo)
		vm.pc += 6
	case 0x0b: // candidate pop of one 32-bit value into a symbol
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		index := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		if err := vm.memory.WriteWord(index, 0, vm.stack[len(vm.stack)-1]); err != nil {
			return fail(err)
		}
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-1]
		vm.pc += 4
	case 0x0a: // popic: pop into constant-indexed symbol element
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		if err := vm.memory.WriteWord(symbol, element, vm.stack[len(vm.stack)-1]); err != nil {
			return fail(err)
		}
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-1]
		vm.pc += 6
	case 0x09: // popi: pop into a symbol element selected by another symbol
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		index, err := vm.memory.ReadWord(indexSymbol, 0)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(symbol, int(int32(index)), vm.stack[len(vm.stack)-1]); err != nil {
			return fail(err)
		}
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-1]
		vm.pc += 6
	case 0x0c: // ssp: save value-stack depth for a branch block
		if len(vm.marks) >= 1024 {
			return fail(ErrStackOverflow)
		}
		vm.marks = append(vm.marks, len(vm.stack))
		vm.pc += 2
	case 0x0d: // rsp: restore the depth saved by the nearest ssp
		if len(vm.marks) == 0 {
			return fail(ErrStackMarkUnderflow)
		}
		mark := vm.marks[len(vm.marks)-1]
		vm.marks = vm.marks[:len(vm.marks)-1]
		for slot := mark; slot < len(vm.stack); slot++ {
			delete(vm.refs, slot)
		}
		vm.stack = vm.stack[:mark]
		vm.pc += 2
	case 0x0e: // inc: SDK assembler's top-value increment
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		vm.stack[len(vm.stack)-1]++
		vm.pc += 2
	case 0x0f: // candidate decrement of the top 32-bit value
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		vm.stack[len(vm.stack)-1]--
		vm.pc += 2
	case 0x74: // candidate direct symbol-word copy, destination then source
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		value, err := vm.memory.ReadWord(source, 0)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, 0, value); err != nil {
			return fail(err)
		}
		vm.pc += 6
	case 0x72: // zseti: scalar destination from indexed source
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		index, err := vm.memory.ReadWord(indexSymbol, 0)
		if err != nil {
			return fail(err)
		}
		value, err := vm.memory.ReadWord(source, int(index))
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, 0, value); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x73: // zsetn: scalar destination from constant-indexed source
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		value, err := vm.memory.ReadWord(source, element)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, 0, value); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x69: // isetc: indexed symbol assignment from signed immediate
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		value := uint32(int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))))
		index, err := vm.memory.ReadWord(indexSymbol, 0)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, int(index), value); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x68: // isetz: indexed destination from scalar symbol
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		index, err := vm.memory.ReadWord(indexSymbol, 0)
		if err != nil {
			return fail(err)
		}
		value, err := vm.memory.ReadWord(source, 0)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, int(index), value); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x66: // iseti: indexed destination from indexed source
		if len(vm.image.Code)-offset < 10 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		destinationIndex := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		sourceIndex := int(binary.LittleEndian.Uint16(vm.image.Code[offset+8:]))
		di, err := vm.memory.ReadWord(destinationIndex, 0)
		if err != nil {
			return fail(err)
		}
		si, err := vm.memory.ReadWord(sourceIndex, 0)
		if err != nil {
			return fail(err)
		}
		value, err := vm.memory.ReadWord(source, int(si))
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, int(di), value); err != nil {
			return fail(err)
		}
		vm.pc += 10
	case 0x6f: // nsetc: constant-indexed symbol assignment from signed immediate
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		value := uint32(int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))))
		if err := vm.memory.WriteWord(destination, element, value); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x6d: // nsetn: constant-indexed destination from constant-indexed source
		if len(vm.image.Code)-offset < 10 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		destinationElement := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		sourceElement := int(binary.LittleEndian.Uint16(vm.image.Code[offset+8:]))
		value, err := vm.memory.ReadWord(source, sourceElement)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, destinationElement, value); err != nil {
			return fail(err)
		}
		vm.pc += 10
	case 0x6e: // nsetz: constant-indexed destination from scalar source
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		source := int(binary.LittleEndian.Uint16(vm.image.Code[offset+6:]))
		value, err := vm.memory.ReadWord(source, 0)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(destination, element, value); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x75: // candidate direct signed-immediate assignment to a symbol
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		destination := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		value := uint32(int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))))
		if err := vm.memory.WriteWord(destination, 0, value); err != nil {
			return fail(err)
		}
		vm.pc += 6
	case 0x7a: // incz: SDK assembler encodes symbol then signed increment
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		increment := int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+4:])))
		value, err := vm.memory.ReadWord(symbol, 0)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(symbol, 0, value+uint32(increment)); err != nil {
			return fail(err)
		}
		vm.pc += 6
	case 0x78: // inci: increment an element selected by a symbol
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		indexSymbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		increment := int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+6:])))
		index, err := vm.memory.ReadWord(indexSymbol, 0)
		if err != nil {
			return fail(err)
		}
		value, err := vm.memory.ReadWord(symbol, int(index))
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(symbol, int(index), value+uint32(increment)); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x79: // incn: increment a constant-indexed element
		if len(vm.image.Code)-offset < 8 {
			return fail(ErrTruncated)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		increment := int32(int16(binary.LittleEndian.Uint16(vm.image.Code[offset+6:])))
		value, err := vm.memory.ReadWord(symbol, element)
		if err != nil {
			return fail(err)
		}
		if err := vm.memory.WriteWord(symbol, element, value+uint32(increment)); err != nil {
			return fail(err)
		}
		vm.pc += 8
	case 0x10: // candidate duplicate of the top 32-bit value
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		if ref, ok := vm.refs[len(vm.stack)-1]; ok {
			vm.refs[len(vm.stack)] = ref
		}
		vm.stack = append(vm.stack, vm.stack[len(vm.stack)-1])
		vm.pc += 2
	case 0x11: // neg: signed arithmetic negation
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		vm.stack[last] = uint32(-int32(vm.stack[last]))
		delete(vm.refs, last)
		vm.pc += 2
	case 0x5d, 0x5e: // SDK eq/ne comparisons of the top two 32-bit values
		if len(vm.stack) < 2 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		var result uint32
		if opcode == 0x5d && vm.stack[last-1] == vm.stack[last] || opcode == 0x5e && vm.stack[last-1] != vm.stack[last] {
			result = 1
		}
		vm.stack[last-1] = result
		delete(vm.refs, last-1)
		delete(vm.refs, last)
		vm.stack = vm.stack[:last]
		vm.pc += 2
	case 0x55: // not: logical inversion
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		if vm.stack[last] == 0 {
			vm.stack[last] = 1
		} else {
			vm.stack[last] = 0
		}
		delete(vm.refs, last)
		vm.pc += 2
	case 0x59, 0x5a, 0x5b, 0x5c: // SDK signed gt/lt/ge/le of the top two values
		if len(vm.stack) < 2 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		var result uint32
		if opcode == 0x59 && int32(vm.stack[last-1]) > int32(vm.stack[last]) ||
			opcode == 0x5a && int32(vm.stack[last-1]) < int32(vm.stack[last]) ||
			opcode == 0x5b && int32(vm.stack[last-1]) >= int32(vm.stack[last]) ||
			opcode == 0x5c && int32(vm.stack[last-1]) <= int32(vm.stack[last]) {
			result = 1
		}
		vm.stack[last-1] = result
		delete(vm.refs, last-1)
		delete(vm.refs, last)
		vm.stack = vm.stack[:last]
		vm.pc += 2
	case 0x12: // swp: confirmed by the SDK assembler's paired SAL/SGS output
		if len(vm.stack) < 2 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		vm.stack[last-1], vm.stack[last] = vm.stack[last], vm.stack[last-1]
		left, leftOK := vm.refs[last-1]
		right, rightOK := vm.refs[last]
		delete(vm.refs, last-1)
		delete(vm.refs, last)
		if leftOK {
			vm.refs[last] = left
		}
		if rightOK {
			vm.refs[last-1] = right
		}
		vm.pc += 2
	case 0x13, 0x14, 0x50, 0x51, 0x52, 0x53, 0x54, 0x56, 0x57, 0x58: // SDK SAL/SGS arithmetic and bit opcodes
		if len(vm.stack) < 2 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		a, b := vm.stack[last-1], vm.stack[last]
		var resultRef gnex32SymbolRef
		var hasResultRef bool
		if opcode == 0x13 {
			if ref, ok := vm.refs[last-1]; ok {
				resultRef, hasResultRef = ref, true
				resultRef.element += int(int32(b))
			} else if ref, ok := vm.refs[last]; ok {
				resultRef, hasResultRef = ref, true
				resultRef.element += int(int32(a))
			}
		}
		switch opcode {
		case 0x13:
			vm.stack[last-1] = a + b
		case 0x14:
			vm.stack[last-1] = a - b
		case 0x50:
			vm.stack[last-1] = a * b
		case 0x51:
			if b == 0 {
				return fail(ErrDivisionByZero)
			}
			vm.stack[last-1] = uint32(int32(a) / int32(b))
		case 0x52:
			if b == 0 {
				return fail(ErrDivisionByZero)
			}
			vm.stack[last-1] = uint32(int32(a) % int32(b))
		case 0x53:
			vm.stack[last-1] = a & b
		case 0x54:
			vm.stack[last-1] = a | b
		case 0x56:
			vm.stack[last-1] = a ^ b
		case 0x57:
			vm.stack[last-1] = a >> b
		case 0x58:
			vm.stack[last-1] = a << b
		}
		delete(vm.refs, last-1)
		delete(vm.refs, last)
		if hasResultRef {
			vm.refs[last-1] = resultRef
		}
		vm.stack = vm.stack[:last]
		vm.pc += 2
	case 0x8d: // candidate indexed symbol lvalue
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		if _, err := vm.memory.ReadWord(symbol, element); err != nil {
			return fail(err)
		}
		vm.refs[len(vm.stack)] = gnex32SymbolRef{symbol: symbol, element: element}
		vm.stack = append(vm.stack, 0)
		vm.pc += 6
	case 0x8b: // stnic: copy the top value into a constant-indexed symbol
		if len(vm.image.Code)-offset < 6 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		element := int(binary.LittleEndian.Uint16(vm.image.Code[offset+4:]))
		if err := vm.memory.WriteWord(symbol, element, vm.stack[len(vm.stack)-1]); err != nil {
			return fail(err)
		}
		vm.pc += 6
	case 0x8c: // stnz: copy the top value into a scalar symbol
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		if err := vm.memory.WriteWord(symbol, 0, vm.stack[len(vm.stack)-1]); err != nil {
			return fail(err)
		}
		vm.pc += 4
	case 0x8e: // ldrz: direct symbol lvalue, confirmed by SDK assembler
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		if len(vm.stack) >= 4096 {
			return fail(ErrStackOverflow)
		}
		symbol := int(binary.LittleEndian.Uint16(vm.image.Code[offset+2:]))
		if _, err := vm.memory.ReadWord(symbol, 0); err != nil {
			return fail(err)
		}
		vm.refs[len(vm.stack)] = gnex32SymbolRef{symbol: symbol}
		vm.stack = append(vm.stack, 0)
		vm.pc += 4
	case 0x8f: // load the word addressed by a stack lvalue
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		slot := len(vm.stack) - 1
		ref, ok := vm.refs[slot]
		if !ok {
			return fail(ErrInvalidLValue)
		}
		value, err := vm.memory.ReadWord(ref.symbol, ref.element)
		if err != nil {
			return fail(err)
		}
		delete(vm.refs, slot)
		vm.stack[slot] = value
		vm.pc += 2
	case 0x90: // candidate assignment through a stack lvalue
		if len(vm.stack) < 2 {
			return fail(ErrStackUnderflow)
		}
		ref, ok := vm.refs[len(vm.stack)-2]
		if !ok {
			return fail(ErrInvalidLValue)
		}
		if err := vm.memory.WriteWord(ref.symbol, ref.element, vm.stack[len(vm.stack)-1]); err != nil {
			return fail(err)
		}
		delete(vm.refs, len(vm.stack)-2)
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-2]
		vm.pc += 2
	case 0x91: // stan: assign through a stack lvalue and retain the value
		if len(vm.stack) < 2 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		ref, ok := vm.refs[last-1]
		if !ok {
			return fail(ErrInvalidLValue)
		}
		value := vm.stack[last]
		if err := vm.memory.WriteWord(ref.symbol, ref.element, value); err != nil {
			return fail(err)
		}
		delete(vm.refs, last-1)
		delete(vm.refs, last)
		vm.stack[last-1] = value
		vm.stack = vm.stack[:last]
		vm.pc += 2
	case 0x7b, 0x7c, 0x7d, 0x7e: // SDK gtjp/ltjp/gejp/lejp: signed immediate comparisons
		instruction, err := vm.image.DecodeAddressInstruction(offset)
		if err != nil {
			return fail(err)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		value := int32(vm.stack[len(vm.stack)-1])
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-1]
		comparison := int32(int16(instruction.Word))
		branch := opcode == 0x7b && value > comparison || opcode == 0x7c && value < comparison || opcode == 0x7d && value >= comparison || opcode == 0x7e && value <= comparison
		if branch {
			vm.pc = instruction.Target
		} else {
			vm.pc = instruction.Next
		}
	case 0x7f, 0x80: // SDK eqjp/nejp compare top with signed immediate word
		instruction, err := vm.image.DecodeAddressInstruction(offset)
		if err != nil {
			return fail(err)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		value := vm.stack[len(vm.stack)-1]
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-1]
		comparison := value == uint32(int32(int16(instruction.Word)))
		if opcode == 0x80 {
			comparison = !comparison
		}
		if comparison {
			vm.pc = instruction.Target
		} else {
			vm.pc = instruction.Next
		}
	case 0x82, 0x83: // tjp/fjp branch on the top value
		instruction, err := vm.image.DecodeAddressInstruction(offset)
		if err != nil {
			return fail(err)
		}
		if len(vm.stack) == 0 {
			return fail(ErrStackUnderflow)
		}
		condition := vm.stack[len(vm.stack)-1]
		delete(vm.refs, len(vm.stack)-1)
		vm.stack = vm.stack[:len(vm.stack)-1]
		if (opcode == 0x82 && condition != 0) || (opcode == 0x83 && condition == 0) {
			vm.pc = instruction.Target
		} else {
			vm.pc = instruction.Next
		}
	case 0x92: // candidate built-in service dispatch with a 16-bit ID
		if len(vm.image.Code)-offset < 4 {
			return fail(ErrTruncated)
		}
		id := binary.LittleEndian.Uint16(vm.image.Code[offset+2:])
		if vm.service == nil {
			vm.fault = &UnsupportedServiceError{ID: id, Offset: offset}
			return vm.fault
		}
		input := append([]uint32(nil), vm.stack...)
		result, err := vm.service(id, input)
		if err != nil {
			return fail(err)
		}
		if result.Pop < 0 || result.Pop > len(vm.stack) ||
			len(vm.stack)-result.Pop+len(result.Push) > 4096 {
			return fail(ErrServiceStackEffect)
		}
		for slot := len(vm.stack) - result.Pop; slot < len(vm.stack); slot++ {
			delete(vm.refs, slot)
		}
		vm.stack = append(vm.stack[:len(vm.stack)-result.Pop], result.Push...)
		vm.pc += 4
	case 0x94: // CopyImage(x, y, image), as emitted by the SDK assembler
		if vm.copyImage == nil {
			vm.fault = &UnsupportedOpcodeError{Opcode: opcode, Offset: offset}
			return vm.fault
		}
		if len(vm.stack) < 3 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		if err := vm.copyImage(int32(vm.stack[last-2]), int32(vm.stack[last-1]), int(vm.stack[last])); err != nil {
			return fail(err)
		}
		for slot := last - 2; slot <= last; slot++ {
			delete(vm.refs, slot)
		}
		vm.stack = vm.stack[:last-2]
		vm.pc += 2
	case 0x95: // CopyImageDir(x, y, image, direction)
		if len(vm.stack) < 4 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		var err error
		if vm.copyImageDir != nil {
			err = vm.copyImageDir(int32(vm.stack[last-3]), int32(vm.stack[last-2]), int(vm.stack[last-1]), vm.stack[last])
		} else if vm.copyImage != nil && vm.stack[last] == 0 {
			err = vm.copyImage(int32(vm.stack[last-3]), int32(vm.stack[last-2]), int(vm.stack[last-1]))
		} else {
			return fail(&UnsupportedOpcodeError{Opcode: opcode, Offset: offset})
		}
		if err != nil {
			return fail(err)
		}
		for slot := last - 3; slot <= last; slot++ {
			delete(vm.refs, slot)
		}
		vm.stack = vm.stack[:last-3]
		vm.pc += 2
	case 0x98: // CopyImageEx(x, y, image, alpha, zoom, mirror, rotation)
		if len(vm.stack) < 7 {
			return fail(ErrStackUnderflow)
		}
		last := len(vm.stack) - 1
		if vm.copyImageEx == nil || vm.stack[last-3] > 3 || vm.stack[last-2] != 0 || vm.stack[last] != 0 || vm.stack[last-1] > 1 {
			return fail(fmt.Errorf("unsupported CopyImageEx alpha=%d zoom=%d mirror=%d rotation=%d: %w", vm.stack[last-3], vm.stack[last-2], vm.stack[last-1], vm.stack[last], &UnsupportedOpcodeError{Opcode: opcode, Offset: offset}))
		}
		err := vm.copyImageEx(int32(vm.stack[last-6]), int32(vm.stack[last-5]), int(vm.stack[last-4]), vm.stack[last-3], vm.stack[last-1])
		if err != nil {
			return fail(err)
		}
		for slot := last - 6; slot <= last; slot++ {
			delete(vm.refs, slot)
		}
		vm.stack = vm.stack[:last-6]
		vm.pc += 2
	default:
		vm.fault = &UnsupportedOpcodeError{Opcode: opcode, Offset: offset}
		return vm.fault
	}
	return nil
}

func (vm *VM) Run(budget uint64) error {
	if vm.fault != nil {
		return vm.fault
	}
	if vm.halted {
		return nil
	}
	for ; budget > 0; budget-- {
		if err := vm.Step(); err != nil {
			return err
		}
		if vm.halted {
			return nil
		}
	}
	return ErrBudget
}
