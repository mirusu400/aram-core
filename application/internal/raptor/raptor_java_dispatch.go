package raptor

import (
	"fmt"

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
// The extra r1 argument can name an interface. In that form the generated code
// indexes a compact table in interface declaration order, not the receiver's
// ordinary vtable. Metadata-poor implementations still retain their bodies in
// guestVTable, so build a compact table from that inline own-method block and
// cache it per receiver/interface pair. Calls without a recognizable
// interface keep the ordinary receiver-table behavior required by issue #196.
// Returning the ordinary table here made View.run land on Object.equals and
// left issue #286's scene loop presenting an unchanged black buffer.
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
	interfaceToken, err := r.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return guest.WIPIReturn{}, name, true, err
	}
	java, err := r.ensureJavaRuntime()
	if err != nil {
		return guest.WIPIReturn{}, name, true, err
	}
	receiver := r.raptorJavaClassForObject(java, object)
	contract := r.raptorJavaClassForObject(java, interfaceToken)
	interfaceDispatch := contract != nil && len(contract.methods) != 0
	if interfaceDispatch {
		for _, method := range contract.methods {
			if method.Body != 0 {
				interfaceDispatch = false
				break
			}
		}
	}
	if receiver != nil && interfaceDispatch &&
		receiver.guestVTable != 0 {
		if java.interfaceVTables == nil {
			java.interfaceVTables = make(map[[2]uint32]uint32)
		}
		key := [2]uint32{receiver.Holder, contract.Holder}
		if table := java.interfaceVTables[key]; table != 0 {
			return guest.WIPIReturn{Low: table}, name, true, nil
		}
		words := uint32(len(contract.methods) + 1)
		table, allocateErr := r.Public.Heap.Allocate(words*4, true)
		if allocateErr != nil {
			return guest.WIPIReturn{}, name, true,
				fmt.Errorf("allocate Raptor Java interface dispatch table: %w", allocateErr)
		}
		if table == 0 {
			return guest.WIPIReturn{}, name, true,
				fmt.Errorf("allocate Raptor Java interface dispatch table returned null")
		}
		if err := r.Public.WriteU32(table, receiver.Holder); err != nil {
			return guest.WIPIReturn{}, name, true, err
		}
		for index, method := range contract.methods {
			body := uint32(0)
			for class, depth := receiver, 0; class != nil && depth < 256; depth++ {
				if declared, found := DeclaredMethod(class, method.Name, method.descriptor); found {
					body = declared.Body
					break
				}
				class = java.ClassByName[class.parentName]
			}
			if body == 0 {
				body, err = r.Public.ReadU32(receiver.guestVTable + 0x2c + uint32(index)*4)
				if err != nil {
					return guest.WIPIReturn{}, name, true, err
				}
			}
			if err := r.Public.WriteU32(table+4+uint32(index)*4, body); err != nil {
				return guest.WIPIReturn{}, name, true, err
			}
		}
		java.interfaceVTables[key] = table
		return guest.WIPIReturn{Low: table}, name, true, nil
	}
	table, err := r.Public.ReadU32(object)
	if err != nil {
		return guest.WIPIReturn{}, name, true, err
	}
	return guest.WIPIReturn{Low: table}, name, true, nil
}
