// Package interpreter provides ARAM's portable, pure-Go ARM/Thumb CPU
// fallback. The initial implementation deliberately faults on instructions it
// does not understand instead of silently treating them as no-ops.
package interpreter

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/bits"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	flagN uint32 = 1 << 31
	flagZ uint32 = 1 << 30
	flagC uint32 = 1 << 29
	flagV uint32 = 1 << 28
	flagT        = cpu.StatusThumb
)

const (
	BackendName        = "portable-interpreter"
	BackendVersion     = "1"
	DefaultMemoryLimit = uint64(512 << 20)
)

type region struct {
	address     uint32
	size        uint32
	permissions cpu.Permissions
	data        []byte
}

// Backend is a bounds-checked ARMv5TE interpreter. It currently implements
// the ARM/Thumb control-flow and integer instructions needed by the first
// application-entry milestone; unsupported encodings produce a precise fault.
type Backend struct {
	mu          sync.Mutex
	regions     []region
	regs        [17]uint32
	mode        cpu.Mode
	stopped     atomic.Bool
	closed      bool
	mapped      uint64
	memoryLimit uint64
}

func New() *Backend {
	return NewWithMemoryLimit(DefaultMemoryLimit)
}

func NewWithMemoryLimit(limit uint64) *Backend {
	return &Backend{mode: cpu.ModeARM, memoryLimit: limit}
}

func (b *Backend) Identity() cpu.Identity {
	return cpu.Identity{
		Name:         BackendName,
		Version:      BackendVersion,
		Architecture: cpu.ARMv5TE,
	}
}

func (b *Backend) Architecture() cpu.Architecture {
	return cpu.ARMv5TE
}

func (b *Backend) Map(address, size uint32, permissions cpu.Permissions) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return cpu.ErrClosed
	}
	end := uint64(address) + uint64(size)
	if size == 0 || end > 1<<32 || !permissions.Valid() ||
		uint64(size) > b.memoryLimit-b.mapped {
		return cpu.ErrInvalidMapping
	}
	for _, mapped := range b.regions {
		mappedEnd := uint64(mapped.address) + uint64(mapped.size)
		if uint64(address) < mappedEnd && uint64(mapped.address) < end {
			return fmt.Errorf("%w: 0x%08x..0x%08x overlaps 0x%08x..0x%08x",
				cpu.ErrInvalidMapping,
				address,
				end,
				mapped.address,
				mappedEnd,
			)
		}
	}
	b.regions = append(b.regions, region{
		address:     address,
		size:        size,
		permissions: permissions,
		data:        make([]byte, int(size)),
	})
	b.mapped += uint64(size)
	sort.Slice(b.regions, func(i, j int) bool {
		return b.regions[i].address < b.regions[j].address
	})
	return nil
}

func (b *Backend) ReadMemory(address uint32, destination []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	return b.copyOut(address, destination, cpu.PermissionRead)
}

func (b *Backend) WriteMemory(address uint32, source []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	return b.copyIn(address, source, cpu.PermissionWrite)
}

func (b *Backend) ReadRegister(id uint32) (uint32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, cpu.ErrClosed
	}
	if id >= uint32(len(b.regs)) {
		return 0, fmt.Errorf("register %d: %w", id, cpu.ErrInvalidAddress)
	}
	return b.regs[id], nil
}

func (b *Backend) WriteRegister(id, value uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if id >= uint32(len(b.regs)) {
		return fmt.Errorf("register %d: %w", id, cpu.ErrInvalidAddress)
	}
	b.regs[id] = value
	if id == cpu.RegisterCPSR {
		if value&cpu.StatusThumb != 0 {
			b.mode = cpu.ModeThumb
		} else {
			b.mode = cpu.ModeARM
		}
	}
	return nil
}

func (b *Backend) Run(ctx context.Context, address uint32, mode cpu.Mode, budget uint64) cpu.Result {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: cpu.ErrClosed}
	}
	if mode != cpu.ModeARM && mode != cpu.ModeThumb {
		return cpu.Result{
			Reason: cpu.StopFault,
			PC:     address,
			Err:    fmt.Errorf("CPU mode %d: %w", mode, cpu.ErrInvalidAddress),
		}
	}
	if mode == cpu.ModeARM && address&3 != 0 || mode == cpu.ModeThumb && address&1 != 0 {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: cpu.ErrInvalidAddress}
	}

	b.mode = mode
	b.setModeFlag()
	b.regs[cpu.RegisterPC] = address
	b.stopped.Store(false)

	var executed uint64
	for budget == 0 || executed < budget {
		if b.stopped.Load() {
			return cpu.Result{
				Reason:       cpu.StopRequested,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          cpu.ErrStopped,
			}
		}
		select {
		case <-ctx.Done():
			return cpu.Result{
				Reason:       cpu.StopRequested,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          ctx.Err(),
			}
		default:
		}

		var (
			reason *cpu.StopReason
			err    error
		)
		if b.mode == cpu.ModeThumb {
			reason, err = b.stepThumb()
		} else {
			reason, err = b.stepARM()
		}
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
				Err:          err,
			}
		}
		executed++
		if reason != nil {
			return cpu.Result{
				Reason:       *reason,
				Instructions: executed,
				PC:           b.regs[cpu.RegisterPC],
			}
		}
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: executed,
		PC:           b.regs[cpu.RegisterPC],
	}
}

