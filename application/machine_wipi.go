// Package application implements ARAM's WIPI native-application machine.
package application

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (m *Machine) runSlice(ctx context.Context, frame bool) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("execute from %s: %w", state, ErrInvalidState)
	}
	pc, err := m.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	cpsr, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		m.state = machinecore.StateFaulted
		m.mu.Unlock()
		return err
	}
	mode := cpu.ModeARM
	if cpsr&cpu.StatusThumb != 0 {
		mode = cpu.ModeThumb
	}
	budget := m.runBudget
	if frame {
		budget = m.frameRunBudget
	}
	m.state = machinecore.StateRunning
	if m.wipi != nil {
		if err := m.wipi.BeginServiceExecution(); err != nil {
			m.state = machinecore.StateFaulted
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()

	result := m.runWIPISlice(ctx, pc, mode, budget, true)

	m.mu.Lock()
	defer m.mu.Unlock()
	requestedState := m.state
	if result.Err == nil {
		switch {
		case result.PC == 0:
			result.Reason = cpu.StopExited
		case result.Reason == cpu.StopBreakpoint &&
			result.PC >= 2 && result.PC-2 == guest.ReturnSentinel:
			result.Reason = cpu.StopExited
			result.PC = 0
		}
	}
	m.lastResult = result
	switch result.Reason {
	case cpu.StopBudget, cpu.StopBreakpoint:
		m.state = machinecore.StatePaused
	case cpu.StopExited:
		m.state = machinecore.StateStopped
	case cpu.StopRequested:
		switch {
		case m.closed || requestedState == machinecore.StateStopped:
			m.state = machinecore.StateStopped
		case requestedState == machinecore.StatePaused:
			m.state = machinecore.StatePaused
		case errors.Is(result.Err, cpu.ErrStopped):
			m.state = machinecore.StateStopped
		default:
			m.state = machinecore.StatePaused
		}
	case cpu.StopFault:
		m.state = machinecore.StateFaulted
	default:
		m.state = machinecore.StateFaulted
	}
	if m.wipi != nil {
		fault := ""
		if result.Err != nil {
			fault = result.Err.Error()
		}
		if err := m.wipi.FinishServiceExecution(
			m.state,
			result.Instructions,
			fault,
		); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	if result.Err != nil && !errors.Is(result.Err, cpu.ErrStopped) {
		return fmt.Errorf("execute guest at 0x%08x: %w", result.PC, result.Err)
	}
	return nil
}

func (m *Machine) runWIPISlice(
	ctx context.Context,
	pc uint32,
	mode cpu.Mode,
	budget uint64,
	stopOnPresent bool,
) cpu.Result {
	var instructions uint64
	presentations := uint32(0)
	javaPresentations := uint32(0)
	stopOnPresent = stopOnPresent && m.wipi != nil
	if stopOnPresent {
		presentations = m.wipi.Stats.PresentCount
		if m.raptor != nil && m.raptor.Java != nil {
			javaPresentations = m.raptor.Java.Host.PresentCount
		}
	}
	for instructions < budget {
		run := m.cpu.Run(ctx, pc, mode, budget-instructions)
		instructions += run.Instructions
		run.Instructions = instructions
		if run.Err != nil || run.Reason != cpu.StopBreakpoint || m.wipi == nil {
			return run
		}
		if run.PC < 2 {
			return run
		}
		trap := run.PC - 2
		var handled bool
		var err error
		if m.raptor != nil {
			handled, err = m.raptor.DispatchTrap(ctx, trap)
		}
		if err == nil && !handled {
			handled, err = m.wipi.DispatchTrap(ctx, trap)
		}
		if err != nil {
			run.Reason = cpu.StopFault
			run.Err = err
			return run
		}
		if !handled {
			return run
		}
		if m.wipi.ExitRequested {
			run.Reason = cpu.StopExited
			run.PC = 0
			return run
		}
		nextPC, err := m.cpu.ReadRegister(cpu.RegisterPC)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           run.PC,
				Err:          err,
			}
		}
		javaPresented := m.raptor != nil && m.raptor.Java != nil &&
			m.raptor.Java.Host.PresentCount != javaPresentations
		if stopOnPresent && (m.wipi.Stats.PresentCount != presentations || javaPresented) {
			// A frontend frame is a presentation quantum. Yield immediately
			// after the guest submits visible output instead of running the
			// remainder of the handset budget and hiding intermediate frames.
			return cpu.Result{
				Reason:       cpu.StopBudget,
				Instructions: instructions,
				PC:           nextPC,
			}
		}
		if instructions >= budget {
			return cpu.Result{
				Reason:       cpu.StopBudget,
				Instructions: instructions,
				PC:           nextPC,
			}
		}
		cpsr, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
		if err != nil {
			return cpu.Result{
				Reason:       cpu.StopFault,
				Instructions: instructions,
				PC:           nextPC,
				Err:          err,
			}
		}
		mode = cpu.ModeARM
		if cpsr&cpu.StatusThumb != 0 {
			mode = cpu.ModeThumb
		}
		pc = nextPC
	}
	return cpu.Result{
		Reason:       cpu.StopBudget,
		Instructions: instructions,
		PC:           pc,
	}
}

