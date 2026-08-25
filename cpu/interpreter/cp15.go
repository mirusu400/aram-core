package interpreter

import (
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

type cp15State struct {
	control                uint32
	translationTableBase   uint32
	domainAccessControl    uint32
	dataFaultStatus        uint32
	instructionFaultStatus uint32
	faultAddress           uint32
	processID              uint32
}

// CP15ControlAccess is one guest read or write of the ARM system control
// register (CP15 c1,c0,0). It is optional host diagnostics and is not part of
// architectural save state.
type CP15ControlAccess struct {
	InstructionAddress uint32
	Value              uint32
	Write              bool
}

// InstructionCachePrefetchAccess records one completed ARM926 CP15
// prefetch-I-cache-line operation. It is optional host diagnostics and is not
// part of architectural save state.
type InstructionCachePrefetchAccess struct {
	InstructionAddress     uint32
	ModifiedVirtualAddress uint32
}

// SetCP15ControlHistoryLimit configures a bounded diagnostic ring for system
// control-register accesses. A zero limit disables and clears the history.
func (b *Backend) SetCP15ControlHistoryLimit(limit uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if limit > 1<<20 {
		return fmt.Errorf("CP15 control history limit %d exceeds diagnostic maximum", limit)
	}
	b.cp15ControlHistory = make([]CP15ControlAccess, limit)
	b.cp15ControlHistoryNext = 0
	return nil
}

// CP15ControlHistory returns configured control-register accesses in guest
// execution order.
func (b *Backend) CP15ControlHistory() []CP15ControlAccess {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := min(b.cp15ControlHistoryNext, uint64(len(b.cp15ControlHistory)))
	history := make([]CP15ControlAccess, int(count))
	if count == 0 {
		return history
	}
	start := b.cp15ControlHistoryNext - count
	for index := range history {
		history[index] = b.cp15ControlHistory[(start+uint64(index))%uint64(len(b.cp15ControlHistory))]
	}
	return history
}

func (b *Backend) recordCP15ControlAccess(pc, value uint32, write bool) {
	if len(b.cp15ControlHistory) == 0 {
		return
	}
	b.cp15ControlHistory[b.cp15ControlHistoryNext%uint64(len(b.cp15ControlHistory))] =
		CP15ControlAccess{InstructionAddress: pc, Value: value, Write: write}
	b.cp15ControlHistoryNext++
}

// SetInstructionCachePrefetchHistoryLimit configures a bounded diagnostic
// ring for CP15 c7,c13,1 linefills. A zero limit disables and clears it.
func (b *Backend) SetInstructionCachePrefetchHistoryLimit(limit uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.ErrClosed
	}
	if limit > 1<<20 {
		return fmt.Errorf("instruction-cache prefetch history limit %d exceeds diagnostic maximum", limit)
	}
	b.instructionPrefetchHistory = make([]InstructionCachePrefetchAccess, limit)
	b.instructionPrefetchHistoryNext = 0
	return nil
}

// InstructionCachePrefetchHistory returns the configured diagnostic ring in
// guest execution order.
func (b *Backend) InstructionCachePrefetchHistory() []InstructionCachePrefetchAccess {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := min(b.instructionPrefetchHistoryNext, uint64(len(b.instructionPrefetchHistory)))
	history := make([]InstructionCachePrefetchAccess, int(count))
	if count == 0 {
		return history
	}
	start := b.instructionPrefetchHistoryNext - count
	for index := range history {
		history[index] = b.instructionPrefetchHistory[(start+uint64(index))%uint64(
			len(b.instructionPrefetchHistory),
		)]
	}
	return history
}

func (b *Backend) recordInstructionCachePrefetch(pc, address uint32) {
	if len(b.instructionPrefetchHistory) == 0 {
		return
	}
	b.instructionPrefetchHistory[b.instructionPrefetchHistoryNext%uint64(
		len(b.instructionPrefetchHistory),
	)] = InstructionCachePrefetchAccess{
		InstructionAddress:     pc,
		ModifiedVirtualAddress: b.modifiedVirtualAddress(address),
	}
	b.instructionPrefetchHistoryNext++
}

func (b *Backend) executeCP15(pc, instruction uint32) error {
	op1 := uint8(instruction>>21) & 7
	read := instruction&(1<<20) != 0
	crn := uint8(instruction >> 16 & 0xf)
	rd := uint32(instruction >> 12 & 0xf)
	coprocessor := uint8(instruction >> 8 & 0xf)
	op2 := uint8(instruction>>5) & 7
	crm := uint8(instruction & 0xf)
	if coprocessor != 15 || op1 != 0 {
		return b.unsupportedARM(pc, instruction)
	}
	if read {
		value, err := b.readCP15(crn, crm, op2)
		if err != nil {
			return fmt.Errorf("ARM MRC p15 at 0x%08x: %w", pc, err)
		}
		if rd == cpu.RegisterPC {
			b.resolveFlags()
			b.regs[cpu.RegisterCPSR] = b.regs[cpu.RegisterCPSR]&^0xf0000000 | value&0xf0000000
		} else {
			b.regs[rd] = value
		}
		if crn == 1 && crm == 0 && op2 == 0 {
			b.recordCP15ControlAccess(pc, value, false)
		}
		return nil
	}
	if rd == cpu.RegisterPC {
		return b.unsupportedARM(pc, instruction)
	}
	value := b.regs[rd]
	if err := b.writeCP15(crn, crm, op2, value); err != nil {
		return fmt.Errorf("ARM MCR p15 at 0x%08x: %w", pc, err)
	}
	if crn == 7 && crm == 13 && op2 == 1 {
		b.recordInstructionCachePrefetch(pc, value)
	}
	if crn == 1 && crm == 0 && op2 == 0 {
		b.recordCP15ControlAccess(pc, value, true)
	}
	return nil
}

