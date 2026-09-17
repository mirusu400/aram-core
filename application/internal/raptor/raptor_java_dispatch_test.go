package raptor

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestRaptorDispatchTableAnswersTheReceiversVTable pins module-100 ordinal 100.
//
// It used to fall through to the unimplemented-import path, which resumed the
// guest with r0 = 0. The guest indexes what comes back as a dispatch table
// (`table + offset*4 + 4`) and branches to the method it finds, so a zero
// answer made it load a method address of 0 from address 4 and branch there:
// SD한국전쟁 fails with "ARM fetch at 0x00000000" at frame 113 dispatching an
// org/kwis/msf/io/Socket method this way (issue #196).
func TestRaptorDispatchTableAnswersTheReceiversVTable(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	object, err := public.Heap.Allocate(12, true)
	if err != nil || object == 0 {
		t.Fatalf("allocate object = 0x%08x, %v", object, err)
	}
	const vtable = uint32(0x1000ff80)
	check(t, public.WriteU32(object, vtable))

	check(t, public.CPU.WriteRegister(cpu.RegisterR0, object))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 100},
	))
	got, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != vtable {
		t.Fatalf("module100#100(object) = 0x%08x, want the receiver's vtable 0x%08x",
			got, vtable)
	}
	if public.Unimplemented["RAPTOR.module100#100"] != 0 {
		t.Fatal("the dispatch-table helper fell through to the anonymous import path")
	}

	// The guest's own unresolved-method path calls the same helper with a null
	// receiver on purpose; that must not read address 0 as a table.
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, 0))
	check(t, runtime.dispatchImport(
		context.Background(),
		raptorImportKey{Module: 100, Ordinal: 100},
	))
	got, err = public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	if got != 0 {
		t.Fatalf("module100#100(null) = 0x%08x, want 0", got)
	}
}

func TestRaptorDispatchTableBuildsCompactInterfaceTable(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	guestVTable, err := public.Heap.Allocate(0x80, true)
	check(t, err)
	object, err := public.Heap.Allocate(12, true)
	check(t, err)

	receiver := &raptorJavaClass{
		Holder:      0x01400100,
		Name:        "app/Main",
		parentName:  "java/lang/Object",
		guestVTable: guestVTable,
		vtable:      0x1000ff80,
	}
	contract := &raptorJavaClass{
		Holder: 0x01400200,
		Name:   "app/View",
		methods: []raptorJavaDeclaredMethod{
			{Name: "free", descriptor: "()V"},
			{Name: "init", descriptor: "(I)V"},
			{Name: "draw", descriptor: "()V"},
			{Name: "run", descriptor: "()V"},
		},
	}
	java := &JavaRuntime{
		classes: map[uint32]*raptorJavaClass{
			receiver.Holder: receiver,
			contract.Holder: contract,
		},
		ClassByName: map[string]*raptorJavaClass{
			receiver.Name: receiver,
			contract.Name: contract,
		},
		interfaceVTables: make(map[[2]uint32]uint32),
	}
	runtime.Java = java
	check(t, public.WriteU32(object, receiver.vtable))
	check(t, public.WriteU32(object+4, receiver.Holder))
	for index, body := range []uint32{0x1001, 0x2001, 0x3001, 0x4001} {
		check(t, public.WriteU32(guestVTable+0x2c+uint32(index)*4, body))
	}
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, object))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, contract.Holder))

	result, _, handled, err := runtime.raptorJavaDispatchTable()
	check(t, err)
	if !handled || result.Low == 0 || result.Low == receiver.vtable {
		t.Fatalf("interface table = 0x%08x, handled=%t", result.Low, handled)
	}
	if got, err := public.ReadU32(result.Low + 0x10); err != nil || got != 0x4001 {
		t.Fatalf("View.run slot = 0x%08x, %v; want 0x00004001", got, err)
	}

	again, _, _, err := runtime.raptorJavaDispatchTable()
	check(t, err)
	if again.Low != result.Low {
		t.Fatalf("cached interface table = 0x%08x, want 0x%08x", again.Low, result.Low)
	}

	concrete := &raptorJavaClass{
		Holder: 0x01400300,
		Name:   "app/Concrete",
		methods: []raptorJavaDeclaredMethod{
			{Name: "run", descriptor: "()V", Body: 0x5001},
		},
	}
	java.classes[concrete.Holder] = concrete
	java.ClassByName[concrete.Name] = concrete
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, concrete.Holder))
	ordinary, _, _, err := runtime.raptorJavaDispatchTable()
	check(t, err)
	if ordinary.Low != receiver.vtable {
		t.Fatalf("concrete-class dispatch table = 0x%08x, want 0x%08x", ordinary.Low, receiver.vtable)
	}
}
