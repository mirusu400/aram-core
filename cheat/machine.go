package cheat

import (
	"context"
	"fmt"
	"image"
	"io"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Machine wraps a core machine and enforces enabled memory cheats at lifecycle
// boundaries. Freeze codes are reapplied after every emulated frame.
type Machine struct {
	machine machinecore.Machine
	engine  *Engine
}

func Wrap(
	machine machinecore.Machine,
	memory Memory,
	options Options,
) (*Machine, error) {
	engine, err := New(memory, options)
	if err != nil {
		return nil, err
	}
	return WrapWithEngine(machine, engine)
}

func WrapWithEngine(
	machine machinecore.Machine,
	engine *Engine,
) (*Machine, error) {
	if machine == nil {
		return nil, fmt.Errorf("cheat target machine is nil")
	}
	if engine == nil {
		return nil, fmt.Errorf("cheat engine is nil")
	}
	return &Machine{machine: machine, engine: engine}, nil
}

func (m *Machine) Cheats() *Engine {
	return m.engine
}

// Unwrap returns the wrapped machine so a host can probe the optional
// interfaces this wrapper does not restate, such as image and diagnostic
// reporting. Guest execution and memory still belong to the wrapper: reaching
// through it for anything that runs or mutates the guest loses the guarantee
// that cheats stay serialized with execution.
func (m *Machine) Unwrap() machinecore.Machine {
	return m.machine
}

func (m *Machine) Load(ctx context.Context, source machinecore.Source) error {
	return m.engine.runMachine(
		func() error { return m.machine.Load(ctx, source) },
		applyAllEnabled,
	)
}

func (m *Machine) State() machinecore.State {
	return m.machine.State()
}

func (m *Machine) Start(ctx context.Context) error {
	return m.engine.runMachine(
		func() error { return m.machine.Start(ctx) },
		applyFrozen,
	)
}

func (m *Machine) Pause() error {
	return m.engine.runMachine(m.machine.Pause, applyNone)
}

func (m *Machine) Resume() error {
	return m.engine.runMachine(m.machine.Resume, applyFrozen)
}

func (m *Machine) Stop() error {
	return m.engine.runMachine(m.machine.Stop, applyNone)
}

func (m *Machine) Reset(ctx context.Context) error {
	return m.engine.runMachine(
		func() error { return m.machine.Reset(ctx) },
		applyAllEnabled,
	)
}

func (m *Machine) StepFrame(ctx context.Context) error {
	return m.engine.runMachine(
		func() error { return m.machine.StepFrame(ctx) },
		applyFrozen,
	)
}

func (m *Machine) QueueInput(event machinecore.InputEvent) error {
	return m.engine.runMachine(
		func() error { return m.machine.QueueInput(event) },
		applyNone,
	)
}

func (m *Machine) Framebuffer() image.Image {
	return m.machine.Framebuffer()
}

func (m *Machine) DrainAudio() machinecore.AudioChunk {
	return m.machine.DrainAudio()
}

func (m *Machine) SaveState(output io.Writer) error {
	return m.engine.runMachine(
		func() error { return m.machine.SaveState(output) },
		applyNone,
	)
}

func (m *Machine) LoadState(input io.Reader) error {
	return m.engine.runMachine(
		func() error { return m.machine.LoadState(input) },
		applyAllEnabled,
	)
}

func (m *Machine) Close() error {
	return m.engine.runMachine(m.machine.Close, applyNone)
}

var _ machinecore.Machine = (*Machine)(nil)
