package application

import "fmt"

// ktfHostTraceLimit bounds the retained KTF host trace. DebugSnapshot never
// returns more than MaxDebugSnapshotEntries, so an entry older than this
// window can no longer reach a caller; retaining it only cost resident memory
// and grew the slice the garbage collector rescans every cycle. A busy KTF
// title crosses fifty thousand host-bridge crossings per second, so the
// unbounded slice was the dominant allocation source in a long session.
const ktfHostTraceLimit = MaxDebugSnapshotEntries

// ktfHostTraceKeep is the window preserved when the limit is reached. Dropping
// the oldest half in a single copy keeps the amortised cost of one trace
// append constant instead of shifting the whole slice per entry.
const ktfHostTraceKeep = ktfHostTraceLimit / 2

// trace records one host trace entry, discarding the oldest entries once the
// retention window is full. hostTraceDropped keeps the reported total honest
// so a snapshot still distinguishes "nothing happened" from "the window
// rolled over".
func (r *ktfRuntime) trace(entry string) {
	if len(r.hostTrace) >= ktfHostTraceLimit {
		dropped := len(r.hostTrace) - ktfHostTraceKeep
		copy(r.hostTrace, r.hostTrace[dropped:])
		clear(r.hostTrace[ktfHostTraceKeep:])
		r.hostTrace = r.hostTrace[:ktfHostTraceKeep]
		r.hostTraceDropped += dropped
	}
	r.hostTrace = append(r.hostTrace, entry)
}

// tracef records one formatted host trace entry.
func (r *ktfRuntime) tracef(format string, args ...any) {
	r.trace(fmt.Sprintf(format, args...))
}