const (
	raptorCallbackInstructionLimit = uint64(64_000_000)
)

func (m *Machine) pumpWIPICallbacks(
	ctx context.Context,
	elapsed time.Duration,
) (cpu.Result, bool, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.Result{}, false, cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return cpu.Result{}, false, fmt.Errorf(
			"pump WIPI callbacks from %s: %w",
			state,
			ErrInvalidState,
		)
	}
	previousState := m.state
	m.state = machinecore.StateRunning
	if err := m.wipi.BeginServiceExecution(); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	callbacks := append([]wipirt.GuestCallback(nil), m.wipi.PendingCallbacks...)
	m.wipi.PendingCallbacks = nil
	target := m.wipi.Services.Clock.Monotonic() + elapsed
	pendingInput := m.input[:0]
	for _, event := range m.input {
		if event.At > target {
			pendingInput = append(pendingInput, event)
			continue
		}
		if err := m.wipi.Services.QueueInput(
			m.wipi.ServiceOwner,
			event.Control,
			event.Pressed,
			event.At,
		); err != nil {
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
			_ = m.wipi.FinishServiceExecution(
				m.state,
				0,
				err.Error(),
			)
			m.mu.Unlock()
			return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
		}
	}
	m.input = pendingInput
	if err := m.wipi.Services.Advance(
		m.wipi.ServiceOwner,
		elapsed,
	); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		_ = m.wipi.FinishServiceExecution(m.state, 0, err.Error())
		m.mu.Unlock()
		return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
	}
	m.wipi.TickMS = uint64(m.wipi.Services.Clock.Monotonic() / time.Millisecond)
	for {
		event, ready := m.wipi.Services.Events.PopReady(
			m.wipi.Services.Clock.Monotonic(),
		)
		if !ready {
			break
		}
		switch event.Kind {
		case shared.EventInputPress, shared.EventInputRelease, shared.EventInputRepeat:
			if m.raptor == nil || !m.raptor.Started {
				continue
			}
			input := machinecore.InputEvent{
				Control: event.Control,
				Pressed: event.Kind != shared.EventInputRelease,
				At:      event.At,
			}
			callback, ok := m.raptor.JavaInputCallback(input)
			if !ok {
				callback, ok = raptorrt.InputCallback(
					m.raptor.Clet.HandleEvent,
					input,
				)
			}
			if ok {
				callbacks = append(callbacks, callback)
			}
		case shared.EventTimer:
			address := uint32(event.Value)
			timer, active := m.wipi.Timers[address]
			if !active || m.wipi.TimerServices[address] != event.ServiceID {
				continue
			}
			delete(m.wipi.Timers, address)
			// Public WIPI timers expose an active field at +24. LGT Raptor's
			// MCTimer is only a callback word, so adjacent application memory
			// must remain untouched when the timer fires.
			if address != 0 && m.raptor == nil {
				if err := m.wipi.WriteU32(address+24, 0); err != nil {
					m.state = machinecore.StateFaulted
					m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
					_ = m.wipi.FinishServiceExecution(
						m.state,
						0,
						err.Error(),
					)
					m.mu.Unlock()
					return cpu.Result{Reason: cpu.StopFault, Err: err}, false, err
				}
			}
			if timer.Callback != 0 {
				callbacks = append(callbacks, wipirt.GuestCallback{
					Procedure: timer.Callback,
					Args:      [4]uint32{address, timer.Parameter},
				})
			}
		case shared.EventAudioComplete:
			for handle, serviceID := range m.wipi.MediaServices {
				if serviceID != event.ServiceID {
					continue
				}
				if clip := m.wipi.MediaClips[handle]; clip != nil {
					clip.State = 0
					clip.Repeat = false
					m.wipi.EnqueueCallback(clip.Callback, handle, 0)
				}
				break
			}
		}
	}
	callbacks = append(callbacks, m.wipi.PendingCallbacks...)
	m.wipi.PendingCallbacks = nil
	if m.raptor != nil && len(callbacks) != 0 {
		for _, callback := range callbacks {
			m.raptor.CallbackTasks = append(
				m.raptor.CallbackTasks,
				&raptorrt.CallbackTask{Callback: callback},
			)
		}
		callbacks = nil
	}
	m.mu.Unlock()

	var callbackResult cpu.Result
	var callbackErr error
	for _, callback := range callbacks {
		result, _, err := m.invokeWIPICallback(ctx, callback)
		callbackResult.Instructions += result.Instructions
		callbackResult.PC = result.PC
		callbackResult.Reason = result.Reason
		callbackResult.Err = result.Err
		callbackErr = err
		if callbackErr != nil || result.Reason == cpu.StopExited {
			break
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if callbackErr != nil {
		m.lastResult = callbackResult
		if errors.Is(callbackErr, cpu.ErrStopped) ||
			errors.Is(callbackErr, context.Canceled) ||
			errors.Is(callbackErr, context.DeadlineExceeded) {
			m.state = machinecore.StatePaused
		} else {
			m.state = machinecore.StateFaulted
		}
		if serviceErr := m.wipi.FinishServiceExecution(
			m.state,
			callbackResult.Instructions,
			callbackErr.Error(),
		); serviceErr != nil && m.state != machinecore.StateFaulted {
			m.state = machinecore.StateFaulted
			return callbackResult, false, serviceErr
		}
		return callbackResult, false, callbackErr
	}
	if callbackResult.Reason == cpu.StopExited {
		m.lastResult = callbackResult
		m.state = machinecore.StateStopped
		if err := m.wipi.FinishServiceExecution(
			m.state,
			callbackResult.Instructions,
			"",
		); err != nil {
			m.state = machinecore.StateFaulted
			return callbackResult, false, err
		}
		return callbackResult, true, nil
	}
	if m.closed {
		m.state = machinecore.StateStopped
		_ = m.wipi.FinishServiceExecution(
			m.state,
			callbackResult.Instructions,
			"",
		)
		return callbackResult, true, cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning {
		m.state = previousState
	}
	if err := m.wipi.FinishServiceExecution(
		m.state,
		callbackResult.Instructions,
		"",
	); err != nil {
		m.state = machinecore.StateFaulted
		return callbackResult, false, err
	}
	return callbackResult, m.state == machinecore.StateStopped, nil
}

func (m *Machine) invokeWIPICallback(
	ctx context.Context,
	callback wipirt.GuestCallback,
) (result cpu.Result, returnValue uint32, returnedErr error) {
	savedContext, err := m.cpu.SaveContext()
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	defer func() {
		if restoreErr := m.cpu.RestoreContext(savedContext); restoreErr != nil && returnedErr == nil {
			result = cpu.Result{Reason: cpu.StopFault, Err: restoreErr}
			returnValue = 0
			returnedErr = restoreErr
		}
	}()

	for register := cpu.RegisterR0; register <= cpu.RegisterR3; register++ {
		if err := m.cpu.WriteRegister(register, callback.Args[register]); err != nil {
			return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
		}
	}
	if err := m.cpu.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	if err := m.cpu.WriteRegister(cpu.RegisterPC, callback.Procedure&^1); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	cpsr, err := m.cpu.ReadRegister(cpu.RegisterCPSR)
	if err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	mode := cpu.ModeARM
	if callback.Procedure&1 != 0 {
		cpsr |= cpu.StatusThumb
		mode = cpu.ModeThumb
	} else {
		cpsr &^= cpu.StatusThumb
	}
	if err := m.cpu.WriteRegister(cpu.RegisterCPSR, cpsr); err != nil {
		return cpu.Result{Reason: cpu.StopFault, Err: err}, 0, err
	}
	instructionLimit := wipirt.CallbackInstructionLimit
	if m.raptor != nil {
		instructionLimit = raptorCallbackInstructionLimit
	}
	result = m.runWIPISlice(
		ctx,
		callback.Procedure&^1,
		mode,
		instructionLimit,
		false,
	)
	if result.Err != nil {
		if m.raptor != nil {
			registers := make([]uint32, cpu.RegisterR12+1)
			for register := range registers {
				registers[register], _ = m.cpu.ReadRegister(uint32(register))
			}
			sp, _ := m.cpu.ReadRegister(cpu.RegisterSP)
			lr, _ := m.cpu.ReadRegister(cpu.RegisterLR)
			status, _ := m.cpu.ReadRegister(cpu.RegisterCPSR)
			stack := make([]uint32, 16)
			data := make([]byte, len(stack)*4)
			if readErr := m.cpu.ReadMemory(sp, data); readErr == nil {
				for index := range stack {
					stack[index] = binary.LittleEndian.Uint32(data[index*4:])
				}
			} else {
				stack = nil
			}
			result.Err = fmt.Errorf(
				"%w (r0-r12=%08x sp=%08x lr=%08x cpsr=%08x stack=%08x)",
				result.Err,
				registers,
				sp,
				lr,
				status,
				stack,
			)
		}
		return result, 0, result.Err
	}
	if result.Reason == cpu.StopExited {
		return result, 0, nil
	}
	if result.Reason != cpu.StopBreakpoint || result.PC < 2 ||
		result.PC-2 != guest.ReturnSentinel {
		err := fmt.Errorf(
			"WIPI callback 0x%08x did not return within %d instructions (stop %d at 0x%08x)",
			callback.Procedure,
			instructionLimit,
			result.Reason,
			result.PC,
		)
		result.Reason = cpu.StopFault
		result.Err = err
		return result, 0, err
	}
	returnValue, err = m.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		result.Reason = cpu.StopFault
		result.Err = err
		return result, 0, err
	}
	return result, returnValue, nil
}
