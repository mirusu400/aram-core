// Package pxo executes the native PXO1 application ABI used by classic LG
// handsets. It owns the image mapping, host-call table and synchronous event
// entry boundary; device services are supplied by a Dispatcher.
package pxo

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
	pxoloader "github.com/mirusu400/aram-core/loader/pxo"
)

const (
	// PXO1 images use a zero-based virtual address space. In addition to the
	// loader fixup stream, the bundled ARM support library contains absolute
	// references into the trailing zero-fill area that are deliberately not
	// listed as fixups, so rebasing the image leaves those references stale.
	ImageBase        = uint32(0)
	HostBase         = uint32(0x02000000)
	HostRegionSize   = uint32(0x1000)
	ReturnTrap       = HostBase + 0x100
	StackBase        = uint32(0x70000000)
	StackSize        = uint32(1 << 20)
	HostSlots        = 12
	DefaultRunBudget = uint64(10_000_000)
)

var ErrRunBudget = errors.New("PXO1 event exceeded instruction budget")

// Call is one PXO1 host-service invocation. Slot is also the number of service
// arguments; Group and Ordinal select the handset API operation.
type Call struct {
	Slot    uint32
	Group   uint32
	Ordinal uint32
	Args    []uint32
}

// Dispatcher implements handset services. It may use Runtime memory helpers
// and may synchronously re-enter guest callbacks.
type Dispatcher interface {
	Dispatch(context.Context, *Runtime, Call) (uint32, error)
}

type DispatchFunc func(context.Context, *Runtime, Call) (uint32, error)

func (f DispatchFunc) Dispatch(ctx context.Context, runtime *Runtime, call Call) (uint32, error) {
	return f(ctx, runtime, call)
}

// Runtime is a loaded PXO1 image and its host boundary.
type Runtime struct {
	CPU        cpu.Backend
	Package    *pxoloader.Package
	Dispatcher Dispatcher
	Calls      []Call
}

// NewRuntime maps and relocates one validated package.
func NewRuntime(backend cpu.Backend, pkg *pxoloader.Package, dispatcher Dispatcher) (*Runtime, error) {
	if backend == nil || pkg == nil {
		return nil, fmt.Errorf("initialize PXO1 runtime: missing CPU or package")
	}
	if uint64(ImageBase)+uint64(pkg.MappedSize()) > uint64(HostBase) {
		return nil, fmt.Errorf("initialize PXO1 runtime: image exceeds reserved address range")
	}
	relocated, err := pkg.Relocate(ImageBase)
	if err != nil {
		return nil, err
	}
	if err := backend.Map(ImageBase, pkg.MappedSize(), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return nil, fmt.Errorf("map PXO1 image: %w", err)
	}
	if err := backend.WriteMemory(ImageBase, relocated); err != nil {
		return nil, fmt.Errorf("copy PXO1 image: %w", err)
	}
	if err := backend.Map(HostBase, HostRegionSize, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute); err != nil {
		return nil, fmt.Errorf("map PXO1 host boundary: %w", err)
	}
	if err := backend.Map(StackBase, StackSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		return nil, fmt.Errorf("map PXO1 stack: %w", err)
	}

	var table [HostSlots * 4]byte
	stubs := make([]byte, HostRegionSize)
	for slot := uint32(0); slot < HostSlots; slot++ {
		address := HostBase + slot*4
		// PXO1 veneers use `mov pc, rN`, not BX. They retain Thumb state and
		// expect an aligned code address in the handset table.
		binary.LittleEndian.PutUint32(table[slot*4:], address)
		binary.LittleEndian.PutUint16(stubs[slot*4:], 0xbe00)
	}
	binary.LittleEndian.PutUint16(stubs[ReturnTrap-HostBase:], 0xbe00)
	if err := backend.WriteMemory(HostBase, stubs); err != nil {
		return nil, fmt.Errorf("install PXO1 host stubs: %w", err)
	}
	if err := backend.WriteMemory(ImageBase+pkg.Descriptor.GOT, table[:]); err != nil {
		return nil, fmt.Errorf("install PXO1 host table: %w", err)
	}
	return &Runtime{CPU: backend, Package: pkg, Dispatcher: dispatcher}, nil
}

// CallEvent invokes the PXO1 application event entry in Thumb mode.
func (r *Runtime) CallEvent(ctx context.Context, event, wparam, lparam uint32, budget uint64) (uint32, error) {
	if r == nil || r.Package == nil {
		return 0, fmt.Errorf("call PXO1 event: runtime is unavailable")
	}
	return r.CallFunction(ctx, ImageBase+r.Package.Descriptor.Entry, []uint32{event, wparam, lparam}, budget)
}

