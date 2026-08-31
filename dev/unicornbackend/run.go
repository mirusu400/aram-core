package unicornbackend

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/mirusu400/aram-core/cpu"
)

func (b *Backend) Run(ctx context.Context, address uint32, mode cpu.Mode, budget uint64) cpu.Result {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: cpu.ErrClosed}
	}
	if !validBackendMode(mode) || mode == cpu.ModeARM && address&3 != 0 ||
		mode == cpu.ModeThumb && address&1 != 0 {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: cpu.ErrInvalidAddress}
	}
	if err := b.setRunPositionLocked(address, mode); err != nil {
		return cpu.Result{Reason: cpu.StopFault, PC: address, Err: err}
	}
	b.stopped.Store(false)
	b.running.Store(true)
	defer b.running.Store(false)

	var executed uint64
	for budget == 0 || executed < budget {
		if result := b.hostStopResult(ctx, executed); result != nil {
			return *result
		}
		pc, err := b.readRegisterLocked(cpu.RegisterPC)
		if err != nil {
			return cpu.Result{Reason: cpu.StopFault, Instructions: executed, PC: address, Err: err}
		}
		currentMode, status, err := b.currentModeLocked()
		if err != nil {
			return cpu.Result{Reason: cpu.StopFault, Instructions: executed, PC: pc, Err: err}
		}
		breakpoint, width, err := b.hostBoundaryAtLocked(pc, currentMode, status)
		if err != nil {
			return cpu.Result{Reason: cpu.StopFault, Instructions: executed, PC: pc, Err: err}
		}
		if breakpoint {
			next := pc + width
			if err := b.writeRegisterLocked(cpu.RegisterPC, next); err != nil {
				return cpu.Result{Reason: cpu.StopFault, Instructions: executed, PC: pc, Err: err}
			}
			executed++
			return cpu.Result{
				Reason: cpu.StopBreakpoint, Instructions: executed, PC: next,
			}
		}

		begin := uint64(pc)
		if currentMode == cpu.ModeThumb {
			begin |= 1
		}
		if code := b.api.start(b.engine, begin, 0, 0, 1); code != ucErrOK {
			faultPC := pc
			if observed, readErr := b.readRegisterLocked(cpu.RegisterPC); readErr == nil {
				faultPC = observed
			}
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: executed,
				PC:           faultPC,
				Err:          b.executionCallError(code),
			}
		}
		executed++
	}
	if result := b.hostStopResult(ctx, executed); result != nil {
		return *result
	}
	pc, err := b.readRegisterLocked(cpu.RegisterPC)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Instructions: executed, Err: err}
	}
	return cpu.Result{Reason: cpu.StopBudget, Instructions: executed, PC: pc}
}

func (b *Backend) setRunPositionLocked(address uint32, mode cpu.Mode) error {
	status, err := b.readRegisterLocked(cpu.RegisterCPSR)
	if err != nil {
		return err
	}
	if mode == cpu.ModeThumb {
		status |= cpu.StatusThumb
	} else {
		status &^= cpu.StatusThumb
	}
	if err := b.writeRegisterLocked(cpu.RegisterCPSR, status); err != nil {
		return err
	}
	return b.writeRegisterLocked(cpu.RegisterPC, address)
}

func (b *Backend) currentModeLocked() (cpu.Mode, uint32, error) {
	status, err := b.readRegisterLocked(cpu.RegisterCPSR)
	if err != nil {
		return cpu.ModeARM, 0, err
	}
	mode := cpu.ModeARM
	if status&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	return mode, status, nil
}

func (b *Backend) hostBoundaryAtLocked(
	pc uint32,
	mode cpu.Mode,
	status uint32,
) (bool, uint32, error) {
	width := uint32(4)
	if mode == cpu.ModeThumb {
		width = 2
	}
	if err := b.checkRange(pc, int(width), cpu.PermissionExecute); err != nil {
		return false, width, err
	}
	var encoded [4]byte
	code := b.api.readMemory(
		b.engine, uint64(pc), unsafe.Pointer(&encoded[0]), uint64(width),
	)
	runtime.KeepAlive(&encoded)
	if code != ucErrOK {
		return false, width, b.memoryCallError("fetch host boundary", code)
	}
	if mode == cpu.ModeThumb {
		instruction := binary.LittleEndian.Uint16(encoded[:2])
		return instruction&0xff00 == 0xbe00 || instruction&0xff00 == 0xdf00,
			width, nil
	}
	instruction := binary.LittleEndian.Uint32(encoded[:])
	condition := uint8(instruction >> 28)
	if !armConditionPassed(condition, status) {
		return false, width, nil
	}
	breakpoint := instruction&0x0ff000f0 == 0x01200070
	softwareInterrupt := instruction&0x0f000000 == 0x0f000000
	return breakpoint || softwareInterrupt, width, nil
}

func armConditionPassed(condition uint8, status uint32) bool {
	n := status&statusN != 0
	z := status&statusZ != 0
	c := status&statusC != 0
	v := status&statusV != 0
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

func (b *Backend) hostStopResult(ctx context.Context, executed uint64) *cpu.Result {
	pc := uint32(0)
	if observed, err := b.readRegisterLocked(cpu.RegisterPC); err == nil {
		pc = observed
	}
	if b.stopped.Load() {
		return &cpu.Result{
			Reason: cpu.StopRequested, Instructions: executed, PC: pc, Err: cpu.ErrStopped,
		}
	}
	if err := ctx.Err(); err != nil {
		return &cpu.Result{
			Reason: cpu.StopRequested, Instructions: executed, PC: pc, Err: err,
		}
	}
	return nil
}

func (b *Backend) executionCallError(code int32) error {
	callErr := b.api.callError("execute instruction", code)
	switch code {
	case ucErrInstructionInvalid, ucErrException:
		return fmt.Errorf("%w: %v", cpu.ErrUnsupportedInstruction, callErr)
	case ucErrReadProtection, ucErrWriteProtection, ucErrFetchProtection:
		return fmt.Errorf("%w: %v", cpu.ErrPermissionDenied, callErr)
	case ucErrReadUnmapped, ucErrWriteUnmapped, ucErrFetchUnmapped,
		ucErrReadUnaligned, ucErrWriteUnaligned, ucErrFetchUnaligned:
		return fmt.Errorf("%w: %v", cpu.ErrInvalidAddress, callErr)
	default:
		return callErr
	}
}
