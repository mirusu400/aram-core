package system

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/mirusu400/aram-core/cpu"
)

const DefaultClockedRunnerQuantum = uint64(4096)

// ExecutionRunner is the common execution surface implemented by CPU
// backends and wrappers such as HLERunner.
type ExecutionRunner interface {
	Run(context.Context, uint32, cpu.Mode, uint64) cpu.Result
}

// ClockedDevice advances deterministically from retired guest instructions. A
// platform device may apply its own rational virtual-time conversion.
type ClockedDevice interface {
	Advance(retiredInstructions uint64) error
}

// ClockedRunner slices CPU execution into bounded quanta and advances all
// scheduled devices after each slice. Interrupt outputs raised by a device are
// therefore visible at the first instruction boundary of the following slice.
type ClockedRunner struct {
	cpu     cpu.Backend
	runner  ExecutionRunner
	quantum uint64
	devices []ClockedDevice
	halt    *CPUHaltLine
	halted  bool
}

// CPUHaltLine models a processor-clock gate: firmware stops its own clock
// through a control register and the clock restarts when an interrupt line
// is asserted. It sits between the interrupt controllers and the CPU so it
// can see the lines, and the register's device reports the request to it.
type CPUHaltLine struct {
	sink InterruptLineSink
	// stop ends the CPU's current slice, so the clock stops at the store
	// that requested it rather than wherever the slice would have ended.
	stop      func() error
	lines     atomic.Uint32
	requested atomic.Bool
}

func NewCPUHaltLine(sink InterruptLineSink, stop func() error) *CPUHaltLine {
	return &CPUHaltLine{sink: sink, stop: stop}
}

// SetInterruptLine implements InterruptLineSink.
func (h *CPUHaltLine) SetInterruptLine(line cpu.InterruptLine, asserted bool) error {
	if line.Valid() {
		mask := uint32(1) << uint32(line)
		if asserted {
			h.lines.Or(mask)
		} else {
			h.lines.And(^mask)
		}
	}
	return h.sink.SetInterruptLine(line, asserted)
}

// Request records that the guest stopped the processor clock.
func (h *CPUHaltLine) Request() {
	h.requested.Store(true)
	if h.stop != nil {
		_ = h.stop()
	}
}

// SetCPUHalt connects the board's processor-clock gate. While the clock is
// stopped the runner executes nothing: guest time still passes for every
// clocked device, and execution resumes once an interrupt line is asserted.
func (r *ClockedRunner) SetCPUHalt(halt *CPUHaltLine) {
	r.halt = halt
	r.halted = false
}

// RestartCPUClock abandons a stop in progress. A machine calls it after a
// reset or a restored snapshot, which begin with the clock running.
func (r *ClockedRunner) RestartCPUClock() {
	r.halted = false
	if r.halt != nil {
		r.halt.requested.Store(false)
	}
}

func NewClockedRunner(
	backend cpu.Backend,
	runner ExecutionRunner,
	quantum uint64,
	devices ...ClockedDevice,
) (*ClockedRunner, error) {
	if backend == nil || runner == nil || quantum == 0 {
		return nil, fmt.Errorf("clocked runner requires CPU, runner, and nonzero quantum")
	}
	configured := append([]ClockedDevice(nil), devices...)
	for index, device := range configured {
		if device == nil {
			return nil, fmt.Errorf("clocked runner device %d is nil", index)
		}
	}
	return &ClockedRunner{
		cpu: backend, runner: runner, quantum: quantum, devices: configured,
	}, nil
}

func (r *ClockedRunner) Run(
	ctx context.Context,
	address uint32,
	mode cpu.Mode,
	budget uint64,
) cpu.Result {
	var total uint64
	for budget == 0 || total < budget {
		slice := r.quantum
		if budget != 0 && budget-total < slice {
			slice = budget - total
		}
		if r.halt != nil && r.halted {
			if r.halt.lines.Load() != 0 {
				r.halted = false
				r.halt.requested.Store(false)
			} else if ctx.Err() == nil {
				total += slice
				for index, device := range r.devices {
					if err := device.Advance(slice); err != nil {
						return cpu.Result{
							Reason:       cpu.StopFault,
							Instructions: total,
							PC:           address,
							Err:          fmt.Errorf("advance clocked device %d: %w", index, err),
						}
					}
				}
				continue
			}
		}
		result := r.runner.Run(ctx, address, mode, slice)
		gated := false
		if r.halt != nil && r.halt.requested.Swap(false) {
			if r.halt.lines.Load() == 0 {
				r.halted = true
			}
			// The gate ended the slice early; that is not a stop the caller
			// asked for.
			if result.Reason == cpu.StopRequested && ctx.Err() == nil {
				gated = true
				result.Reason, result.Err = cpu.StopBudget, nil
			}
		}
		retired := result.Instructions
		total += retired
		result.Instructions = total
		for index, device := range r.devices {
			if err := device.Advance(retired); err != nil {
				return cpu.Result{
					Reason:       cpu.StopFault,
					Instructions: total,
					PC:           result.PC,
					Err:          fmt.Errorf("advance clocked device %d: %w", index, err),
				}
			}
		}
		if result.Err != nil || result.Reason != cpu.StopBudget || total == budget {
			return result
		}
		if retired != slice && !gated {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: total,
				PC:           result.PC,
				Err: fmt.Errorf(
					"execution runner retired %d instructions from a %d-instruction budget",
					retired, slice,
				),
			}
		}
		status, err := r.cpu.ReadRegister(cpu.RegisterCPSR)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: total,
				PC:           result.PC,
				Err:          fmt.Errorf("read CPU mode after clocked slice: %w", err),
			}
		}
		address = result.PC
		mode = cpu.ModeARM
		if status&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
	}
	return cpu.Result{Reason: cpu.StopBudget, Instructions: total, PC: address}
}

func (r *ClockedRunner) ActiveHLEInvocation() (HLEInvocation, bool) {
	runner, ok := r.runner.(interface {
		ActiveInvocation() (HLEInvocation, bool)
	})
	if !ok {
		return HLEInvocation{}, false
	}
	return runner.ActiveInvocation()
}

var _ ExecutionRunner = (*ClockedRunner)(nil)
