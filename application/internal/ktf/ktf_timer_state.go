package ktf

import (
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

// Short-lived callbacks can outnumber ordinary adapter objects between guest
// collections. A million scalar statuses still fits the bounded state block.
const maxKTFJavaTimerStates = 1 << 20

// Keep this extension separate from the positional metadata codec so versions
// through 15 retain their original wire layout.
type ktfJavaTimersSnapshot struct {
	Tasks  []ktfJavaTimerTaskSnapshot
	States map[uint32]uint8
}

type ktfJavaTimerTaskSnapshot struct {
	Instance, Owner      uint32
	PeriodMS, DeadlineMS uint64
	FixedRate, Started   bool
}

func (r *Runtime) writeJavaTimerState(writer *guest.StateWriter) error {
	if len(r.javaTimerTaskStates) > maxKTFJavaTimerStates {
		return fmt.Errorf("KTF Java timer status count exceeds limit")
	}
	state := ktfJavaTimersSnapshot{Tasks: make([]ktfJavaTimerTaskSnapshot, len(r.Tasks)), States: r.javaTimerTaskStates}
	for i, task := range r.Tasks {
		state.Tasks[i] = ktfJavaTimerTaskSnapshot{task.timerTask, task.timerOwner, task.timerPeriodMS, task.timerDeadlineMS, task.timerFixedRate, task.timerStarted}
	}
	encoded, err := shared.MarshalStateComponent(state)
	if err != nil {
		return fmt.Errorf("encode KTF Java timers: %w", err)
	}
	if len(encoded) > int(maxKTFStateMetadata) {
		return fmt.Errorf("KTF Java timer state exceeds limit")
	}
	writer.U32(uint32(len(encoded)))
	writer.Write(encoded)
	return nil
}

func readJavaTimerState(decoder *guest.StateDecoder, tasks []ktfTaskSnapshot) (*ktfJavaTimersSnapshot, error) {
	size := decoder.U32()
	if size > maxKTFStateMetadata {
		return nil, decoder.Fail("KTF Java timer state exceeds limit")
	}
	var state ktfJavaTimersSnapshot
	if err := shared.UnmarshalStateComponent(decoder.Bytes(int(size)), &state); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid KTF Java timer state: %v", err))
	}
	if len(state.Tasks) != len(tasks) || len(state.States) > maxKTFJavaTimerStates {
		return nil, decoder.Fail("invalid KTF Java timer counts")
	}
	for instance, value := range state.States {
		if instance == 0 || value < ktfJavaTimerScheduled || value > ktfJavaTimerCancelled {
			return nil, decoder.Fail("invalid KTF Java TimerTask status")
		}
	}
	live := make(map[uint32]bool)
	for i, task := range state.Tasks {
		if task.Instance == 0 {
			if task != (ktfJavaTimerTaskSnapshot{}) {
				return nil, decoder.Fail("invalid KTF non-timer task")
			}
			continue
		}
		if task.Owner == 0 || (task.FixedRate && task.PeriodMS == 0) {
			return nil, decoder.Fail("invalid KTF Java timer ownership")
		}
		if !tasks[i].Done {
			if live[task.Instance] || state.States[task.Instance] != ktfJavaTimerScheduled {
				return nil, decoder.Fail("invalid live KTF Java TimerTask")
			}
			live[task.Instance] = true
		}
	}
	return &state, decoder.Err
}

func (r *Runtime) restoreJavaTimerState(state *ktfJavaTimersSnapshot) {
	r.javaTimerTasks = make(map[uint32]*Task)
	r.javaTimerTaskStates = make(map[uint32]uint8)
	if state == nil {
		return
	}
	r.javaTimerTaskStates = guest.CloneMap(state.States)
	if r.javaTimerTaskStates == nil {
		r.javaTimerTaskStates = make(map[uint32]uint8)
	}
	for i, saved := range state.Tasks {
		task := r.Tasks[i]
		task.timerTask, task.timerOwner = saved.Instance, saved.Owner
		task.timerPeriodMS, task.timerDeadlineMS = saved.PeriodMS, saved.DeadlineMS
		task.timerFixedRate, task.timerStarted = saved.FixedRate, saved.Started
		if saved.Instance != 0 && !task.Done {
			r.javaTimerTasks[saved.Instance] = task
		}
	}
}
