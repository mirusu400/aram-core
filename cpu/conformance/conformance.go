// Package conformance provides a backend-agnostic differential test harness for
// cpu.Backend implementations. The portable interpreter is ARAM's accuracy
// oracle; any second backend (a recompiler, a native/JIT core behind build
// tags) must reproduce the interpreter's architectural state exactly. This
// package runs the same program and initial state through two backends and
// reports the first observable divergence — the dynarmic/Unicorn "diff against
// the reference" model adapted to Go and the cpu.Backend interface.
package conformance

import (
	"context"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	// CodeBase is where a Program's code is mapped (RWX), StackTop the initial
	// SP, and DataBase a scratch RW region programs may load/store through.
	CodeBase  uint32 = 0x1000
	CodeSize  uint32 = 0x1000
	DataBase  uint32 = 0x2000
	DataSize  uint32 = 0x1000
	StackBase uint32 = 0x3000
	StackSize uint32 = 0x1000
	StackTop  uint32 = StackBase + StackSize
)

// Program is one differential test case: a small instruction sequence that must
// end in a BKPT (so Run stops at StopBreakpoint), plus the initial register and
// scratch-memory state to run it from.
type Program struct {
	Name     string
	Mode     cpu.Mode
	Code     []byte
	Regs     map[uint32]uint32 // initial register overrides (id -> value)
	Data     map[uint32][]byte // initial writes into the scratch region
	Budget   uint64            // instruction budget (0 -> a default cap)
	StartPC  uint32            // entry; 0 -> CodeBase
	CaptureN uint32            // scratch bytes to capture from DataBase (0 -> 64)
}

// Snapshot is the observable architectural state after a Program runs: the 17
// registers (R0..PC, CPSR), a window of scratch memory, and the Run result.
type Snapshot struct {
	Regs   [17]uint32
	Data   []byte
	Reason cpu.StopReason
	Retire uint64
	PC     uint32
	Err    string
}

// Execute maps a fresh backend from newBackend, loads the program, runs it, and
// captures the resulting Snapshot. It is deterministic: the same (newBackend,
// program) always yields the same Snapshot on a correct backend.
func Execute(newBackend func() cpu.Backend, p Program) (Snapshot, error) {
	backend := newBackend()
	defer backend.Close()

	if err := backend.Map(CodeBase, CodeSize, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return Snapshot{}, fmt.Errorf("map code: %w", err)
	}
	if err := backend.Map(DataBase, DataSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return Snapshot{}, fmt.Errorf("map data: %w", err)
	}
	if err := backend.Map(StackBase, StackSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return Snapshot{}, fmt.Errorf("map stack: %w", err)
	}
	if err := backend.WriteMemory(CodeBase, p.Code); err != nil {
		return Snapshot{}, fmt.Errorf("write code: %w", err)
	}
	for addr, bytes := range p.Data {
		if err := backend.WriteMemory(addr, bytes); err != nil {
			return Snapshot{}, fmt.Errorf("write data 0x%08x: %w", addr, err)
		}
	}
	if err := backend.WriteRegister(cpu.RegisterSP, StackTop); err != nil {
		return Snapshot{}, fmt.Errorf("set sp: %w", err)
	}
	for id, value := range p.Regs {
		if err := backend.WriteRegister(id, value); err != nil {
			return Snapshot{}, fmt.Errorf("set r%d: %w", id, err)
		}
	}

	start := p.StartPC
	if start == 0 {
		start = CodeBase
	}
	budget := p.Budget
	if budget == 0 {
		budget = 1024
	}
	result := backend.Run(context.Background(), start, p.Mode, budget)

	var snap Snapshot
	for id := uint32(0); id < 17; id++ {
		value, err := backend.ReadRegister(id)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read r%d: %w", id, err)
		}
		snap.Regs[id] = value
	}
	capture := p.CaptureN
	if capture == 0 {
		capture = 64
	}
	snap.Data = make([]byte, capture)
	if err := backend.ReadMemory(DataBase, snap.Data); err != nil {
		return Snapshot{}, fmt.Errorf("read data: %w", err)
	}
	snap.Reason = result.Reason
	snap.Retire = result.Instructions
	snap.PC = result.PC
	if result.Err != nil {
		snap.Err = result.Err.Error()
	}
	return snap, nil
}

var regNames = [17]string{
	"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "r11", "r12", "sp", "lr", "pc", "cpsr",
}

// Diff returns a human-readable description of the first field where two
// snapshots differ, or "" when they are identical. Backends that agree here are
// architecturally indistinguishable through the cpu.Backend interface.
func Diff(oracle, subject Snapshot) string {
	if oracle.Reason != subject.Reason {
		return fmt.Sprintf("reason: oracle=%v subject=%v", oracle.Reason, subject.Reason)
	}
	if oracle.Retire != subject.Retire {
		return fmt.Sprintf("retired: oracle=%d subject=%d", oracle.Retire, subject.Retire)
	}
	if oracle.PC != subject.PC {
		return fmt.Sprintf("result-pc: oracle=0x%08x subject=0x%08x", oracle.PC, subject.PC)
	}
	if oracle.Err != subject.Err {
		return fmt.Sprintf("err: oracle=%q subject=%q", oracle.Err, subject.Err)
	}
	for id := 0; id < 17; id++ {
		if oracle.Regs[id] != subject.Regs[id] {
			return fmt.Sprintf("%s: oracle=0x%08x subject=0x%08x",
				regNames[id], oracle.Regs[id], subject.Regs[id])
		}
	}
	for i := range oracle.Data {
		if i >= len(subject.Data) || oracle.Data[i] != subject.Data[i] {
			return fmt.Sprintf("data[0x%08x]: oracle=0x%02x subject=0x%02x",
				DataBase+uint32(i), oracle.Data[i], subject.Data[i])
		}
	}
	return ""
}
