package raptor

import "github.com/mirusu400/aram-core/application/internal/guest"

// Module-100 ordinals 86 and 87 are the AOT runtime's paired object-monitor
// helpers - the code a Raptor Clet's `synchronized` region compiles to. Each
// takes exactly one argument, the object, in r0, and returns nothing.
//
// ARAM used to read them as a getfield/putfield accessor pair with the ABI
// "r0 = object, r1 = value, r2 = byte offset into the field block", bounded
// only by an offset limit of 0x10000. That reading was a guess made while
// booting 놈3 (fb565ee) and it is wrong: **the call sites set only r0**, so
// r1 and r2 are whatever the caller happened to leave in them, and ordinal 87
// wrote that leftover value at that leftover offset into a live object.
//
// 훼밀리마트타이쿤 (issue #211) shows the whole chain. The AOT code around a
// synchronized method is
//
//	0x570c0  mov   r12, sp                 ; prologue
//	         stmdb sp!, {r4-r6, r11, r12, lr}
//	0x570d4  str   r0, [r11, #-0x1c]       ; this
//	         ... two helper calls that set up an exception frame ...
//	0x5710c  subs  r5, r0, #0
//	0x57110  beq   0x57130
//	0x57114  ldr   r0, [r11, #-0x1c]       ; the unwind path
//	         mov   lr, pc
//	         bx    r3                      ; -> ordinal 87
//	         ...                           ; rethrow
//	0x57130  ldr   r0, [r11, #-0x24]       ; the normal path
//	         mov   lr, pc
//	         bx    r3                      ; -> ordinal 86
//	         ...                           ; body
//	0x57234  ldr   r0, [r11, #-0x1c]
//	         mov   lr, pc
//	         bx    r3                      ; -> ordinal 87
//	0x57244  sub   sp, r11, #0x18          ; epilogue
//	         ldm   sp, {r4-r6, r11, sp, lr}
//	         bx    lr
//
// Ordinal 86 is entered once on the way in, ordinal 87 on both the normal exit
// and the unwind path, and at every one of those call sites the only register
// loaded is r0 and the result is thrown away.
//
// In one seed-75 run ordinal 87 was reached six times with r2 left holding
// 0x00, 0x08, 0x23, 0x2a and 0x34 - two of those are not even multiples of
// four - and r1 holding 0. The handler wrote a zero word at each of them
// inside one Card subclass's 34-word field block, which
//
//   - nulled the java/util/Stack reference at slot 13 (offset 0x34),
//   - cleared the two boolean slots at offsets 0x00 and 0x08, and
//   - at the unaligned offsets 0x23 and 0x2a sliced the top bytes off two live
//     object references: 0x100372b8 became 0x000372b8 and 0x10039238 became
//     0x00009238.
//
// The guest then did what a null check permits with a non-null reference -
// `ldrne r3, [r3]; ldrne r12, [r3, #0x48]; bxne r12` - and dispatched through
// a word of its own .text. That is the "invalid guest address: 0xe58381ac" and
// "ARM fetch at 0x00000000" this family of reports keeps showing: a code word
// used as a pointer, because a host helper had cut a pointer in half.
//
// A 120-frame no-input scan of all 97 LGT corpus titles finds these two
// ordinals in six of them, always in matched pairs with the same object
// (SD한국전쟁 9/9, 간호사타이쿤2 63/62, 붕어빵타이쿤3 6/6, 스파이더맨3 47/46,
// 메이플스토리2007 1/1, 월드장기체스 1/1),
// and r2 is a heap pointer or zero in every one of them - never a field offset.
// The pairing, the single argument, the discarded result, and the placement
// inside the exception frame are what identify the pair as monitorenter and
// monitorexit.
//
// What is implemented here is the calling convention, not the lock. Two things
// are deliberately left undone rather than guessed at, and both are separate
// work from the memory corruption above:
//
//   - The lock itself. ARAM does not model Raptor object monitors: a Java task
//     can be descheduled at a slice boundary inside a synchronized region and
//     another task may then enter it. Closing that needs
//     NextRunnableJavaTask to know a task is blocked on an object and a way to
//     re-run the blocked monitorenter call.
//   - monitorenter on null, which is a NullPointerException on a handset. No
//     corpus title was observed doing it, and ARAM cannot deliver a Raptor
//     exception to its handler anyway (see raptor_java_throw.go), so nothing
//     is invented here.
//
// The helper still names each call, so a title that depends on real monitor
// semantics shows up in the observed-API table instead of disappearing into an
// anonymous unimplemented import.
func (r *Runtime) raptorJavaMonitorHelper(
	ordinal uint32,
) (guest.WIPIReturn, string, bool, error) {
	name := "RAPTOR.java.monitorEnter"
	if ordinal == 87 {
		name = "RAPTOR.java.monitorExit"
	}
	return guest.WIPIReturn{}, name, true, nil
}
