package raptor

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// maxRaptorJavaSleepMS bounds how long one Thread.sleep call parks a Java
// task. A handset resumes a sleeping thread from an interrupt or a timer we do
// not model, so an unbounded park would strand a title that sleeps until it is
// woken. Capping the park keeps the thread scheduled again within a second.
const maxRaptorJavaSleepMS = 1000

// sleepRaptorJavaTask parks the running Raptor Java task for the requested
// number of milliseconds and yields the CPU slice.
//
// The Raptor Java host runs with ktf.Runtime.DeferThreads disabled, because the
// Raptor machine schedules its Java threads itself instead of through the KTF
// scheduler. That left java/lang/Thread.sleep as a plain no-op: a title whose
// thread paces itself with
//
//	while (running) { step(); Thread.sleep(frameDelay); }
//
// spun the loop for the whole instruction budget instead of once per frame.
// 현영맞고2006 issued 2300 sleeps a frame that way (issue #79), so its own
// notion of elapsed time ran thousands of times faster than the clock its
// state machine waits on and the title never left its opening screens.
//
// Parking records the wake time on the task; NextRunnableJavaTask skips a task
// that is still sleeping, and the machine yields the slice so the remaining
// budget goes to the other threads and to the frame.
func (r *Runtime) sleepRaptorJavaTask(java *JavaRuntime) (guest.WIPIReturn, error) {
	low, err := r.CPU.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	high, err := r.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return guest.WIPIReturn{}, err
	}
	millis := int64(uint64(high)<<32 | uint64(low))
	task := java.activeTask
	if task == nil || millis <= 0 {
		// Sleeping outside a scheduled Java task (from a Clet callback, say)
		// has nothing to park, and a zero sleep is only a yield point.
		return guest.WIPIReturn{}, nil
	}
	delay := uint64(millis)
	if delay > maxRaptorJavaSleepMS {
		delay = maxRaptorJavaSleepMS
	}
	task.WakeAtMS = r.Public.TickMS + delay
	r.javaYieldRequested = true
	return guest.WIPIReturn{}, nil
}

// SetActiveJavaTask names the Java task the machine is about to run, so a
// Thread.sleep issued by that task parks the right thread.
func (r *Runtime) SetActiveJavaTask(task *JavaTask) {
	if r == nil || r.Java == nil {
		return
	}
	r.Java.activeTask = task
}

// TakeJavaYield reports and clears a pending Java-thread yield. The machine
// ends the CPU slice on one so a parked thread stops consuming the frame's
// instruction budget.
func (r *Runtime) TakeJavaYield() bool {
	if r == nil || !r.javaYieldRequested {
		return false
	}
	r.javaYieldRequested = false
	return true
}
