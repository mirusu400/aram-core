package system

import (
	"context"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const MaxHLEInvocationsPerRun = 4096

type HLECallContext struct {
	Call HLECallProfile
	CPU  cpu.Backend
	Bus  *Bus
}

type HLECallHandler interface {
	InvokeHLE(HLECallContext) error
}

type StatefulHLECallHandler interface {
	HLECallHandler
	Reset() error
	SaveState() ([]byte, error)
	LoadState([]byte) error
}

type HLECallHandlerFunc func(HLECallContext) error

func (f HLECallHandlerFunc) InvokeHLE(call HLECallContext) error {
	return f(call)
}

type HLEInvocation struct {
	ID            string
	Contract      string
	Address       uint32
	ReturnAddress uint32
}

// HLERunner owns profile-declared calls into unavailable code. It relies on a
// backend execution trap, applies only the named ABI handler, and resumes via
// the architectural link register without modifying guest instructions.
type HLERunner struct {
	bus         *Bus
	backend     cpu.ExecutionTrapBackend
	gates       map[cpu.ExecutionTrap]HLECallProfile
	handlers    map[string]HLECallHandler
	invocations []HLEInvocation
}

func NewHLERunner(
	bus *Bus,
	backend cpu.Backend,
	calls []HLECallProfile,
	handlers map[string]HLECallHandler,
) (*HLERunner, error) {
	trapBackend, ok := backend.(cpu.ExecutionTrapBackend)
	if bus == nil || backend == nil || !ok {
		return nil, fmt.Errorf("HLE runner requires a bus and execution-trap backend")
	}
	gates := make(map[cpu.ExecutionTrap]HLECallProfile, len(calls))
	traps := make([]cpu.ExecutionTrap, 0, len(calls))
	for _, call := range calls {
		if err := call.validate(); err != nil {
			return nil, err
		}
		trap := cpu.ExecutionTrap{Address: call.Address, Mode: call.Mode}
		if _, duplicate := gates[trap]; duplicate {
			return nil, fmt.Errorf("duplicate HLE call address 0x%08x", call.Address)
		}
		handler, exists := handlers[call.Contract]
		if !exists || handler == nil {
			return nil, fmt.Errorf("HLE call %q has no handler for %q", call.ID, call.Contract)
		}
		gates[trap] = call
		traps = append(traps, trap)
	}
	if err := trapBackend.SetExecutionTraps(traps); err != nil {
		return nil, fmt.Errorf("configure HLE execution traps: %w", err)
	}
	return &HLERunner{
		bus:      bus,
		backend:  trapBackend,
		gates:    gates,
		handlers: handlers,
	}, nil
}

func (r *HLERunner) Run(
	ctx context.Context,
	address uint32,
	mode cpu.Mode,
	budget uint64,
) cpu.Result {
	var instructions uint64
	var calls uint64
	for budget == 0 || instructions < budget {
		remaining := uint64(0)
		if budget != 0 {
			remaining = budget - instructions
		}
		result := r.backend.Run(ctx, address, mode, remaining)
		instructions += result.Instructions
		result.Instructions = instructions
		if result.Reason != cpu.StopExecutionTrap || result.Err != nil {
			return result
		}
		calls++
		if calls > MaxHLEInvocationsPerRun {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           result.PC,
				Err:          fmt.Errorf("HLE invocation limit exceeded"),
			}
		}
		status, statusErr := r.backend.ReadRegister(cpu.RegisterCPSR)
		if statusErr != nil {
			return r.fault(instructions, result.PC, statusErr)
		}
		trapMode := cpu.ModeARM
		if status&cpu.StatusThumb != 0 {
			trapMode = cpu.ModeThumb
		}
		call, exists := r.gates[cpu.ExecutionTrap{Address: result.PC, Mode: trapMode}]
		if !exists {
			// Debuggers and milestone tests may merge their own traps with the HLE
			// gates. Preserve an unowned stop for that caller instead of turning it
			// into a machine fault.
			return result
		}
		if err := r.handlers[call.Contract].InvokeHLE(HLECallContext{
			Call: call,
			CPU:  r.backend,
			Bus:  r.bus,
		}); err != nil {
			return r.fault(instructions, result.PC, fmt.Errorf("HLE call %q: %w", call.ID, err))
		}
		link, linkErr := r.backend.ReadRegister(cpu.RegisterLR)
		if linkErr != nil {
			return r.fault(instructions, result.PC, linkErr)
		}
		returnMode := cpu.ModeARM
		returnAddress := link &^ uint32(3)
		if link&1 != 0 {
			returnMode = cpu.ModeThumb
			returnAddress = link &^ uint32(1)
		}
		if err := r.backend.WriteRegister(cpu.RegisterPC, returnAddress); err != nil {
			return r.fault(instructions, result.PC, err)
		}
		if returnMode == cpu.ModeThumb {
			status |= cpu.StatusThumb
		} else {
			status &^= cpu.StatusThumb
		}
		if err := r.backend.WriteRegister(cpu.RegisterCPSR, status); err != nil {
			return r.fault(instructions, result.PC, err)
		}
		r.invocations = append(r.invocations, HLEInvocation{
			ID:            call.ID,
			Contract:      call.Contract,
			Address:       call.Address,
			ReturnAddress: returnAddress,
		})
		address = returnAddress
		mode = returnMode
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructions,
		PC:           address,
	}
}

func (r *HLERunner) Invocations() []HLEInvocation {
	return append([]HLEInvocation(nil), r.invocations...)
}

func (r *HLERunner) fault(instructions uint64, pc uint32, err error) cpu.Result {
	return cpu.Result{
		Reason:       cpu.StopFault,
		Instructions: instructions,
		PC:           pc,
		Err:          err,
	}
}
