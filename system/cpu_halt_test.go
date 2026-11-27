package system

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

type haltTestSink struct{ irq bool }

func (s *haltTestSink) SetInterruptLine(line cpu.InterruptLine, asserted bool) error {
	if line == cpu.InterruptIRQ {
		s.irq = asserted
	}
	return nil
}

// haltTestCPU retires its whole budget unless told to stop, like a CPU
// spinning in an idle loop. At haltAt executed instructions it stores to
// the clock-gate register.
type haltTestCPU struct {
	executed uint64
	haltAt   uint64
	halt     *CPUHaltLine
	stopped  bool
	calls    int
}

func (c *haltTestCPU) Run(_ context.Context, pc uint32, _ cpu.Mode, budget uint64) cpu.Result {
	c.calls++
	c.stopped = false
	if c.haltAt != 0 && c.executed < c.haltAt && c.executed+budget > c.haltAt {
		ran := c.haltAt - c.executed
		c.executed = c.haltAt
		c.halt.Request()
		if c.stopped {
			return cpu.Result{Reason: cpu.StopRequested, Instructions: ran, PC: pc + 4}
		}
		budget -= ran
		c.executed += budget
		return cpu.Result{Reason: cpu.StopBudget, Instructions: ran + budget, PC: pc}
	}
	c.executed += budget
	return cpu.Result{Reason: cpu.StopBudget, Instructions: budget, PC: pc}
}

// haltTestTimer raises the interrupt line after a number of instructions of
// guest time and lowers it again one advance later.
type haltTestTimer struct {
	elapsed uint64
	fireAt  uint64
	line    InterruptLineSink
	raised  bool
}

func (d *haltTestTimer) Advance(retired uint64) error {
	d.elapsed += retired
	if d.raised {
		d.raised = false
		return d.line.SetInterruptLine(cpu.InterruptIRQ, false)
	}
	if d.fireAt != 0 && d.elapsed >= d.fireAt {
		d.fireAt = 0
		d.raised = true
		return d.line.SetInterruptLine(cpu.InterruptIRQ, true)
	}
	return nil
}

func TestClockedRunnerStopsTheCPUClockUntilAnInterrupt(t *testing.T) {
	backend := interpreter.New()
	sink := &haltTestSink{}
	core := &haltTestCPU{haltAt: 130}
	halt := NewCPUHaltLine(sink, func() error { core.stopped = true; return nil })
	core.halt = halt
	timer := &haltTestTimer{fireAt: 1000, line: halt}
	runner, err := NewClockedRunner(backend, core, 100, timer)
	check(t, err)
	runner.SetCPUHalt(halt)

	result := runner.Run(context.Background(), 0x1000, cpu.ModeARM, 2000)
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 2000 {
		t.Fatalf("result = %+v", result)
	}
	// Guest time is never lost: the timer saw every instruction of the run.
	if timer.elapsed != 2000 {
		t.Fatalf("clocked device saw %d instructions, want 2000", timer.elapsed)
	}
	// The clock stopped at the store (130) and restarted once the line rose:
	// 130 + nine 100-instruction slices of stopped time reaches 1030, where
	// the timer fires. The remaining 970 are executed.
	if core.executed != 130+970 {
		t.Fatalf("CPU executed %d instructions, want %d", core.executed, 130+970)
	}

	// A request made while an interrupt is already asserted does not stop
	// the clock, and a restored machine starts with it running.
	core2 := &haltTestCPU{haltAt: 50}
	halt2 := NewCPUHaltLine(sink, func() error { core2.stopped = true; return nil })
	core2.halt = halt2
	check(t, halt2.SetInterruptLine(cpu.InterruptIRQ, true))
	runner2, err := NewClockedRunner(backend, core2, 100)
	check(t, err)
	runner2.SetCPUHalt(halt2)
	if result := runner2.Run(context.Background(), 0x1000, cpu.ModeARM, 400); result.Instructions != 400 || core2.executed != 400 {
		t.Fatalf("pending interrupt: result %+v executed %d", result, core2.executed)
	}
	check(t, halt2.SetInterruptLine(cpu.InterruptIRQ, false))
	core2.haltAt = 450
	runner2.Run(context.Background(), 0x1000, cpu.ModeARM, 200)
	if core2.executed != 450 {
		t.Fatalf("CPU executed %d instructions before the stop, want 450", core2.executed)
	}
	runner2.RestartCPUClock()
	core2.haltAt = 0
	runner2.Run(context.Background(), 0x1000, cpu.ModeARM, 100)
	if core2.executed != 550 {
		t.Fatalf("CPU executed %d instructions after the restart, want 550", core2.executed)
	}
}
