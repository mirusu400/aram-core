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

// ktfHostTraceSampleInterval keeps high-frequency leaf graphics calls useful
// for diagnostics without formatting and retaining an entry for every pixel.
const ktfHostTraceSampleInterval = 256

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

// omitTrace accounts for an intentionally sampled-out entry. This preserves
// the total reported by DebugSnapshot while avoiding its string allocation.
func (r *ktfRuntime) omitTrace() {
	r.hostTraceDropped++
}

// traceHostCall records a host bridge crossing and samples only leaf graphics
// calls that titles commonly invoke once per pixel. The first call is always
// retained, followed by one entry per interval.
func (r *ktfRuntime) traceHostCall(entry string) {
	r.hostCallCount++
	if !isKTFHighFrequencyHostTrace(entry) {
		r.trace(entry)
		return
	}
	if r.hostTraceSamples == nil {
		r.hostTraceSamples = make(map[string]uint64)
	}
	count := r.hostTraceSamples[entry] + 1
	r.hostTraceSamples[entry] = count
	if count == 1 || count%ktfHostTraceSampleInterval == 0 {
		r.trace(entry)
		return
	}
	r.omitTrace()
}

func isKTFHighFrequencyHostTrace(entry string) bool {
	switch entry {
	case "java.method.org/kwis/msp/lcdui/Graphics.setPixel(II)V",
		"java.method.org/kwis/msp/lcdui/Graphics.setRGBPixels(IIII[III)V",
		"java.native_override.org/kwis/msp/lcdui/Graphics.setPixel(II)V",
		"java.native_override.org/kwis/msp/lcdui/Graphics.setRGBPixels(IIII[III)V",
		"wipic.2.8",
		"wipic.2.22",
		"wipic.2.23":
		return true
	default:
		return false
	}
}

func isKTFHighFrequencyJavaMethod(className, name, descriptor string) bool {
	if className != "org/kwis/msp/lcdui/Graphics" {
		return false
	}
	switch name {
	case "setPixel":
		return descriptor == "(II)V"
	case "setRGBPixels":
		return descriptor == "(IIII[III)V"
	default:
		return false
	}
}
