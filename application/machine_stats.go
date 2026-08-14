package application

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func (m *Machine) ImageInfo() ImageInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info
}

func (m *Machine) LastResult() cpu.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastResult
}

// EADSFrameStats returns a copy of the recovered OEM lifecycle trace. The
// boolean is false for applications that do not use the MinigameQVGAOEM
// service profile.
func (m *Machine) EADSFrameStats() (EADSFrameStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.minigame == nil || m.state == machinecore.StateRunning {
		return EADSFrameStats{}, false
	}
	stats := m.minigame.Stats
	stats.Events = append([]EADSEventResult(nil), stats.Events...)
	return stats, true
}

// WIPIFrameStats returns standard public WIPI-C API and presentation activity.
func (m *Machine) WIPIFrameStats() (WIPIFrameStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil && m.state != machinecore.StateRunning {
		calls := m.ktf.HostCallCount
		var unimplemented uint64
		for _, count := range m.ktf.UnimplementedJava {
			unimplemented += count
		}
		implemented := calls
		if unimplemented < implemented {
			implemented -= unimplemented
		} else {
			implemented = 0
		}
		return WIPIFrameStats{
			PresentCount:       m.ktf.PresentCount,
			APICalls:           calls,
			ImplementedCalls:   implemented,
			UnimplementedCalls: unimplemented,
			LastAPI:            m.ktf.LastJavaMethod,
			LastUnimplemented:  m.ktf.LastUnimplementedJava,
		}, true
	}
	if m.wipi == nil || m.state == machinecore.StateRunning {
		return WIPIFrameStats{}, false
	}
	return m.wipi.Stats, true
}

// WIPIAPICoverage reports the recovered ABI size, semantically modeled subset,
// and selectors observed in this machine.
func (m *Machine) WIPIAPICoverage() (WIPIAPICoverage, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil && m.state != machinecore.StateRunning {
		return WIPIAPICoverage{}, true
	}
	if m.wipi == nil || m.state == machinecore.StateRunning {
		return WIPIAPICoverage{}, false
	}
	return m.wipi.Coverage(), true
}

// WIPIUnimplementedAPIs returns the sorted selectors actually reached without
// a semantic implementation. Catalog presence alone never counts as support.
func (m *Machine) WIPIUnimplementedAPIs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ktf != nil {
		names := make([]string, 0, len(m.ktf.UnimplementedJava))
		for name := range m.ktf.UnimplementedJava {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}
	if m.wipi == nil {
		return nil
	}
	return m.wipi.UnimplementedNames()
}

// RenderFirstFrame executes the recovered bootstrap, setup, start, preload,
// and first visible frame event sequence for MinigameQVGAOEM.
func (m *Machine) RenderFirstFrame(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return cpu.ErrClosed
	}
	if m.minigame == nil {
		m.mu.Unlock()
		return fmt.Errorf("render first frame: title runtime is unavailable")
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

	err := runtime.RenderFirstFrame(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if errors.Is(err, cpu.ErrStopped) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			m.state = machinecore.StatePaused
			m.lastResult = cpu.Result{
				Reason: cpu.StopRequested,
				Err:    err,
			}
		} else {
			m.state = machinecore.StateFaulted
			m.lastResult = cpu.Result{
				Reason: cpu.StopFault,
				Err:    err,
			}
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
	last := runtime.Stats.Events[len(runtime.Stats.Events)-1]
	runtime.SyncFrame()
	m.lastResult = cpu.Result{
		Reason:       cpu.StopBreakpoint,
		Instructions: last.Instructions,
		PC:           guest.ReturnSentinel,
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

func (m *Machine) ReadRegister(id uint32) (uint32, error) {
	return m.cpu.ReadRegister(id)
}
