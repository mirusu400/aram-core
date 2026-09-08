package raptor

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// raptorJavaDispatchTable answers module-100 ordinal 100: given a receiver in
// r0, return the dispatch table the caller is about to index.
//
// The AOT compiler emits a direct vtable load for an ordinary virtual call -
// `ldr r3, [r3]` off the object, then `ldr r12, [r3, #slot]` - but routes one
// call shape through this helper first. SD한국전쟁 (issue #196) does
//
//	mov    r0, r4                 ; the receiver
//	bl     module 100 ordinal 100 ; -> the dispatch table
//	ldr    r1, =offsetTable
//	movs   r2, #4
//	ldrsh  r3, [r1, r2]           ; the published method offset
//	lsls   r3, r3, #2
//	adds   r3, r3, r0             ; table + offset*4
//	ldr    r6, [r3, #4]           ; ... + 4: the method
//	cmp    r6, #0
//	beq    unresolved
//	mov    r0, r4
//	bl     bx_r6
//
// `table + offset*4 + 4` with an offset read from the linker-filled halfword
// table is exactly the arithmetic linkRaptorJavaClasses publishes offsets for,
// and exactly what the direct `ldr r3, [r3]` path computes off object+0. So
// what the helper must return is the receiver's own dispatch table, which in
// ARAM's model is the word at object+0 that NewRaptorJavaObject writes.
//
// ARAM had no handler, so the generic unimplemented-import path resumed the
// guest with r0 = 0. The guest then indexed a table at address 0, loaded a
// null method, and - because the branch to its own unresolved-method helper
// (the same ordinal called with r0 = 0) came back instead of raising - ran
// `bx r6` with r6 = 0. That is the "ARM fetch at 0x00000000" in issue #196:
// SD한국전쟁 dispatches an org/kwis/msf/io/Socket method this way at frame 113.
//
// On a handset this is most likely interface dispatch, where the helper's
// extra arguments name the interface and the answer is that interface's slice
// of the receiver's method table. ARAM does not model interface method tables:
// it builds one flat vtable per class and publishes the flat offsets itself
// (see buildRaptorJavaVTable and raptorJavaFlatVirtualSlot), so the receiver's
// own table is the correct answer for every offset ARAM itself published. A
// title that indexes it with an offset the *guest* computed for a per-interface
// table would still land on the wrong slot; no corpus title was observed doing
// that, and the ordinal is rare - a 120-frame no-input scan of all 97 LGT
// titles found one other caller (턴, twice).
//
// With this, SD한국전쟁 reaches a later module-100 newArray call at frame 3030.
// That import has compact and extended ABIs whose count registers differ; its
// decoder lives in dispatchJavaImport.
//
// A null receiver returns zero rather than reading address 0: the guest's own
// unresolved-method path calls this helper with r0 = 0 on purpose. A receiver
// that is non-null but unreadable is reported, the same way the guest's direct
// `ldr r3, [r3]` off the object would fault.
func (r *Runtime) raptorJavaDispatchTable() (guest.WIPIReturn, string, bool, error) {
	const name = "RAPTOR.java.dispatchTable"
	object, err := r.CPU.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return guest.WIPIReturn{}, name, true, err
	}
	if object == 0 {
		return guest.WIPIReturn{}, name, true, nil
	}
	table, err := r.Public.ReadU32(object)
	if err != nil {
		return guest.WIPIReturn{}, name, true, err
	}
	return guest.WIPIReturn{Low: table}, name, true, nil
}
