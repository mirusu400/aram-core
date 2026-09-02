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
	// The quantum is handed to the guest in as few pieces as the title's own
	// timers need. A title that is not waiting on one gets the whole quantum at
	// once, exactly as before; a title parked in Thread.sleep gets the clock
	// stopped on its deadline first, so a wait it asked to be 40 ms long is not
	// rounded up to the next whole 16.667 ms presentation quantum.
	advanced := time.Duration(0)
	steps := 0
	advance := func(step time.Duration) error {
		if step <= 0 {
			return nil
		}
		start := runtime.Services.Clock.Monotonic()
		if err := runtime.Services.Advance(runtime.ServiceOwner, step); err != nil {
			return err
		}
		// Services.Advance is the transaction commit point. Transfer PCM
		// ownership now, before a potentially slow CPU task, so the host audio
		// consumer never waits for the rest of StepFrame.
		m.publishAudioFromMedia(runtime.Services.Media, start)
		advanced += step
		runtime.TickMS = uint64(
			runtime.Services.Clock.Monotonic() / time.Millisecond,
		)
		if step != elapsed {
			runtime.TraceQuantumStep(step)
		}
		return nil
	}
	// nextStep sizes the next piece of the quantum: up to the deadline the guest
	// is sleeping on, or the whole remainder once it has nothing left to wait
	// for.
	nextStep := func() time.Duration {
		remaining := elapsed - advanced
		if remaining <= 0 {
			return 0
		}
		if steps >= ktfQuantumStepsMax {
			return remaining
		}
		if wake, ok := runtime.NextWakeWithin(remaining); ok {
			steps++
			return wake
		}
		return remaining
	}
	if err := advance(nextStep()); err != nil {
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
	presents := 0
	result := cpu.Result{Reason: cpu.StopBudget}
	var instructions uint64
	var consumeErr error
	var advanceErr error
	runtime.PaintStalled = false
taskLoop:
	for slices := 0; slices < ktfTaskSlicesPerQuantumMax &&
		instructions < budget; slices++ {
		remaining := budget - instructions
		// Hand the guest any input it can now take. A quantum that carries
		// several submitted frames keeps the card busy for most of its length,
		// so offering input only once at the top of the quantum means a title
		// whose paint task is almost always pending never sees a key at all.
		if err := m.queueKTFInput(runtime); err != nil {
			result = cpu.Result{
				Reason:       cpu.StopFault,
				PC:           result.PC,
				Instructions: instructions,
				Err:          err,
			}
			break
		}
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
			presents++
			// StepFrame is a presentation quantum, but stopping at the very
			// first submitted frame hands a title that paints one element per
			// repaint almost none of the quantum: 메이플스토리 궁수편 draws its main
			// menu that way and got about five hundred guest instructions per
			// 16.67 ms against a budget of a million, so the menu was still
			// half-composed when the title's own two-second timer wiped it and
			// the player saw an all but empty screen (issue #55). Allowing a
			// few frames per quantum gives that loop room to finish while
			// still capping an uncapped paint loop, which would otherwise
			// render many invisible intermediate frames in one host update -
			// and, in 스파이더맨3, queue Java paint tasks faster than they
			// retire until the task table overflows.
			if presents >= ktfPresentsPerQuantumMax {
				break
			}
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
			if runtime.HasRunnableTask() {
				continue
			}
			// Nothing can run until a timer expires. Give the guest the next
			// piece of the quantum so a wait it asked to be forty milliseconds
			// long ends there instead of at the next quantum boundary, which
			// used to round every guest sleep up to 16.667 ms.
			step := nextStep()
			if step <= 0 {
				break taskLoop
			}
			if err := advance(step); err != nil {
				advanceErr = err
				result = cpu.Result{
					Reason:       cpu.StopFault,
					PC:           result.PC,
					Instructions: instructions,
					Err:          err,
				}
				break taskLoop
			}
			continue
		default:
			break taskLoop
		}
	}

	// The frontend paces the machine by counting quanta, so a quantum always
	// hands the guest exactly the time it was given even when the task loop
	// returned early with some of it unspent.
	if err := advance(elapsed - advanced); err != nil && advanceErr == nil {
		advanceErr = err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResult = result
	if advanceErr != nil && consumeErr == nil {
		consumeErr = advanceErr
	}
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
			!runtime.CanQueueKeyEvent() {
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
