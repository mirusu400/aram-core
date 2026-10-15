package raptor

const raptorClockPollYieldThreshold = uint32(64)

// clockPollState lasts for one guest slice; it is reset before a saved callback
// resumes. The clock itself remains owned by the serializable frame services.
type clockPollState struct {
	lr             uint32
	timeMS         int64
	hits           uint32
	yieldRequested bool
}

// Issue 525's Clet waits two seconds by polling MC_knlCurrentTime. Time only
// advances between frames, so spending the whole instruction budget at the
// same clock-read site cannot finish that wait. Suspend resumable callbacks
// early; synchronous constructors and startup invocations must still return.
func (r *Runtime) observeClockPoll(lr uint32) {
	if !r.callbackTaskActive {
		return
	}
	now := r.Public.Services.Clock.WallMillis()
	if r.clockPoll.lr != lr || r.clockPoll.timeMS != now {
		r.clockPoll = clockPollState{lr: lr, timeMS: now, hits: 1}
		return
	}
	if r.clockPoll.hits < raptorClockPollYieldThreshold {
		r.clockPoll.hits++
	}
	if r.clockPoll.hits >= raptorClockPollYieldThreshold {
		r.clockPoll.yieldRequested = true
	}
}

func (r *Runtime) TakeClockPollYield() bool {
	requested := r.clockPoll.yieldRequested
	r.clockPoll.yieldRequested = false
	return requested
}
