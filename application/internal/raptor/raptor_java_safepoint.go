package raptor

// raptorJavaSafepointYieldThreshold is deliberately high enough that ordinary
// loops keep running in one slice, while a callback that is genuinely parked on
// one AOT backedge gives another Java task a chance well before it consumes a
// handset frame. The LGT AOT runtime calls module 100 ordinal 85 at loop
// backedges; whole-image audits for issue #240 established it as a safepoint,
// not an exception-frame pop.
const raptorJavaSafepointYieldThreshold = uint32(1024)

// SetCallbackTaskActive scopes safepoint preemption to resumable callback
// tasks. Synchronous Java/Clet invocations are expected to return in the same
// host call and cannot safely be suspended by a cooperative yield.
func (r *Runtime) SetCallbackTaskActive(active bool) {
	if r == nil {
		return
	}
	r.callbackTaskActive = active
	if !active {
		r.javaSafepointLR = 0
		r.javaSafepointHits = 0
	}
}

// observeJavaSafepoint records one module-100 ordinal 85 call. A single or
// changing backedge remains a no-op. Only a hot repeated backedge inside a
// resumable callback, with another runnable Java task available, requests a
// slice yield. That lets the task which owns the state being polled make
// progress instead of letting the callback consume every frame forever.
func (r *Runtime) observeJavaSafepoint(lr uint32) {
	if r == nil || !r.callbackTaskActive || !r.hasRunnableJavaTask() {
		return
	}
	if r.javaSafepointLR != lr {
		r.javaSafepointLR = lr
		r.javaSafepointHits = 1
		return
	}
	if r.javaSafepointHits < raptorJavaSafepointYieldThreshold {
		r.javaSafepointHits++
	}
	if r.javaSafepointHits < raptorJavaSafepointYieldThreshold {
		return
	}

	// runWIPISlice consumes javaYieldRequested immediately after the trap
	// returns. Keep the separate yielded bit until runRaptorCallbackTask has
	// saved the callback CPU context and can safely schedule another Java task.
	r.javaSafepointHits = 0
	r.javaYieldRequested = true
	r.javaSafepointYielded = true
}

func (r *Runtime) hasRunnableJavaTask() bool {
	if r == nil || r.Java == nil {
		return false
	}
	now := uint64(0)
	if r.Public != nil {
		now = r.Public.TickMS
	}
	for _, task := range r.Java.Tasks {
		if task != nil && !task.Done && task.WakeAtMS <= now {
			return true
		}
	}
	return false
}

func (r *Runtime) resetJavaSafepointSlice() {
	if r == nil {
		return
	}
	r.javaSafepointLR = 0
	r.javaSafepointHits = 0
	r.javaSafepointYielded = false
}

// TakeJavaSafepointYield reports whether the just-finished guest slice stopped
// specifically at a cooperative Java safepoint. It is separate from
// TakeJavaYield because the latter is consumed inside runWIPISlice before the
// callback context has been saved.
func (r *Runtime) TakeJavaSafepointYield() bool {
	if r == nil || !r.javaSafepointYielded {
		return false
	}
	r.javaSafepointYielded = false
	return true
}