// CallFunction invokes an in-image callback through the same ABI as the
// handset event dispatcher. It is intended for top-level event scheduling;
// callers must not use it recursively from Dispatcher.Dispatch.
func (r *Runtime) CallFunction(ctx context.Context, procedure uint32, args []uint32, budget uint64) (uint32, error) {
	if r == nil || r.CPU == nil || r.Package == nil {
		return 0, fmt.Errorf("call PXO1 function: runtime is unavailable")
	}
	if len(args) > cpu.MaxHostCallWords {
		return 0, fmt.Errorf("call PXO1 function: %d arguments exceed limit", len(args))
	}
	if budget == 0 {
		budget = DefaultRunBudget
	}
	for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
		if err := r.CPU.WriteRegister(register, 0); err != nil {
			return 0, fmt.Errorf("initialize PXO1 register r%d: %w", register, err)
		}
	}
	stack := StackBase + StackSize - 0x100 - uint32(max(0, len(args)-4))*4
	for index, value := range args {
		if index < 4 {
			if err := r.CPU.WriteRegister(uint32(index), value); err != nil {
				return 0, fmt.Errorf("initialize PXO1 argument %d: %w", index, err)
			}
			continue
		}
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], value)
		if err := r.CPU.WriteMemory(stack+uint32(index-4)*4, encoded[:]); err != nil {
			return 0, fmt.Errorf("initialize PXO1 stack argument %d: %w", index, err)
		}
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterSP: stack,
		cpu.RegisterLR: ReturnTrap | 1,
	} {
		if err := r.CPU.WriteRegister(register, value); err != nil {
			return 0, fmt.Errorf("initialize PXO1 register r%d: %w", register, err)
		}
	}

	pc := procedure &^ 1
	mode := cpu.ModeARM
	if procedure&1 != 0 {
		mode = cpu.ModeThumb
	}
	var executed uint64
	for executed < budget {
		result := r.CPU.Run(ctx, pc, mode, budget-executed)
		executed += result.Instructions
		if result.Err != nil {
			return 0, fmt.Errorf("execute PXO1 event at 0x%08x: %w", result.PC, result.Err)
		}
		switch result.Reason {
		case cpu.StopBreakpoint:
			if result.PC < 2 {
				return 0, fmt.Errorf("invalid PXO1 breakpoint PC 0x%08x", result.PC)
			}
			trap := result.PC - 2
			if trap == ReturnTrap {
				value, err := r.CPU.ReadRegister(cpu.RegisterR0)
				if err != nil {
					return 0, err
				}
				return value, nil
			}
			if trap < HostBase || trap >= HostBase+HostSlots*4 || (trap-HostBase)&3 != 0 {
				return 0, fmt.Errorf("unknown PXO1 breakpoint 0x%08x", trap)
			}
			slot := (trap - HostBase) / 4
			if err := r.dispatchHostCall(ctx, slot); err != nil {
				return 0, err
			}
			nextPC, nextMode, err := r.resumeAddress()
			if err != nil {
				return 0, err
			}
			pc, mode = nextPC, nextMode
		case cpu.StopBudget:
			return 0, fmt.Errorf("%w after %d instructions at 0x%08x", ErrRunBudget, executed, result.PC)
		case cpu.StopExited:
			value, err := r.CPU.ReadRegister(cpu.RegisterR0)
			return value, err
		default:
			return 0, fmt.Errorf("PXO1 event stopped with reason %d at 0x%08x", result.Reason, result.PC)
		}
	}
	return 0, fmt.Errorf("%w after %d instructions", ErrRunBudget, executed)
}

func (r *Runtime) dispatchHostCall(ctx context.Context, slot uint32) error {
	var frame cpu.HostCallFrame
	if err := cpu.CaptureHostCallFrame(r.CPU, &frame, cpu.HostCallFrameRequest{StackWords: HostSlots + 6}); err != nil {
		return fmt.Errorf("capture PXO1 host call %d: %w", slot, err)
	}
	values := make([]uint32, 0, HostSlots+2)
	values = append(values,
		frame.Registers[cpu.RegisterR0],
		frame.Registers[cpu.RegisterR1],
		frame.Registers[cpu.RegisterR2],
		frame.Registers[cpu.RegisterR3],
	)
	values = append(values, frame.Stack[4:]...)
	if int(slot)+2 > len(values) {
		return fmt.Errorf("PXO1 host call slot %d exceeds captured arguments", slot)
	}
	call := Call{
		Slot:    slot,
		Group:   values[slot],
		Ordinal: values[slot+1],
		Args:    append([]uint32(nil), values[:slot]...),
	}
	r.Calls = append(r.Calls, call)
	var value uint32
	if r.Dispatcher != nil {
		var err error
		value, err = r.Dispatcher.Dispatch(ctx, r, call)
		if err != nil {
			return fmt.Errorf("dispatch PXO1 service group 0x%x ordinal 0x%x: %w", call.Group, call.Ordinal, err)
		}
	}

	returnAddress := frame.Stack[3]
	cpsr := frame.Registers[cpu.RegisterCPSR]
	if returnAddress&1 != 0 {
		cpsr |= cpu.StatusThumb
	} else {
		cpsr &^= cpu.StatusThumb
	}
	var commit cpu.RegisterCommit
	_ = commit.Set(cpu.RegisterR0, value)
	_ = commit.Set(cpu.RegisterR4, frame.Stack[0])
	_ = commit.Set(cpu.RegisterR5, frame.Stack[1])
	_ = commit.Set(cpu.RegisterR6, frame.Stack[2])
	_ = commit.Set(cpu.RegisterSP, frame.Registers[cpu.RegisterSP]+16)
	_ = commit.Set(cpu.RegisterPC, returnAddress&^1)
	_ = commit.Set(cpu.RegisterCPSR, cpsr)
	if err := cpu.CommitHostCallRegisters(r.CPU, commit); err != nil {
		return fmt.Errorf("return from PXO1 host call: %w", err)
	}
	return nil
}

func (r *Runtime) resumeAddress() (uint32, cpu.Mode, error) {
	pc, err := r.CPU.ReadRegister(cpu.RegisterPC)
	if err != nil {
		return 0, 0, err
	}
	cpsr, err := r.CPU.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return 0, 0, err
	}
	mode := cpu.ModeARM
	if cpsr&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	return pc, mode, nil
}

// ReadMemory reads guest bytes for a service implementation.
func (r *Runtime) ReadMemory(address uint32, destination []byte) error {
	return r.CPU.ReadMemory(address, destination)
}

// WriteMemory writes guest bytes for a service implementation.
func (r *Runtime) WriteMemory(address uint32, source []byte) error {
	return r.CPU.WriteMemory(address, source)
}
