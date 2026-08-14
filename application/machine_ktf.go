// Package application implements ARAM's WIPI native-application machine.
package application

import (
	"context"
	"errors"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

func (m *Machine) runKTFSlice(ctx context.Context, elapsed time.Duration) error {
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
		return fmt.Errorf("execute KTF application from %s: %w", state, ErrInvalidState)
	}
	if elapsed < 0 {
		m.mu.Unlock()
		return fmt.Errorf("advance KTF clock: negative elapsed time %s", elapsed)
	}
	runtime := m.ktf
	started := m.ktfStarted
	budget := m.runBudget
	if m.ktfRunBudget != 0 {
		budget = m.ktfRunBudget
	}
	if budget < ktfrt.RunBudgetMin {
		budget = ktfrt.RunBudgetMin
	}
	if _, err := runtime.Services.Coordinator.BeginQuantum(); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	if err := runtime.Services.Coordinator.Transition(
		runtime.ServiceOwner,
		shared.LifecycleRunning,
		runtime.Services.Clock.Monotonic(),
		runtime.Services.Events,
	); err != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	m.state = machinecore.StateRunning
	m.mu.Unlock()

	if !started {
		if err := runtime.StartMainClass(ctx); err != nil {
			_ = runtime.Services.Coordinator.Fault(
				runtime.ServiceOwner,
				err.Error(),
				runtime.Services.Clock.Monotonic(),
				runtime.Services.Events,
			)
			m.mu.Lock()
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
			m.mu.Unlock()
			return err
		}
		m.mu.Lock()
		m.ktfStarted = true
		m.mu.Unlock()
	}
	if err := runtime.Services.Advance(
		runtime.ServiceOwner,
		elapsed,
	); err != nil {
		_ = runtime.Services.Coordinator.Fault(
			runtime.ServiceOwner,
			err.Error(),
			runtime.Services.Clock.Monotonic(),
			runtime.Services.Events,
		)
		m.mu.Lock()
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	runtime.TickMS = uint64(
		runtime.Services.Clock.Monotonic() / time.Millisecond,
	)
	if err := m.queueKTFInput(runtime); err != nil {
		_ = runtime.Services.Coordinator.Fault(
			runtime.ServiceOwner,
			err.Error(),
			runtime.Services.Clock.Monotonic(),
			runtime.Services.Events,
		)
		m.mu.Lock()
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		m.mu.Unlock()
		return err
	}
	result := cpu.Result{Reason: cpu.StopBudget}
	var instructions uint64
	var consumeErr error
	runtime.PaintStalled = false
taskLoop:
	for slices := 0; slices < ktfTaskSlicesPerQuantumMax &&
		instructions < budget; slices++ {
		remaining := budget - instructions
		presentations := runtime.PresentCount
		sliceResult := runtime.RunTaskSlice(ctx, remaining)
		if sliceResult.Instructions > remaining {
			sliceResult = cpu.Result{
				Reason:       cpu.StopFault,
				PC:           sliceResult.PC,
				Instructions: remaining,
				Err: fmt.Errorf(
					"KTF task exceeded quantum budget: used %d, remaining %d",
					sliceResult.Instructions,
					remaining,
				),
			}
		}
		instructions += sliceResult.Instructions
		if err := runtime.Services.Coordinator.Consume(
			runtime.ServiceOwner,
			sliceResult.Instructions,
		); err != nil {
			consumeErr = err
			result = cpu.Result{
				Reason:       cpu.StopFault,
				PC:           sliceResult.PC,
				Instructions: instructions,
				Err:          err,
			}
			break
		}
		sliceResult.Instructions = instructions
		result = sliceResult
		if sliceResult.Err != nil {
			break
		}
		if runtime.PresentCount != presentations {
			// StepFrame is a presentation quantum. Once the guest submits a
			// frame, return it to the frontend instead of allowing an
			// uncapped paint loop to render many invisible intermediate
			// frames in one host update.
			break
		}
		if runtime.PaintStalled {
			// The card the guest keeps asking to repaint is waiting on a
			// paint task that is inside Thread.sleep, and the virtual clock
			// does not move until this quantum ends. Spending the remaining
			// slices re-running the guest's event loop cannot produce a
			// frame, so return the quantum and let time advance instead.
			break
		}
		switch sliceResult.Reason {
		case cpu.StopBudget:
			// A Java task can yield or return before consuming the CPU
			// quantum. Continue with the next runnable task without advancing
			// virtual time, matching the handset's cooperative scheduler.
			continue
		default:
			break taskLoop
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResult = result
	if consumeErr != nil {
		m.state = machinecore.StateFaulted
		m.lastResult = cpu.Result{
			Reason: cpu.StopFault,
			PC:     result.PC, Instructions: result.Instructions, Err: consumeErr,
		}
		_ = runtime.Services.Coordinator.Fault(
			runtime.ServiceOwner,
			consumeErr.Error(),
			runtime.Services.Clock.Monotonic(),
			runtime.Services.Events,
		)
		return consumeErr
	}
	switch result.Reason {
	case cpu.StopBudget, cpu.StopBreakpoint:
		m.state = machinecore.StatePaused
	case cpu.StopExited:
		if runtime.CanAwaitEvents() {
			m.state = machinecore.StatePaused
		} else {
			m.state = machinecore.StateStopped
		}
	case cpu.StopRequested:
		m.state = machinecore.StatePaused
	default:
		m.state = machinecore.StateFaulted
	}
	if m.state == machinecore.StateFaulted {
		message := "KTF guest execution fault"
		if result.Err != nil {
			message = result.Err.Error()
		}
		_ = runtime.Services.Coordinator.Fault(
			runtime.ServiceOwner,
			message,
			runtime.Services.Clock.Monotonic(),
			runtime.Services.Events,
		)
	} else {
		target := shared.LifecyclePaused
		if m.state == machinecore.StateStopped {
			target = shared.LifecycleStopped
		}
		if err := runtime.Services.Coordinator.Transition(
			runtime.ServiceOwner,
			target,
			runtime.Services.Clock.Monotonic(),
			runtime.Services.Events,
		); err != nil {
			m.state = machinecore.StateFaulted
			m.lastResult.Err = err
			return err
		}
	}
	if result.Err != nil && !errors.Is(result.Err, cpu.ErrStopped) {
		return fmt.Errorf("execute KTF guest at 0x%08x: %w", result.PC, result.Err)
	}
	return nil
}

func (m *Machine) queueKTFInput(runtime *ktfrt.Runtime) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Duration(runtime.TickMS) * time.Millisecond
	for index, event := range m.input {
		if event.At > now {
			continue
		}
		if _, known := guest.InputKeyCode(event.Control); known &&
			(runtime.DisplayCards[runtime.DefaultDisplay] == 0 ||
				!runtime.HasJavaTaskCapacity()) {
			break
		}
		if err := runtime.Services.QueueInput(
			runtime.ServiceOwner,
			event.Control,
			event.Pressed,
			event.At,
		); err != nil {
			return fmt.Errorf("queue shared KTF input %q: %w", event.Control, err)
		}
		m.input = append(m.input[:index], m.input[index+1:]...)
		break
	}
	return runtime.DrainServiceEvents(now)
}