func (b *Backend) readCP15(crn, crm, op2 uint8) (uint32, error) {
	switch {
	case crn == 1 && crm == 0 && op2 == 0:
		return b.cp15.control, nil
	case crn == 2 && crm == 0 && op2 == 0:
		return b.cp15.translationTableBase, nil
	case crn == 3 && crm == 0 && op2 == 0:
		return b.cp15.domainAccessControl, nil
	case crn == 5 && crm == 0 && op2 == 0:
		return b.cp15.dataFaultStatus, nil
	case crn == 5 && crm == 0 && op2 == 1:
		return b.cp15.instructionFaultStatus, nil
	case crn == 6 && crm == 0 && op2 == 0:
		return b.cp15.faultAddress, nil
	case crn == 13 && crm == 0 && op2 == 0:
		return b.cp15.processID, nil
	default:
		return 0, fmt.Errorf("unsupported CP15 read c%d,c%d,%d", crn, crm, op2)
	}
}

func (b *Backend) writeCP15(crn, crm, op2 uint8, value uint32) error {
	switch {
	case crn == 1 && crm == 0 && op2 == 0:
		if value != b.cp15.control {
			b.invalidateTLB()
			b.executeData = nil
			b.dataData = nil
		}
		b.cp15.control = value
		return nil
	case crn == 2 && crm == 0 && op2 == 0:
		b.cp15.translationTableBase = value
		b.invalidateTLB()
		return nil
	case crn == 3 && crm == 0 && op2 == 0:
		b.cp15.domainAccessControl = value
		return nil
	case crn == 5 && crm == 0 && op2 == 0:
		b.cp15.dataFaultStatus = value
		return nil
	case crn == 5 && crm == 0 && op2 == 1:
		b.cp15.instructionFaultStatus = value
		return nil
	case crn == 6 && crm == 0 && op2 == 0:
		b.cp15.faultAddress = value
		return nil
	case crn == 7 && crm == 5 && (op2 == 0 || op2 == 1 || op2 == 2):
		// Invalidate all or one instruction-cache entry by MVA or set/way.
		// Replacement timing is intentionally not modeled, so a set/way
		// operation conservatively drops the functional cache shadow.
		if op2 == 1 {
			b.invalidateInstructionCacheMVA(value)
		} else {
			b.invalidateInstructionCache()
		}
		b.executeData = nil
		return nil
	case crn == 7 && crm == 13 && op2 == 1:
		// ARM926 CP15 prefetch performs an I-cache lookup and fills the line
		// on a cacheable miss. This is architecturally observable when guest
		// code deliberately preserves instructions before reusing their RAM.
		return b.prefetchInstructionCacheLine(value)
	case crn == 7 && crm == 6 && (op2 == 0 || op2 == 1 || op2 == 2):
		// Invalidate all or one data-cache entry by MVA or set/way. Guest
		// memory is coherent; clear only the host data-region lookup.
		b.dataData = nil
		return nil
	case crn == 7 && crm == 10 && (op2 == 1 || op2 == 2 || op2 == 4):
		// Clean one D-cache entry or drain the write buffer. Interpreter
		// writes reach guest memory synchronously, so completion is immediate.
		return nil
	case crn == 7 && crm == 14 && (op2 == 1 || op2 == 2):
		// Clean and invalidate one D-cache entry. Writes are already visible;
		// invalidate the host lookup just as for the invalidate-only forms.
		b.dataData = nil
		return nil
	case crn == 7 && crm == 7 && op2 == 0:
		// Invalidate unified instruction and data caches. Guest memory is
		// coherent in the interpreter; clear both host lookup accelerators.
		b.executeData = nil
		b.dataData = nil
		b.invalidateInstructionCache()
		return nil
	case crn == 8 && crm == 7 && op2 == 0:
		// Invalidate the unified software TLB. Translation-table contents are
		// otherwise permitted to remain stale exactly as cached hardware is.
		b.invalidateTLB()
		return nil
	case crn == 13 && crm == 0 && op2 == 0:
		b.cp15.processID = value
		b.invalidateTLB()
		return nil
	default:
		return fmt.Errorf("unsupported CP15 write c%d,c%d,%d", crn, crm, op2)
	}
}
