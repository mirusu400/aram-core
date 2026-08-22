package ktf

import (
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
)

// ktfHostTraceLimit bounds the retained KTF host trace. DebugSnapshot never
// returns more than guest.MaxDebugSnapshotEntries, so an entry older than this
// window can no longer reach a caller; retaining it only cost resident memory
// and grew the slice the garbage collector rescans every cycle. A busy KTF
// title crosses fifty thousand host-bridge crossings per second, so the
// unbounded slice was the dominant allocation source in a long session.
const ktfHostTraceLimit = guest.MaxDebugSnapshotEntries

// ktfHostTraceKeep is the window preserved when the limit is reached. Dropping
// the oldest half in a single copy keeps the amortised cost of one trace
// append constant instead of shifting the whole slice per entry.
const ktfHostTraceKeep = ktfHostTraceLimit / 2

// HostTraceSampleInterval keeps high-frequency leaf graphics calls useful
// for diagnostics without formatting and retaining an entry for every pixel.
const HostTraceSampleInterval = 256

// trace records one host trace entry, discarding the oldest entries once the
// retention window is full. hostTraceDropped keeps the reported total honest
// so a snapshot still distinguishes "nothing happened" from "the window
// rolled over".
func (r *Runtime) trace(entry string) {
	if len(r.HostTrace) >= ktfHostTraceLimit {
		dropped := len(r.HostTrace) - ktfHostTraceKeep
		copy(r.HostTrace, r.HostTrace[dropped:])
		clear(r.HostTrace[ktfHostTraceKeep:])
		r.HostTrace = r.HostTrace[:ktfHostTraceKeep]
		r.HostTraceDropped += dropped
	}
	r.HostTrace = append(r.HostTrace, entry)
}

// tracef records one formatted host trace entry.
func (r *Runtime) tracef(format string, args ...any) {
	r.trace(fmt.Sprintf(format, args...))
}

// traceJavaMethodCall records the host-trace line for one host Java method
// invocation, which a Java-heavy title issues tens of thousands of times a
// second. It builds the same text tracef produced for
//
//	"java_method_call:%s.%s%s:lr=0x%08x:%08x"
//
// but appends it into a scratch buffer instead of going through fmt: the
// variadic call boxed five arguments, and formatting the register slice with
// %08x walked it reflectively. TestKTFJavaMethodTraceMatchesFmt pins the two
// against each other.
func (r *Runtime) traceJavaMethodCall(
	className, name, descriptor string,
	link uint32,
	registers []uint32,
) {
	buffer := append(r.traceScratch[:0], "java_method_call:"...)
	buffer = append(buffer, className...)
	buffer = append(buffer, '.')
	buffer = append(buffer, name...)
	buffer = append(buffer, descriptor...)
	buffer = append(buffer, ":lr=0x"...)
	buffer = appendTraceHex8(buffer, link)
	buffer = append(buffer, ":["...)
	for index, register := range registers {
		if index != 0 {
			buffer = append(buffer, ' ')
		}
		buffer = appendTraceHex8(buffer, register)
	}
	buffer = append(buffer, ']')
	r.traceScratch = buffer
	r.trace(string(buffer))
}

// appendTraceHex8 appends value as eight lowercase hex digits, the way fmt
// renders %08x for a uint32.
func appendTraceHex8(destination []byte, value uint32) []byte {
	const digits = "0123456789abcdef"
	for shift := 28; shift >= 0; shift -= 4 {
		destination = append(destination, digits[value>>uint(shift)&0xf])
	}
	return destination
}

// omitTrace accounts for an intentionally sampled-out entry. This preserves
// the total reported by DebugSnapshot while avoiding its string allocation.
func (r *Runtime) omitTrace() {
	r.HostTraceDropped++
}

// traceHostCall records a host bridge crossing and samples only leaf graphics
// calls that titles commonly invoke once per pixel. The first call is always
// retained, followed by one entry per interval.
func (r *Runtime) TraceHostCall(entry string) {
	r.HostCallCount++
	if !isKTFHighFrequencyHostTrace(entry) {
		r.trace(entry)
		return
	}
	if r.hostTraceSamples == nil {
		r.hostTraceSamples = make(map[string]uint64)
	}
	count := r.hostTraceSamples[entry] + 1
	r.hostTraceSamples[entry] = count
	if count == 1 || count%HostTraceSampleInterval == 0 {
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
		// Per-frame WIPI-C graphics calls a title issues thousands of times:
		// SetContext (2.6), FillRect (2.11), and the pixel/blit leaves
		// (2.8/2.22/2.23). Sampling them keeps a representative count without
		// letting the flood roll the retained trace over and evict the rare
		// calls (media, files, kernel) that actually matter for diagnostics.
		"wipic.2.6",
		"wipic.2.8",
		"wipic.2.11",
		"wipic.2.22",
		"wipic.2.23",
		// MC_mdaGetState (9.16): a title polls this in a tight loop while it
		// waits for an effect to finish, so sample it too.
		"wipic.9.16":
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
