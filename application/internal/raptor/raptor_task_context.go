package raptor

import (
	"errors"

	"github.com/mirusu400/aram-core/cpu"
)

// taskExecutionContext keeps a task's registers in the CPU backend's reusable
// form between slices. Restoring that form switches tasks without retiring
// the TLB and every translated block, which RestoreContext has to do because
// portable bytes may describe a different machine. Before this the callback
// and Java task switches went through the portable bytes every slice, and a
// title retranslated its whole working set every two or three frames:
// 판타지포에버3 translated 125,013 native blocks over 600 frames, 23% of the
// frame, against 220 flushes that matched its 221 serialized saves.
//
// The portable bytes are kept current alongside the fast form. They are what
// save state carries, what a restored state resumes from, and what a backend
// without the fast path uses; marshalling them costs a register copy, while
// restoring them costs the translation cache.
type taskExecutionContext struct {
	fast cpu.ExecutionContext
}

// saveTaskContext captures the CPU state a task will resume from.
func saveTaskContext(
	backend cpu.Backend,
	serialized *[]byte,
	state *taskExecutionContext,
) error {
	if fast, ok := backend.(cpu.ExecutionContextBackend); ok {
		saved, err := fast.SaveExecutionContext(state.fast)
		if err != nil && state.fast != nil &&
			!errors.Is(err, cpu.ErrExecutionContextUnavailable) {
			// The retained context belongs to another backend; capture a
			// fresh one rather than failing the slice.
			saved, err = fast.SaveExecutionContext(nil)
		}
		if err == nil {
			data, err := fast.MarshalExecutionContext(saved, (*serialized)[:0])
			if err != nil {
				return err
			}
			state.fast, *serialized = saved, data
			return nil
		}
		if !errors.Is(err, cpu.ErrExecutionContextUnavailable) {
			return err
		}
	}
	data, err := backend.SaveContext()
	if err != nil {
		return err
	}
	state.fast, *serialized = nil, data
	return nil
}

// restoreTaskContext puts a task's saved CPU state back, through the fast
// form when the backend still accepts it and the portable bytes otherwise (a
// restored save state, or a backend without the fast path).
func restoreTaskContext(
	backend cpu.Backend,
	serialized []byte,
	state *taskExecutionContext,
) error {
	if fast, ok := backend.(cpu.ExecutionContextBackend); ok && state.fast != nil {
		err := fast.RestoreExecutionContext(state.fast)
		if err == nil {
			return nil
		}
		if !errors.Is(err, cpu.ErrExecutionContextUnavailable) {
			return err
		}
	}
	return backend.RestoreContext(serialized)
}

// HasContext reports whether the task has run before and carries the state to
// resume from.
func (t *CallbackTask) HasContext() bool {
	return t != nil && (len(t.Context) != 0 || t.execution.fast != nil)
}

// SaveContext records where this task's slice stopped.
func (t *CallbackTask) SaveContext(backend cpu.Backend) error {
	return saveTaskContext(backend, &t.Context, &t.execution)
}

// RestoreContext resumes this task's registers.
func (t *CallbackTask) RestoreContext(backend cpu.Backend) error {
	return restoreTaskContext(backend, t.Context, &t.execution)
}

// HasContext reports whether the thread has run before and carries the state
// to resume from.
func (t *JavaTask) HasContext() bool {
	return t != nil && (len(t.Context) != 0 || t.execution.fast != nil)
}

// SaveContext records where this thread's slice stopped.
func (t *JavaTask) SaveContext(backend cpu.Backend) error {
	return saveTaskContext(backend, &t.Context, &t.execution)
}

// RestoreContext resumes this thread's registers.
func (t *JavaTask) RestoreContext(backend cpu.Backend) error {
	return restoreTaskContext(backend, t.Context, &t.execution)
}