func (b *Backend) Stop() error {
	b.stopped.Store(true)
	return nil
}

func (b *Backend) SaveContext() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, cpu.ErrClosed
	}
	data := make([]byte, 4+4+len(b.regs)*4+4)
	copy(data, "ARMC")
	binary.LittleEndian.PutUint32(data[4:8], 1)
	offset := 8
	for _, value := range b.regs {
		binary.LittleEndian.PutUint32(data[offset:offset+4], value)
		offset += 4
	}
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(b.mode))
	return data, nil
}

func (b *Backend) RestoreContext(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	expected := 4 + 4 + len(b.regs)*4 + 4
	if len(data) != expected || string(data[:4]) != "ARMC" ||
		binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return fmt.Errorf("CPU context: %w", cpu.ErrInvalidAddress)
	}
	var restored [17]uint32
	offset := 8
	for index := range restored {
		restored[index] = binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4
	}
	mode := cpu.Mode(binary.LittleEndian.Uint32(data[offset : offset+4]))
	if !mode.Valid() {
		return fmt.Errorf("CPU context mode: %w", cpu.ErrInvalidAddress)
	}
	b.regs = restored
	b.mode = mode
	b.setModeFlag()
	return nil
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	b.regions = nil
	b.mapped = 0
	return nil
}

