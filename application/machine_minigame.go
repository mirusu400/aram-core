// Package application implements ARAM's WIPI native-application machine.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func (m *Machine) stepMinigameFrame(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	if m.minigame == nil {
		m.mu.Unlock()
		return fmt.Errorf("step EADS frame: title runtime is unavailable")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused:
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("execute from %s: %w", state, ErrInvalidState)
	}
	runtime := m.minigame
	m.state = machinecore.StateRunning
	if m.wipi != nil {
		if err := m.wipi.BeginServiceExecution(); err != nil {
			m.state = machinecore.StateFaulted
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()

	result, completed, err := runtime.StepFrame(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if errors.Is(err, cpu.ErrStopped) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			m.state = machinecore.StatePaused
			m.lastResult = cpu.Result{Reason: cpu.StopRequested, Err: err}
		} else {
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{Reason: cpu.StopFault, Err: err}
		}
		if m.wipi != nil {
			_ = m.wipi.FinishServiceExecution(
				m.state,
				m.lastResult.Instructions,
				err.Error(),
			)
		}
		return err
	}
	runtime.SyncFrame()
	reason := cpu.StopBudget
	pc, readErr := m.cpu.ReadRegister(cpu.RegisterPC)
	if readErr != nil {
		m.state = machinecore.StateFaulted
		return readErr
	}
	if completed {
		reason = cpu.StopBreakpoint
		pc = guest.ReturnSentinel
	}
	m.lastResult = cpu.Result{
		Reason:       reason,
		Instructions: result.Instructions,
		PC:           pc,
	}
	m.state = machinecore.StatePaused
	if m.wipi != nil {
		if err := m.wipi.FinishServiceExecution(
			m.state,
			m.lastResult.Instructions,
			"",
		); err != nil {
			m.state = machinecore.StateFaulted
			return err
		}
	}
	return nil
}
