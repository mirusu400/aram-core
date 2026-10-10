package raptor

import (
	"fmt"
	"image"
	"sort"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

const maxRaptorJavaStateMetadata = 64 << 20

type javaDeclaredMethodState struct {
	Name, Descriptor string
	Body             uint32
	Flags            uint16
}

type javaDeclaredFieldState struct {
	Name, Descriptor string
	Index            uint32
}

type javaClassState struct {
	Holder, Descriptor, FieldSize, StaticBase, VTable, GuestVTable uint32
	HostClass, ClassObject                                         uint32
	Name, ParentName                                               string
	Methods                                                        []javaDeclaredMethodState
	Fields                                                         []javaDeclaredFieldState
}

type javaMethodState struct {
	ClassName, Name, Descriptor string
	Static                      bool
}

type javaInterfaceVTableState struct {
	Receiver, Interface, Table uint32
}

type javaTaskState struct {
	Target, Procedure, Stack uint32
	Context                  []byte
	Done                     bool
	WakeAtMS                 uint64
}

type javaState struct {
	Classes          []javaClassState
	ClassNames       map[string]uint32
	ClassOrder       []uint32
	HostMethods      map[uint32]javaMethodState
	NextMethod       uint32
	PrimitiveArrays  map[uint32]uint32
	NoopStub         uint32
	ArrayVTable      uint32
	InterfaceVTables []javaInterfaceVTableState
	FlatVirtual      []javaMethodState
	LGTToKTF         map[uint32]uint32
	KTFToLGT         map[uint32]uint32
	Scratch, JARPath uint32
	FieldOffsets     uint32
	FieldNames       uint32
	FieldCount       uint32
	LaunchRequested  bool
	MainClass        string
	MainInstance     uint32
	CurrentCard      uint32
	DirtyCards       map[uint32]bool
	DirtyCardRegions map[uint32]javaPaintRegionState
	ThreadTargets    []uint32
	Tasks            []javaTaskState
	NextTask         uint32
	RealHeapBases    []uint32
}

type javaPaintRegionState struct {
	MinX, MinY, MaxX, MaxY int32
}

func javaMethodToState(method raptorJavaMethod) javaMethodState {
	return javaMethodState{method.className, method.Name, method.descriptor, method.isStatic}
}

func (method javaMethodState) runtimeMethod() raptorJavaMethod {
	return raptorJavaMethod{className: method.ClassName, Name: method.Name,
		descriptor: method.Descriptor, isStatic: method.Static}
}

func captureJavaState(java *JavaRuntime) (javaState, error) {
	if java == nil || java.Host == nil || java.Host.Services == nil ||
		java.privateMedia == nil || java.activeTask != nil || java.callSerially != 0 ||
		len(java.initializing) != 0 || len(java.constructing) != 0 ||
		len(java.parentInspection) != 0 || len(java.vtableBuilding) != 0 {
		return javaState{}, fmt.Errorf("save Raptor Java state while a host continuation is active")
	}
	state := javaState{
		ClassNames:      make(map[string]uint32, len(java.ClassByName)),
		HostMethods:     make(map[uint32]javaMethodState, len(java.hostMethods)),
		NextMethod:      java.nextMethod,
		PrimitiveArrays: guest.CloneMap(java.primitiveArrays),
		NoopStub:        java.noopStub, ArrayVTable: java.arrayVTable,
		LGTToKTF: guest.CloneMap(java.lgtToKTF), KTFToLGT: guest.CloneMap(java.ktfToLGT),
		Scratch: java.scratch, JARPath: java.jarPath,
		FieldOffsets: java.fieldOffsets, FieldNames: java.fieldNames, FieldCount: java.fieldCount,
		LaunchRequested: java.LaunchRequested, MainClass: java.MainClass,
		MainInstance: java.MainInstance, CurrentCard: java.currentCard,
		DirtyCards:       guest.CloneMap(java.dirtyCards),
		DirtyCardRegions: make(map[uint32]javaPaintRegionState, len(java.dirtyCardRegions)),
		ThreadTargets:    append([]uint32(nil), java.threadTargets...),
		NextTask:         uint32(java.nextTask), RealHeapBases: java.Host.EmbeddedHeapBases(),
	}
	for card, region := range java.dirtyCardRegions {
		state.DirtyCardRegions[card] = javaPaintRegionState{int32(region.Min.X), int32(region.Min.Y), int32(region.Max.X), int32(region.Max.Y)}
	}
	for name, class := range java.ClassByName {
		if class != nil {
			state.ClassNames[name] = class.Holder
		}
	}
	for _, class := range java.classes {
		current := javaClassState{
			Holder: class.Holder, Descriptor: class.descriptor, Name: class.Name,
			ParentName: class.parentName, FieldSize: class.fieldSize,
			StaticBase: class.staticBase, VTable: class.vtable,
			GuestVTable: class.guestVTable, HostClass: class.hostClass,
			ClassObject: class.classObject,
		}
		for _, method := range class.methods {
			current.Methods = append(current.Methods, javaDeclaredMethodState{
				method.Name, method.descriptor, method.Body, method.flags})
		}
		for _, field := range class.fields {
			current.Fields = append(current.Fields, javaDeclaredFieldState{
				field.Name, field.descriptor, field.index})
		}
		state.Classes = append(state.Classes, current)
	}
	sort.Slice(state.Classes, func(i, j int) bool { return state.Classes[i].Holder < state.Classes[j].Holder })
	for _, class := range java.classOrder {
		if class != nil {
			state.ClassOrder = append(state.ClassOrder, class.Holder)
		}
	}
	for ordinal, method := range java.hostMethods {
		state.HostMethods[ordinal] = javaMethodToState(method)
	}
	for key, table := range java.interfaceVTables {
		state.InterfaceVTables = append(state.InterfaceVTables, javaInterfaceVTableState{key[0], key[1], table})
	}
	sort.Slice(state.InterfaceVTables, func(i, j int) bool {
		left, right := state.InterfaceVTables[i], state.InterfaceVTables[j]
		if left.Receiver != right.Receiver {
			return left.Receiver < right.Receiver
		}
		return left.Interface < right.Interface
	})
	for _, method := range java.flatVirtual {
		state.FlatVirtual = append(state.FlatVirtual, javaMethodToState(method))
	}
	for _, task := range java.Tasks {
		if task == nil || len(task.Context) > guest.MaxStateContext {
			return javaState{}, fmt.Errorf("save Raptor Java state: invalid task")
		}
		state.Tasks = append(state.Tasks, javaTaskState{
			Target: task.Target, Procedure: task.Procedure, Stack: task.Stack,
			Context: append([]byte(nil), task.Context...), Done: task.Done,
			WakeAtMS: task.WakeAtMS,
		})
	}
	return state, nil
}

// ValidateJavaState checks that the embedded adapter can be captured before
// the application writes any save-state bytes to its caller.
func ValidateJavaState(runtime *Runtime) error {
	if runtime == nil || runtime.Java == nil {
		return nil
	}
	_, err := captureJavaState(runtime.Java)
	return err
}

func (state javaState) validate() error {
	if len(state.Classes) > 4096 || len(state.ClassOrder) > 4096 ||
		len(state.HostMethods) > 16384 || len(state.Tasks) > 1024 ||
		len(state.LGTToKTF) > 1<<20 || len(state.KTFToLGT) > 1<<20 ||
		int(state.NextTask) > max(1, len(state.Tasks)) {
		return fmt.Errorf("Raptor Java state exceeds table limits")
	}
	seen := make(map[uint32]bool, len(state.Classes))
	for _, class := range state.Classes {
		if class.Holder == 0 || seen[class.Holder] {
			return fmt.Errorf("duplicate Raptor Java class holder")
		}
		seen[class.Holder] = true
	}
	for _, holder := range state.ClassOrder {
		if !seen[holder] {
			return fmt.Errorf("Raptor Java class order names unknown holder")
		}
	}
	for _, holder := range state.ClassNames {
		if !seen[holder] {
			return fmt.Errorf("Raptor Java class name names unknown holder")
		}
	}
	for _, task := range state.Tasks {
		if task.Procedure == 0 || len(task.Context) > guest.MaxStateContext {
			return fmt.Errorf("invalid Raptor Java task")
		}
	}
	return nil
}

func restoreJavaState(java *JavaRuntime, state javaState) error {
	if err := state.validate(); err != nil {
		return err
	}
	java.classes = make(map[uint32]*raptorJavaClass, len(state.Classes))
	for _, saved := range state.Classes {
		class := &raptorJavaClass{
			Holder: saved.Holder, descriptor: saved.Descriptor, Name: saved.Name,
			parentName: saved.ParentName, fieldSize: saved.FieldSize,
			staticBase: saved.StaticBase, vtable: saved.VTable,
			guestVTable: saved.GuestVTable, hostClass: saved.HostClass,
			classObject: saved.ClassObject,
		}
		for _, method := range saved.Methods {
			class.methods = append(class.methods, raptorJavaDeclaredMethod{
				Name: method.Name, descriptor: method.Descriptor, Body: method.Body, flags: method.Flags})
		}
		for _, field := range saved.Fields {
			class.fields = append(class.fields, raptorJavaDeclaredField{
				Name: field.Name, descriptor: field.Descriptor, index: field.Index})
		}
		java.classes[class.Holder] = class
	}
	java.ClassByName = make(map[string]*raptorJavaClass, len(state.ClassNames))
	for name, holder := range state.ClassNames {
		java.ClassByName[name] = java.classes[holder]
	}
	java.classOrder = make([]*raptorJavaClass, 0, len(state.ClassOrder))
	for _, holder := range state.ClassOrder {
		java.classOrder = append(java.classOrder, java.classes[holder])
	}
	java.hostMethods = make(map[uint32]raptorJavaMethod, len(state.HostMethods))
	for ordinal, method := range state.HostMethods {
		java.hostMethods[ordinal] = method.runtimeMethod()
	}
	java.nextMethod = state.NextMethod
	java.primitiveArrays = guest.CloneMap(state.PrimitiveArrays)
	java.noopStub, java.arrayVTable = state.NoopStub, state.ArrayVTable
	java.interfaceVTables = make(map[[2]uint32]uint32, len(state.InterfaceVTables))
	for _, entry := range state.InterfaceVTables {
		java.interfaceVTables[[2]uint32{entry.Receiver, entry.Interface}] = entry.Table
	}
	java.flatVirtual = make([]raptorJavaMethod, 0, len(state.FlatVirtual))
	for _, method := range state.FlatVirtual {
		java.flatVirtual = append(java.flatVirtual, method.runtimeMethod())
	}
	// The KTF collector registered these two maps as weak tables at launch.
	// Keep their identities while replacing their contents.
	clear(java.lgtToKTF)
	for address, mirror := range state.LGTToKTF {
		java.lgtToKTF[address] = mirror
	}
	clear(java.ktfToLGT)
	for mirror, address := range state.KTFToLGT {
		java.ktfToLGT[mirror] = address
	}
	java.scratch, java.jarPath = state.Scratch, state.JARPath
	java.fieldOffsets, java.fieldNames, java.fieldCount = state.FieldOffsets, state.FieldNames, state.FieldCount
	java.LaunchRequested, java.MainClass = state.LaunchRequested, state.MainClass
	java.MainInstance, java.currentCard = state.MainInstance, state.CurrentCard
	java.dirtyCards = guest.CloneMap(state.DirtyCards)
	java.dirtyCardRegions = make(map[uint32]image.Rectangle, len(state.DirtyCardRegions))
	for card, region := range state.DirtyCardRegions {
		java.dirtyCardRegions[card] = image.Rect(int(region.MinX), int(region.MinY), int(region.MaxX), int(region.MaxY))
	}
	java.threadTargets = append([]uint32(nil), state.ThreadTargets...)
	java.Tasks = make([]*JavaTask, 0, len(state.Tasks))
	for _, saved := range state.Tasks {
		java.Tasks = append(java.Tasks, &JavaTask{
			Target: saved.Target, Procedure: saved.Procedure, Stack: saved.Stack,
			Context: append([]byte(nil), saved.Context...), Done: saved.Done,
			WakeAtMS: saved.WakeAtMS,
		})
	}
	java.nextTask = int(state.NextTask)
	java.parentInspection = make(map[uint32]bool)
	java.vtableBuilding = make(map[uint32]bool)
	java.initializing = make(map[uint32]bool)
	java.constructing = make(map[uint32]bool)
	java.activeTask = nil
	java.callSerially = 0
	java.syncScratch = nil
	return java.Host.PruneEmbeddedJavaHeaps(state.RealHeapBases)
}

func marshalJavaState(java *JavaRuntime) ([]byte, error) {
	state, err := captureJavaState(java)
	if err != nil {
		return nil, err
	}
	encoded, err := shared.MarshalStateComponent(state)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxRaptorJavaStateMetadata {
		return nil, fmt.Errorf("Raptor Java metadata exceeds limit")
	}
	return encoded, nil
}

func unmarshalJavaState(data []byte) (javaState, error) {
	var state javaState
	if len(data) > maxRaptorJavaStateMetadata {
		return state, fmt.Errorf("Raptor Java metadata exceeds limit")
	}
	if err := shared.UnmarshalStateComponent(data, &state); err != nil {
		return state, err
	}
	return state, state.validate()
}