func (b *Backend) stepThumb() (*cpu.StopReason, error) {
	pc := b.regs[cpu.RegisterPC]
	instruction, err := b.read16(pc, cpu.PermissionExecute)
	if err != nil {
		return nil, fmt.Errorf("Thumb fetch at 0x%08x: %w", pc, err)
	}
	next := pc + 2
	b.regs[cpu.RegisterPC] = next

	switch {
	case instruction&0xff00 == 0xbe00: // BKPT
		reason := cpu.StopBreakpoint
		return &reason, nil

	case instruction&0xe000 == 0x0000 &&
		instruction&0x1800 != 0x1800: // LSL/LSR/ASR immediate
		op := uint32(instruction>>11) & 3
		shift := uint32(instruction>>6) & 0x1f
		rs := uint32(instruction>>3) & 7
		rd := uint32(instruction) & 7
		value := b.regs[rs]
		var result uint32
		var carry bool
		switch op {
		case 0: // LSL
			if shift == 0 {
				result = value
				carry = b.regs[cpu.RegisterCPSR]&flagC != 0
			} else {
				result = value << shift
				carry = value&(uint32(1)<<(32-shift)) != 0
			}
		case 1: // LSR, immediate zero encodes a shift of 32
			if shift == 0 {
				result = 0
				carry = value&flagN != 0
			} else {
				result = value >> shift
				carry = value&(uint32(1)<<(shift-1)) != 0
			}
		case 2: // ASR, immediate zero encodes a shift of 32
			if shift == 0 {
				carry = value&flagN != 0
				if carry {
					result = ^uint32(0)
				}
			} else {
				result = uint32(int32(value) >> shift)
				carry = value&(uint32(1)<<(shift-1)) != 0
			}
		default:
			return nil, b.unsupportedThumb(pc, instruction)
		}
		b.regs[rd] = result
		b.setNZC(result, carry)
		return nil, nil

	case instruction&0xf800 == 0x2000: // MOVS Rd, #imm8
		rd := uint32(instruction>>8) & 7
		value := uint32(instruction & 0xff)
		b.regs[rd] = value
		b.setNZ(value)
		return nil, nil

	case instruction&0xf800 == 0x2800: // CMP Rd, #imm8
		rd := uint32(instruction>>8) & 7
		result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
		b.setNZCV(result, carry, overflow)
		return nil, nil

	case instruction&0xf800 == 0x3000: // ADDS Rd, #imm8
		rd := uint32(instruction>>8) & 7
		result, carry, overflow := addWithCarry(b.regs[rd], uint32(instruction&0xff), 0)
		b.regs[rd] = result
		b.setNZCV(result, carry, overflow)
		return nil, nil

	case instruction&0xf800 == 0x3800: // SUBS Rd, #imm8
		rd := uint32(instruction>>8) & 7
		result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
		b.regs[rd] = result
		b.setNZCV(result, carry, overflow)
		return nil, nil

	case instruction&0xf800 == 0x1800: // ADD/SUB register or immediate3
		immediate := instruction&(1<<10) != 0
		subtract := instruction&(1<<9) != 0
		rnOrImmediate := uint32(instruction>>6) & 7
		rs := uint32(instruction>>3) & 7
		rd := uint32(instruction) & 7
		right := rnOrImmediate
		if !immediate {
			right = b.regs[rnOrImmediate]
		}
		var result uint32
		var carry, overflow bool
		if subtract {
			result, carry, overflow = addWithCarry(b.regs[rs], ^right, 1)
		} else {
			result, carry, overflow = addWithCarry(b.regs[rs], right, 0)
		}
		b.regs[rd] = result
		b.setNZCV(result, carry, overflow)
		return nil, nil

	case instruction&0xfc00 == 0x4000: // ALU operations
		op := (instruction >> 6) & 0xf
		rs := uint32(instruction>>3) & 7
		rd := uint32(instruction) & 7
		left, right := b.regs[rd], b.regs[rs]
		switch op {
		case 0x0: // AND
			b.regs[rd] = left & right
			b.setNZ(b.regs[rd])
		case 0x1: // EOR
			b.regs[rd] = left ^ right
			b.setNZ(b.regs[rd])
		case 0x2: // LSL by register
			result, carry := shiftLSL(left, uint8(right), b.carry())
			b.regs[rd] = result
			b.setNZC(result, carry)
		case 0x3: // LSR by register
			result, carry := shiftLSR(left, uint8(right), b.carry())
			b.regs[rd] = result
			b.setNZC(result, carry)
		case 0x4: // ASR by register
			result, carry := shiftASR(left, uint8(right), b.carry())
			b.regs[rd] = result
			b.setNZC(result, carry)
		case 0x5: // ADC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow := addWithCarry(left, right, carryIn)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
		case 0x6: // SBC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow := addWithCarry(left, ^right, carryIn)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
		case 0x7: // ROR by register
			result, carry := shiftROR(left, uint8(right), b.carry())
			b.regs[rd] = result
			b.setNZC(result, carry)
		case 0x8: // TST
			b.setNZ(left & right)
		case 0x9: // NEG
			result, carry, overflow := addWithCarry(0, ^right, 1)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
		case 0xa: // CMP
			result, carry, overflow := addWithCarry(left, ^right, 1)
			b.setNZCV(result, carry, overflow)
		case 0xb: // CMN
			result, carry, overflow := addWithCarry(left, right, 0)
			b.setNZCV(result, carry, overflow)
		case 0xc: // ORR
			b.regs[rd] = left | right
			b.setNZ(b.regs[rd])
		case 0xd: // MUL
			b.regs[rd] = left * right
			b.setNZ(b.regs[rd])
		case 0xe: // BIC
			b.regs[rd] = left &^ right
			b.setNZ(b.regs[rd])
		case 0xf: // MVN
			b.regs[rd] = ^right
			b.setNZ(b.regs[rd])
		default:
			return nil, b.unsupportedThumb(pc, instruction)
		}
		return nil, nil

	case instruction&0xfc00 == 0x4400: // high-register ops / BX
		op := (instruction >> 8) & 3
		rs := uint32(instruction>>3)&7 | uint32(instruction>>6)&1<<3
		rd := uint32(instruction)&7 | uint32(instruction>>7)&1<<3
		switch op {
		case 0: // ADD
			result := b.readOperandRegister(rd, pc, cpu.ModeThumb) +
				b.readOperandRegister(rs, pc, cpu.ModeThumb)
			if rd == cpu.RegisterPC {
				result &^= 1
			}
			b.regs[rd] = result
		case 1: // CMP
			result, carry, overflow := addWithCarry(
				b.readOperandRegister(rd, pc, cpu.ModeThumb),
				^b.readOperandRegister(rs, pc, cpu.ModeThumb),
				1,
			)
			b.setNZCV(result, carry, overflow)
		case 2: // MOV
			result := b.readOperandRegister(rs, pc, cpu.ModeThumb)
			if rd == cpu.RegisterPC {
				result &^= 1
			}
			b.regs[rd] = result
		case 3: // BX
			target := b.readOperandRegister(rs, pc, cpu.ModeThumb)
			if instruction&(1<<7) != 0 { // BLX
				b.regs[cpu.RegisterLR] = (pc + 2) | 1
			}
			b.branchExchange(target)
		}
		return nil, nil

	case instruction&0xf800 == 0x4800: // LDR Rd, [PC, #imm]
		rd := uint32(instruction>>8) & 7
		address := ((pc + 4) &^ uint32(3)) +
			uint32(instruction&0xff)*4
		value, readErr := b.read32(address, cpu.PermissionRead)
		if readErr != nil {
			return nil, readErr
		}
		b.regs[rd] = value
		return nil, nil

	case instruction&0xf000 == 0x5000: // register-offset load/store
		op := uint32(instruction>>9) & 7
		ro := uint32(instruction>>6) & 7
		rb := uint32(instruction>>3) & 7
		rd := uint32(instruction) & 7
		address := b.regs[rb] + b.regs[ro]
		switch op {
		case 0: // STR
			if writeErr := b.write32(address, b.regs[rd], cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		case 1: // STRH
			if writeErr := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		case 2: // STRB
			if writeErr := b.copyIn(
				address,
				[]byte{byte(b.regs[rd])},
				cpu.PermissionWrite,
			); writeErr != nil {
				return nil, writeErr
			}
		case 3: // LDRSB
			var value [1]byte
			if readErr := b.copyOut(address, value[:], cpu.PermissionRead); readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(int32(int8(value[0])))
		case 4: // LDR
			value, readErr := b.read32(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = value
		case 5: // LDRH
			value, readErr := b.read16(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(value)
		case 6: // LDRB
			var value [1]byte
			if readErr := b.copyOut(address, value[:], cpu.PermissionRead); readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(value[0])
		case 7: // LDRSH
			value, readErr := b.read16(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(int32(int16(value)))
		}
		return nil, nil

	case instruction&0xe000 == 0x6000: // immediate word/byte load/store
		byteTransfer := instruction&(1<<12) != 0
		load := instruction&(1<<11) != 0
		offset := uint32(instruction>>6) & 0x1f
		rb := uint32(instruction>>3) & 7
		rd := uint32(instruction) & 7
		if !byteTransfer {
			offset *= 4
		}
		address := b.regs[rb] + offset
		switch {
		case load && byteTransfer:
			var value [1]byte
			if readErr := b.copyOut(address, value[:], cpu.PermissionRead); readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(value[0])
		case load:
			value, readErr := b.read32(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = value
		case byteTransfer:
			if writeErr := b.copyIn(
				address,
				[]byte{byte(b.regs[rd])},
				cpu.PermissionWrite,
			); writeErr != nil {
				return nil, writeErr
			}
		default:
			if writeErr := b.write32(address, b.regs[rd], cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		}
		return nil, nil

	case instruction&0xf000 == 0x8000: // immediate halfword load/store
		load := instruction&(1<<11) != 0
		offset := uint32(instruction>>6) & 0x1f
		rb := uint32(instruction>>3) & 7
		rd := uint32(instruction) & 7
		address := b.regs[rb] + offset*2
		if load {
			value, readErr := b.read16(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = uint32(value)
		} else if writeErr := b.write16(
			address,
			uint16(b.regs[rd]),
			cpu.PermissionWrite,
		); writeErr != nil {
			return nil, writeErr
		}
		return nil, nil

	case instruction&0xf000 == 0x9000: // SP-relative word load/store
		load := instruction&(1<<11) != 0
		rd := uint32(instruction>>8) & 7
		address := b.regs[cpu.RegisterSP] + uint32(instruction&0xff)*4
		if load {
			value, readErr := b.read32(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[rd] = value
		} else if writeErr := b.write32(
			address,
			b.regs[rd],
			cpu.PermissionWrite,
		); writeErr != nil {
			return nil, writeErr
		}
		return nil, nil

	case instruction&0xff00 == 0xb000: // ADD/SUB SP, #imm7*4
		offset := uint32(instruction&0x7f) * 4
		if instruction&(1<<7) != 0 {
			b.regs[cpu.RegisterSP] -= offset
		} else {
			b.regs[cpu.RegisterSP] += offset
		}
		return nil, nil

	case instruction&0xfe00 == 0xb400: // PUSH
		registers := uint16(instruction & 0xff)
		includeLR := instruction&(1<<8) != 0
		count := bits.OnesCount16(registers)
		if includeLR {
			count++
		}
		start := b.regs[cpu.RegisterSP] - uint32(count*4)
		address := start
		for register := uint32(0); register < 8; register++ {
			if registers&(1<<register) == 0 {
				continue
			}
			if writeErr := b.write32(address, b.regs[register], cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
			address += 4
		}
		if includeLR {
			if writeErr := b.write32(address, b.regs[cpu.RegisterLR], cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		}
		b.regs[cpu.RegisterSP] = start
		return nil, nil

	case instruction&0xfe00 == 0xbc00: // POP
		registers := uint16(instruction & 0xff)
		includePC := instruction&(1<<8) != 0
		address := b.regs[cpu.RegisterSP]
		for register := uint32(0); register < 8; register++ {
			if registers&(1<<register) == 0 {
				continue
			}
			value, readErr := b.read32(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.regs[register] = value
			address += 4
		}
		if includePC {
			value, readErr := b.read32(address, cpu.PermissionRead)
			if readErr != nil {
				return nil, readErr
			}
			b.branchExchange(value)
			address += 4
		}
		b.regs[cpu.RegisterSP] = address
		return nil, nil

	case instruction&0xf000 == 0xa000: // ADD Rd, PC/SP, #imm
		rd := uint32(instruction>>8) & 7
		base := b.regs[cpu.RegisterSP]
		if instruction&(1<<11) == 0 {
			base = (pc + 4) &^ uint32(3)
		}
		b.regs[rd] = base + uint32(instruction&0xff)*4
		return nil, nil

	case instruction&0xf000 == 0xc000: // STMIA/LDMIA Rb!, register list
		load := instruction&(1<<11) != 0
		rb := uint32(instruction>>8) & 7
		registers := uint16(instruction & 0xff)
		if registers == 0 {
			return nil, b.unsupportedThumb(pc, instruction)
		}
		address := b.regs[rb]
		for register := uint32(0); register < 8; register++ {
			if registers&(1<<register) == 0 {
				continue
			}
			if load {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return nil, readErr
				}
				b.regs[register] = value
			} else if writeErr := b.write32(
				address,
				b.regs[register],
				cpu.PermissionWrite,
			); writeErr != nil {
				return nil, writeErr
			}
			address += 4
		}
		// ARMv5 Thumb LDM suppresses writeback when the base register is in
		// the list; STM always performs writeback for the encodings used here.
		if !load || registers&(1<<rb) == 0 {
			b.regs[rb] = address
		}
		return nil, nil

	case instruction&0xf000 == 0xd000: // conditional branch / SWI
		condition := uint8(instruction>>8) & 0xf
		if condition == 0xf {
			reason := cpu.StopBreakpoint
			return &reason, nil
		}
		if condition == 0xe {
			return nil, b.unsupportedThumb(pc, instruction)
		}
		if b.conditionPassed(condition) {
			offset := int32(int8(instruction&0xff)) << 1
			b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + offset)
		}
		return nil, nil

	case instruction&0xf800 == 0xf000: // BL (two-halfword Thumb instruction)
		suffix, readErr := b.read16(pc+2, cpu.PermissionExecute)
		if readErr != nil {
			return nil, readErr
		}
		if suffix&0xf800 != 0xf800 {
			return nil, b.unsupportedThumb(pc, instruction)
		}
		high := int32(instruction & 0x7ff)
		if high&(1<<10) != 0 {
			high |= ^int32(0x7ff)
		}
		target := uint32(int32(pc+4)+(high<<12)) +
			uint32(suffix&0x7ff)*2
		b.regs[cpu.RegisterLR] = (pc + 4) | 1
		b.regs[cpu.RegisterPC] = target
		return nil, nil

	case instruction&0xf800 == 0xf800:
		return nil, b.unsupportedThumb(pc, instruction)

	case instruction&0xf800 == 0xe000: // unconditional branch
		offset := int32(instruction & 0x7ff)
		if offset&(1<<10) != 0 {
			offset |= ^int32(0x7ff)
		}
		b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + (offset << 1))
		return nil, nil

	default:
		return nil, b.unsupportedThumb(pc, instruction)
	}
}

func (b *Backend) stepARM() (*cpu.StopReason, error) {
	pc := b.regs[cpu.RegisterPC]
	instruction, err := b.read32(pc, cpu.PermissionExecute)
	if err != nil {
		return nil, fmt.Errorf("ARM fetch at 0x%08x: %w", pc, err)
	}
	b.regs[cpu.RegisterPC] = pc + 4
	condition := uint8(instruction >> 28)
	if condition != 0xf && !b.conditionPassed(condition) {
		return nil, nil
	}

	switch {
	case instruction&0x0ff000f0 == 0x01200070: // BKPT
		reason := cpu.StopBreakpoint
		return &reason, nil

	case instruction&0x0ffffff0 == 0x012fff10: // BX Rm
		b.branchExchange(b.readOperandRegister(instruction&0xf, pc, cpu.ModeARM))
		return nil, nil

	case instruction&0x0ffffff0 == 0x012fff30: // BLX Rm
		target := b.readOperandRegister(instruction&0xf, pc, cpu.ModeARM)
		b.regs[cpu.RegisterLR] = pc + 4
		b.branchExchange(target)
		return nil, nil

	case instruction&0xfe000000 == 0xfa000000: // BLX immediate
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		if instruction&(1<<24) != 0 {
			offset += 2
		}
		b.regs[cpu.RegisterLR] = pc + 4
		b.regs[cpu.RegisterPC] = uint32(int32(pc+8) + offset)
		b.mode = cpu.ModeThumb
		b.setModeFlag()
		return nil, nil

	case instruction&0x0fff0fff == 0x0e070f15:
		// MCR p15, 0, Rd, c7, c5, 0 invalidates the instruction cache.
		// Guest code is interpreted directly from coherent mapped memory, so
		// the architectural cache-maintenance operation has no host work.
		return nil, nil

	case instruction&0x0e000000 == 0x0a000000: // B / BL
		offset := int32(instruction&0x00ffffff) << 2
		if offset&(1<<25) != 0 {
			offset |= ^int32(0x03ffffff)
		}
		if instruction&(1<<24) != 0 {
			b.regs[cpu.RegisterLR] = pc + 4
		}
		b.regs[cpu.RegisterPC] = uint32(int32(pc+8) + offset)
		return nil, nil

	case instruction&0x0c000000 == 0x00000000: // data processing
		immediate := instruction&(1<<25) != 0
		opcode := uint8(instruction >> 21 & 0xf)
		setFlags := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		rd := uint32(instruction>>12) & 0xf
		var operand2 uint32
		operandCarry := b.carry()
		if immediate {
			rotate := int((instruction >> 8 & 0xf) * 2)
			operand2 = bits.RotateLeft32(uint32(instruction&0xff), -rotate)
			if rotate != 0 {
				operandCarry = operand2&flagN != 0
			}
		} else {
			rm := uint32(instruction & 0xf)
			value := b.readOperandRegister(rm, pc, cpu.ModeARM)
			shiftType := uint8(instruction>>5) & 3
			if instruction&(1<<4) == 0 {
				amount := uint8(instruction >> 7 & 0x1f)
				switch shiftType {
				case 0:
					operand2, operandCarry = shiftLSL(value, amount, operandCarry)
				case 1:
					if amount == 0 {
						amount = 32
					}
					operand2, operandCarry = shiftLSR(value, amount, operandCarry)
				case 2:
					if amount == 0 {
						amount = 32
					}
					operand2, operandCarry = shiftASR(value, amount, operandCarry)
				case 3:
					if amount == 0 {
						oldCarry := operandCarry
						operandCarry = value&1 != 0
						operand2 = value >> 1
						if oldCarry {
							operand2 |= flagN
						}
					} else {
						operand2, operandCarry = shiftROR(value, amount, operandCarry)
					}
				}
			} else {
				if instruction&(1<<7) != 0 {
					return nil, b.unsupportedARM(pc, instruction)
				}
				rs := uint32(instruction>>8) & 0xf
				amount := uint8(b.readOperandRegister(rs, pc, cpu.ModeARM))
				switch shiftType {
				case 0:
					operand2, operandCarry = shiftLSL(value, amount, operandCarry)
				case 1:
					operand2, operandCarry = shiftLSR(value, amount, operandCarry)
				case 2:
					operand2, operandCarry = shiftASR(value, amount, operandCarry)
				case 3:
					operand2, operandCarry = shiftROR(value, amount, operandCarry)
				}
			}
		}
		left := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var result uint32
		var carry, overflow bool
		writeResult := true
		arithmeticFlags := false
		switch opcode {
		case 0x0: // AND
			result = left & operand2
		case 0x1: // EOR
			result = left ^ operand2
		case 0x2: // SUB
			result, carry, overflow = addWithCarry(left, ^operand2, 1)
			arithmeticFlags = true
		case 0x3: // RSB
			result, carry, overflow = addWithCarry(operand2, ^left, 1)
			arithmeticFlags = true
		case 0x4: // ADD
			result, carry, overflow = addWithCarry(left, operand2, 0)
			arithmeticFlags = true
		case 0x5: // ADC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(left, operand2, carryIn)
			arithmeticFlags = true
		case 0x6: // SBC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(left, ^operand2, carryIn)
			arithmeticFlags = true
		case 0x7: // RSC
			carryIn := uint32(0)
			if b.carry() {
				carryIn = 1
			}
			result, carry, overflow = addWithCarry(operand2, ^left, carryIn)
			arithmeticFlags = true
		case 0x8: // TST
			result = left & operand2
			setFlags = true
			writeResult = false
		case 0x9: // TEQ
			result = left ^ operand2
			setFlags = true
			writeResult = false
		case 0xa: // CMP
			result, carry, overflow = addWithCarry(left, ^operand2, 1)
			setFlags = true
			writeResult = false
			arithmeticFlags = true
		case 0xb: // CMN
			result, carry, overflow = addWithCarry(left, operand2, 0)
			setFlags = true
			writeResult = false
			arithmeticFlags = true
		case 0xc: // ORR
			result = left | operand2
		case 0xd: // MOV
			result = operand2
		case 0xe: // BIC
			result = left &^ operand2
		case 0xf: // MVN
			result = ^operand2
		default:
			return nil, b.unsupportedARM(pc, instruction)
		}
		if writeResult {
			b.regs[rd] = result
		}
		if setFlags {
			if arithmeticFlags {
				b.setNZCV(result, carry, overflow)
			} else {
				b.setNZC(result, operandCarry)
			}
		}
		return nil, nil

	case instruction&0x0c000000 == 0x04000000: // LDR/STR
		registerOffset := instruction&(1<<25) != 0
		preIndex := instruction&(1<<24) != 0
		up := instruction&(1<<23) != 0
		byteTransfer := instruction&(1<<22) != 0
		writeBack := instruction&(1<<21) != 0
		load := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		rd := uint32(instruction>>12) & 0xf
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var offset uint32
		if !registerOffset {
			offset = uint32(instruction & 0xfff)
		} else {
			if instruction&(1<<4) != 0 {
				return nil, b.unsupportedARM(pc, instruction)
			}
			rm := uint32(instruction & 0xf)
			value := b.readOperandRegister(rm, pc, cpu.ModeARM)
			shiftType := uint8(instruction>>5) & 3
			amount := uint8(instruction >> 7 & 0x1f)
			switch shiftType {
			case 0:
				offset, _ = shiftLSL(value, amount, b.carry())
			case 1:
				if amount == 0 {
					amount = 32
				}
				offset, _ = shiftLSR(value, amount, b.carry())
			case 2:
				if amount == 0 {
					amount = 32
				}
				offset, _ = shiftASR(value, amount, b.carry())
			case 3:
				if amount == 0 {
					offset = value >> 1
					if b.carry() {
						offset |= flagN
					}
				} else {
					offset, _ = shiftROR(value, amount, b.carry())
				}
			}
		}
		indexedAddress := base
		if up {
			indexedAddress += offset
		} else {
			indexedAddress -= offset
		}
		address := base
		if preIndex {
			address = indexedAddress
		}
		if load {
			if byteTransfer {
				var value [1]byte
				if readErr := b.copyOut(address, value[:], cpu.PermissionRead); readErr != nil {
					return nil, readErr
				}
				if rd == cpu.RegisterPC {
					b.branchExchange(uint32(value[0]))
				} else {
					b.regs[rd] = uint32(value[0])
				}
			} else {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return nil, readErr
				}
				if rd == cpu.RegisterPC {
					b.branchExchange(value)
				} else {
					b.regs[rd] = value
				}
			}
		} else if byteTransfer {
			value := b.regs[rd]
			if rd == cpu.RegisterPC {
				value = pc + 12
			}
			if writeErr := b.copyIn(address, []byte{byte(value)}, cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		} else {
			value := b.regs[rd]
			if rd == cpu.RegisterPC {
				value = pc + 12
			}
			if writeErr := b.write32(address, value, cpu.PermissionWrite); writeErr != nil {
				return nil, writeErr
			}
		}
		if (!preIndex || writeBack) && !(load && rd == rn) {
			b.regs[rn] = indexedAddress
		}
		return nil, nil

	case instruction&0x0e000000 == 0x08000000: // LDM/STM
		preIndex := instruction&(1<<24) != 0
		increment := instruction&(1<<23) != 0
		loadPSR := instruction&(1<<22) != 0
		writeBack := instruction&(1<<21) != 0
		load := instruction&(1<<20) != 0
		rn := uint32(instruction>>16) & 0xf
		registers := uint16(instruction)
		if loadPSR || registers == 0 || rn == cpu.RegisterPC {
			return nil, b.unsupportedARM(pc, instruction)
		}
		count := uint32(bits.OnesCount16(registers))
		base := b.readOperandRegister(rn, pc, cpu.ModeARM)
		var address uint32
		if increment {
			address = base
			if preIndex {
				address += 4
			}
		} else {
			address = base - count*4
			if !preIndex {
				address += 4
			}
		}
		var loadedPC *uint32
		for register := uint32(0); register < 16; register++ {
			if registers&(1<<register) == 0 {
				continue
			}
			if load {
				value, readErr := b.read32(address, cpu.PermissionRead)
				if readErr != nil {
					return nil, readErr
				}
				if register == cpu.RegisterPC {
					loadedPC = &value
				} else {
					b.regs[register] = value
				}
			} else {
				value := b.regs[register]
				if register == cpu.RegisterPC {
					value = pc + 12
				} else if register == rn {
					value = base
				}
				if writeErr := b.write32(address, value, cpu.PermissionWrite); writeErr != nil {
					return nil, writeErr
				}
			}
			address += 4
		}
		if writeBack && (!load || registers&(1<<rn) == 0) {
			if increment {
				b.regs[rn] = base + count*4
			} else {
				b.regs[rn] = base - count*4
			}
		}
		if loadedPC != nil {
			b.branchExchange(*loadedPC)
		}
		return nil, nil

	case instruction&0x0f000000 == 0x0f000000: // SWI
		reason := cpu.StopBreakpoint
		return &reason, nil

	default:
		return nil, b.unsupportedARM(pc, instruction)
	}
}

func (b *Backend) branchExchange(target uint32) {
	if target&1 != 0 {
		b.mode = cpu.ModeThumb
		b.regs[cpu.RegisterPC] = target &^ 1
	} else {
		b.mode = cpu.ModeARM
		b.regs[cpu.RegisterPC] = target &^ 3
	}
	b.setModeFlag()
}

func (b *Backend) readOperandRegister(id, instructionAddress uint32, mode cpu.Mode) uint32 {
	if id != cpu.RegisterPC {
		return b.regs[id]
	}
	if mode == cpu.ModeThumb {
		return instructionAddress + 4
	}
	return instructionAddress + 8
}

func (b *Backend) conditionPassed(condition uint8) bool {
	cpsr := b.regs[cpu.RegisterCPSR]
	n := cpsr&flagN != 0
	z := cpsr&flagZ != 0
	c := cpsr&flagC != 0
	v := cpsr&flagV != 0
	switch condition {
	case 0x0:
		return z
	case 0x1:
		return !z
	case 0x2:
		return c
	case 0x3:
		return !c
	case 0x4:
		return n
	case 0x5:
		return !n
	case 0x6:
		return v
	case 0x7:
		return !v
	case 0x8:
		return c && !z
	case 0x9:
		return !c || z
	case 0xa:
		return n == v
	case 0xb:
		return n != v
	case 0xc:
		return !z && n == v
	case 0xd:
		return z || n != v
	case 0xe:
		return true
	default:
		return false
	}
}

func (b *Backend) setModeFlag() {
	if b.mode == cpu.ModeThumb {
		b.regs[cpu.RegisterCPSR] |= flagT
	} else {
		b.regs[cpu.RegisterCPSR] &^= flagT
	}
}

func (b *Backend) setNZ(value uint32) {
	b.regs[cpu.RegisterCPSR] &^= flagN | flagZ
	if value == 0 {
		b.regs[cpu.RegisterCPSR] |= flagZ
	}
	if value&(uint32(1)<<31) != 0 {
		b.regs[cpu.RegisterCPSR] |= flagN
	}
}

func (b *Backend) setNZCV(value uint32, carry, overflow bool) {
	b.setNZ(value)
	b.regs[cpu.RegisterCPSR] &^= flagC | flagV
	if carry {
		b.regs[cpu.RegisterCPSR] |= flagC
	}
	if overflow {
		b.regs[cpu.RegisterCPSR] |= flagV
	}
}

func (b *Backend) setNZC(value uint32, carry bool) {
	b.setNZ(value)
	b.regs[cpu.RegisterCPSR] &^= flagC
	if carry {
		b.regs[cpu.RegisterCPSR] |= flagC
	}
}

func (b *Backend) carry() bool {
	return b.regs[cpu.RegisterCPSR]&flagC != 0
}

func shiftLSL(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	switch {
	case amount == 0:
		return value, oldCarry
	case amount < 32:
		return value << amount, value&(uint32(1)<<(32-amount)) != 0
	case amount == 32:
		return 0, value&1 != 0
	default:
		return 0, false
	}
}

func shiftLSR(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	switch {
	case amount == 0:
		return value, oldCarry
	case amount < 32:
		return value >> amount, value&(uint32(1)<<(amount-1)) != 0
	case amount == 32:
		return 0, value&flagN != 0
	default:
		return 0, false
	}
}

func shiftASR(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	switch {
	case amount == 0:
		return value, oldCarry
	case amount < 32:
		return uint32(int32(value) >> amount),
			value&(uint32(1)<<(amount-1)) != 0
	default:
		if value&flagN != 0 {
			return ^uint32(0), true
		}
		return 0, false
	}
}

func shiftROR(value uint32, amount uint8, oldCarry bool) (uint32, bool) {
	if amount == 0 {
		return value, oldCarry
	}
	rotation := int(amount & 31)
	if rotation == 0 {
		return value, value&flagN != 0
	}
	result := bits.RotateLeft32(value, -rotation)
	return result, result&flagN != 0
}

func addWithCarry(left, right, carry uint32) (uint32, bool, bool) {
	unsigned := uint64(left) + uint64(right) + uint64(carry)
	result := uint32(unsigned)
	leftSign := left >> 31
	rightSign := right >> 31
	resultSign := result >> 31
	overflow := leftSign == rightSign && leftSign != resultSign
	return result, unsigned>>32 != 0, overflow
}

func (b *Backend) unsupportedThumb(pc uint32, instruction uint16) error {
	return fmt.Errorf("%w: Thumb 0x%04x at 0x%08x",
		cpu.ErrUnsupportedInstruction, instruction, pc)
}

func (b *Backend) unsupportedARM(pc, instruction uint32) error {
	return fmt.Errorf("%w: ARM 0x%08x at 0x%08x",
		cpu.ErrUnsupportedInstruction, instruction, pc)
}

func (b *Backend) read16(address uint32, permission cpu.Permissions) (uint16, error) {
	var data [2]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data[:]), nil
}

func (b *Backend) read32(address uint32, permission cpu.Permissions) (uint32, error) {
	var data [4]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (b *Backend) write16(address uint32, value uint16, permission cpu.Permissions) error {
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) write32(address, value uint32, permission cpu.Permissions) error {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) copyOut(address uint32, destination []byte, permission cpu.Permissions) error {
	if len(destination) == 0 {
		return nil
	}
	current := address
	remaining := destination
	for len(remaining) > 0 {
		mapped, offset, err := b.findRegion(current, permission)
		if err != nil {
			return err
		}
		count := min(len(remaining), len(mapped.data)-offset)
		copy(remaining[:count], mapped.data[offset:offset+count])
		remaining = remaining[count:]
		if len(remaining) == 0 {
			break
		}
		if uint64(current)+uint64(count) > uint64(^uint32(0)) {
			return cpu.ErrInvalidAddress
		}
		current += uint32(count)
	}
	return nil
}

func (b *Backend) copyIn(address uint32, source []byte, permission cpu.Permissions) error {
	if len(source) == 0 {
		return nil
	}
	current := address
	remaining := source
	for len(remaining) > 0 {
		mapped, offset, err := b.findRegion(current, permission)
		if err != nil {
			return err
		}
		count := min(len(remaining), len(mapped.data)-offset)
		copy(mapped.data[offset:offset+count], remaining[:count])
		remaining = remaining[count:]
		if len(remaining) == 0 {
			break
		}
		if uint64(current)+uint64(count) > uint64(^uint32(0)) {
			return cpu.ErrInvalidAddress
		}
		current += uint32(count)
	}
	return nil
}

func (b *Backend) findRegion(address uint32, permission cpu.Permissions) (*region, int, error) {
	for index := range b.regions {
		mapped := &b.regions[index]
		if address < mapped.address || uint64(address) >= uint64(mapped.address)+uint64(mapped.size) {
			continue
		}
		if mapped.permissions&permission != permission {
			return nil, 0, fmt.Errorf("%w at 0x%08x", cpu.ErrPermissionDenied, address)
		}
		return mapped, int(address - mapped.address), nil
	}
	return nil, 0, fmt.Errorf("%w: 0x%08x", cpu.ErrInvalidAddress, address)
}

var _ cpu.Backend = (*Backend)(nil)
