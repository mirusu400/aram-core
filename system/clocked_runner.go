package system

import (
	"context"
	"fmt"

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
		result := r.runner.Run(ctx, address, mode, slice)
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
		if retired != slice {
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

var _ ExecutionRunner = (*ClockedRunner)(nil)
