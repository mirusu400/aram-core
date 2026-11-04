package raptor

import (
	"fmt"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
)

// The LGT Raptor AOT compiler does not inline Java's implicit runtime checks.
// It emits the compare, a branch over the failure path, and a call to a helper
// in module 100 that raises the exception. That helper never comes back on a
// handset: the instruction after the call is the guarded operation itself,
// reached only when the check passed.
//
//	cmp   r0, #0
//	bne   ok           ; reference is non-null, do the dereference
//	movs  r0, #0
//	ldr   r3, =throw   ; module 100 ordinal 34
//	bl    bx_r3        ; raises NullPointerException and does not return
//	ok:
//	ldr   r3, [r5]     ; the dereference the check guarded
//	ldr   r3, [r3, #0x3c]
//	bl    bx_r3        ; ... and the virtual call through it
//
// ARAM had no handler for these ordinals, so they fell through to the generic
// unimplemented-import path: it counted the call, named it "RAPTOR.module100#34"
// and resumed the guest with r0 = 0. Control then landed on exactly the
// operation the check had rejected. Reading a field out of the null reference
// is survivable - address 0 is mapped and reads as zero - but a virtual call
// through it loads a method address of 0 and branches there, and the machine
// reported a bare "ARM fetch at 0x00000000" that named nothing about the
// exception that had already been thrown and lost.
//
// 아빠와나 reaches this through Class.getResourceAsStream("/0i.dat"): its
// package holds 1i.dat through 7i.dat and no 0i.dat, so the lookup correctly
// answers null, the title's own null check calls ordinal 34, the swallowed
// throw returned, and the following virtual dispatch on the null reference
// branched to zero (issues #164/#195/#207/#215 - one title, filed four times
// because the reporting fingerprint kept the register dump).
//
// A Raptor Clet's catch handlers live in AOT tables ARAM does not decode and
// the helper is never told where they are, so ARAM cannot route the exception
// to the handler that may be waiting for it. What it can do is stop pretending
// the throw did not happen: the exception is recorded under its own name, and
// if the guest's undefined aftermath does fault, the invocation ends as an
// uncaught Java exception - the way a handset's dispatcher ends a task that
// lets one escape - instead of failing the machine on an address-zero fetch
// with no explanation.
//
// Ordinal 37, the divide-by-zero check, is deliberately absent from this map:
// it is the one member of the family whose call sites are followed by a branch
// to the division's merge point instead of by the guarded operation, so the
// code shape alone does not settle whether that helper returns.
var raptorJavaThrowClasses = map[uint32]string{
	34: "java/lang/NullPointerException",
	35: "java/lang/ArrayIndexOutOfBoundsException",
	38: "java/lang/ClassCastException",
}

// RaptorJavaThrowName is the label an undeliverable exception is recorded
// under in the observed and unimplemented API tables.
func RaptorJavaThrowName(class string) string {
	return "RAPTOR.Java.throw." + class
}

// recordRaptorJavaThrow notes an implicit-check exception the guest raised and
// ARAM cannot deliver. site is the guest address the throw helper was called
// from, which names the check that failed. The guest still resumes at the
// instruction after the call, because the alternative - ending the invocation
// here - also ends the frames of titles whose swallowed throw is harmless.
func (r *Runtime) recordRaptorJavaThrow(class string, site uint32) {
	r.LastJavaThrow = fmt.Sprintf("%s thrown at 0x%08x", class, site&^1)
	r.pendingJavaThrow = r.LastJavaThrow
	if r.Public == nil {
		return
	}
	name := RaptorJavaThrowName(class)
	r.Public.Stats.APICalls++
	r.Public.Stats.UnimplementedCalls++
	r.Public.Stats.LastAPI = name
	r.Public.Stats.LastUnimplemented = name
	if r.Public.Observed != nil {
		r.Public.Observed[name]++
	}
	if r.Public.Unimplemented != nil {
		r.Public.Unimplemented[name]++
	}
}

// BeginGuestSlice starts a new attribution window. Only a fault raised in the
// same slice as an undeliverable throw is attributed to it.
func (r *Runtime) BeginGuestSlice() {
	r.pendingJavaThrow = ""
	r.resetJavaSafepointSlice()
	r.clockPoll = clockPollState{}
}

// TakeUndeliveredJavaThrow reports the exception raised in the current slice
// that ARAM could not deliver, and clears it. A guest fault that follows one is
// the aftermath of the exception, not an independent failure.
func (r *Runtime) TakeUndeliveredJavaThrow() (string, bool) {
	description := r.pendingJavaThrow
	r.pendingJavaThrow = ""
	if description == "" {
		return "", false
	}
	return description, true
}

// AbsorbUncaughtJavaException applies a handset event dispatcher's policy to a
// Java exception that escaped a card's keyNotify. The dispatcher catches what
// the listener let escape, and the event is simply over. The title keeps
// running with whatever state the handler had already written.
//
// The AOT bridge cannot unwind a host Java exception into guest catch blocks
// (see above), so the exception reaches this point even when the title would
// have caught it inside the handler. Ending the event there is still closer
// to the handset than ending the session. SKVM absorbs exceptions at the same
// boundary.
//
// 월드장기체스 opens its 환경설정 box in two steps. One key switches the state.
// The box then animates open over a few paints and only afterwards posts its
// message. The box's key handler reads message 0 unchecked, so a key pressed
// while it opens indexes an empty Vector (issue #575).
//
// Only a host-raised exception is absorbed. The host raises it in place of
// returning, so no guest code has run past the throw. A guest implicit-check
// throw resumes the guest instead (see recordRaptorJavaThrow), and the fault
// that may follow is left alone. fault is the text the session would have
// ended with. It is kept so a caller polling UncaughtJavaExceptions reports
// the same failure.
func (r *Runtime) AbsorbUncaughtJavaException(
	callback wipirt.GuestCallback,
	err error,
	fault string,
) bool {
	class, ok := ktfrt.UnhandledJavaException(err)
	if !ok || !r.IsJavaInputCallback(callback) {
		return false
	}
	r.uncaughtJavaExceptions++
	r.lastUncaughtJavaException = fault
	if r.Public != nil && r.Public.Observed != nil {
		r.Public.Observed["RAPTOR.Java.uncaught."+class]++
	}
	return true
}

// UncaughtJavaExceptions reports how many Java exceptions escaped a card key
// callback and were absorbed, and the fault text of the last one.
func (r *Runtime) UncaughtJavaExceptions() (uint64, string) {
	if r == nil {
		return 0, ""
	}
	return r.uncaughtJavaExceptions, r.lastUncaughtJavaException
}
