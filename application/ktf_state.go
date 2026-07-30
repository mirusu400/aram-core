package application

import (
	"fmt"
	"image"
	"image/color"
	"reflect"
	"sort"
	"strconv"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ktfStateSchema       = uint32(2)
	maxKTFStateMetadata  = uint32(64 << 20)
	maxKTFStateEntries   = 16_384
	maxKTFStateHostCalls = int(ktfHostSize / 4)
)

type ktfSavedState struct {
	owner             shared.OwnerID
	name              string
	services          *shared.Services
	hostMemory        []byte
	heapMemory        []byte
	heapAllocations   []heapBlock
	incrementalHeaps  []ktfIncrementalHeapSnapshot
	metadata          ktfMetadataSnapshot
	resolvedHostCalls map[uint32]ktfHostCall
}

type ktfPersistentState struct {
	owner          shared.OwnerID
	storage        shared.StoragePersistenceState
	fileData       map[string][]byte
	databaseStores map[string]*ktfDatabase
}

func (r *ktfRuntime) capturePersistentState() (ktfPersistentState, error) {
	if r == nil || r.services == nil {
		return ktfPersistentState{}, fmt.Errorf("KTF services are missing")
	}
	state := ktfPersistentState{
		owner:          r.serviceOwner,
		storage:        r.services.Storage.ExportPersistence(),
		fileData:       make(map[string][]byte),
		databaseStores: make(map[string]*ktfDatabase),
	}
	for _, file := range state.storage.Files {
		if file.Namespace == shared.NamespacePrivate {
			state.fileData[file.Path] = append([]byte(nil), file.Data...)
		}
	}
	if len(state.storage.RecordStores) != len(r.databaseStores) {
		return ktfPersistentState{}, fmt.Errorf(
			"KTF database persistence count differs",
		)
	}
	for _, saved := range state.storage.RecordStores {
		database := r.databaseStores[saved.Name]
		if saved.Owner != state.owner || database == nil ||
			database.name != saved.Name {
			return ktfPersistentState{}, fmt.Errorf(
				"KTF database %q has invalid persistent metadata",
				saved.Name,
			)
		}
		expectedNext := uint32(len(saved.Records))
		if expectedNext == 0 {
			expectedNext = 1
		}
		if saved.NextID != expectedNext {
			return ktfPersistentState{}, fmt.Errorf(
				"KTF database %q has non-contiguous persistent records",
				saved.Name,
			)
		}
		clone := &ktfDatabase{
			name:       saved.Name,
			recordSize: database.recordSize,
			records:    make([][]byte, len(saved.Records)),
		}
		for index, record := range saved.Records {
			if record.ID != uint32(index) {
				return ktfPersistentState{}, fmt.Errorf(
					"KTF database %q has non-contiguous record %d",
					saved.Name,
					record.ID,
				)
			}
			clone.records[index] = append([]byte(nil), record.Data...)
		}
		state.databaseStores[saved.Name] = clone
	}
	return state, nil
}

func (r *ktfRuntime) restorePersistentState(state ktfPersistentState) error {
	if r == nil || r.services == nil {
		return fmt.Errorf("KTF services are missing")
	}
	for index := range state.storage.RecordStores {
		if state.storage.RecordStores[index].Owner != state.owner {
			return fmt.Errorf(
				"KTF record store %q belongs to owner %d, want %d",
				state.storage.RecordStores[index].Name,
				state.storage.RecordStores[index].Owner,
				state.owner,
			)
		}
		state.storage.RecordStores[index].Owner = r.serviceOwner
	}
	if err := r.services.Storage.ImportPersistence(state.storage); err != nil {
		return fmt.Errorf("restore KTF persistence: %w", err)
	}
	r.fileData = cloneByteMap(state.fileData)
	r.databaseStores = make(
		map[string]*ktfDatabase,
		len(state.databaseStores),
	)
	r.databaseServices = make(
		map[string]shared.ServiceID,
		len(state.databaseStores),
	)
	for _, name := range sortedStringKeys(state.databaseStores) {
		database := state.databaseStores[name]
		r.databaseStores[name] = &ktfDatabase{
			name:       database.name,
			recordSize: database.recordSize,
			records:    cloneByteSlices(database.records),
		}
		serviceID, err := r.services.Storage.OpenRecordStore(
			r.serviceOwner,
			name,
		)
		if err != nil {
			return fmt.Errorf(
				"reopen KTF database %q after reset: %w",
				name,
				err,
			)
		}
		r.databaseServices[name] = serviceID
	}
	return nil
}

type ktfExecutableSnapshot struct {
	WipiExeAddress, ExeInterfaceAddress, FunctionsAddress uint32
	Name                                                  string
	ExecutableInit, InterfaceInit, GetDefaultDLL          uint32
	GetClass, InterfaceUnknown2, InterfaceUnknown3        uint32
}

type ktfHostCallSnapshot struct {
	Address uint32
	Name    string
}

type ktfIncrementalHeapSnapshot struct {
	Base        uint32
	Size        uint32
	Allocations []heapBlockSnapshot
}

type ktfIncrementalMemoryRegionSnapshot struct {
	Base uint32
	Size uint32
}

type heapBlockSnapshot struct {
	Address uint32
	Size    uint32
}

type ktfHashtableEntrySnapshot struct {
	Key   uint32
	Value uint32
}

type ktfEnumerationSnapshot struct {
	Values []uint32
	Index  uint32
}

type ktfClipSnapshot struct {
	Volume   int32
	Listener uint32
	Playing  bool
	Data     []byte
}

type ktfLWCSnapshot struct {
	X, Y, Width, Height                  int32
	PreferredWidth, PreferredHeight      int32
	Background, Foreground, Parent, Card uint32
	Title, Command, Work, Focus, Text    uint32
	Gap                                  int32
	Shown, Valid, Focused                bool
	Vertical, Packed                     bool
}

type ktfDatabaseSnapshot struct {
	Name       string
	RecordSize uint32
	Records    [][]byte
}

type ktfInputStreamSnapshot struct {
	Data     []byte
	Position uint32
}

type ktfFileSnapshot struct {
	Name     string
	Position uint32
	Mode     uint32
	Closed   bool
}

type ktfGraphicsSnapshot struct {
	Target    uint32
	Screen    bool
	Clip      [4]int32
	Color     [4]uint8
	Translate [2]int32
}

type ktfWIPICFramebufferSnapshot struct {
	Object, Body, PixelObject, PixelHeader, Pixels uint32
	Width, Height, Stride, Bits                    int32
	Screen                                         bool
}

type ktfWIPICImageSnapshot struct {
	Object, Body, Framebuffer, Source uint32
}

type ktfWIPICMemorySnapshot struct {
	Base, Data, Size uint32
}

type ktfWIPICTimerSnapshot struct {
	Callback, Parameter uint32
	Deadline            uint64
	Active              bool
}

type ktfTaskSnapshot struct {
	Context                     []byte
	ExceptionFrame              uint32
	LastJavaMethod              string
	Done, PresentOnReturn       bool
	BestEffortPaint, WIPICTimer bool
	PaintCard, KeyCard          uint32
	LayoutOnReturn              uint32
	StartBlocker                int32
	ChildStartGrace             uint64
}

type ktfPendingJavaCallSnapshot struct {
	Instance   uint32
	Name       string
	Descriptor string
	Args       []uint32
}

type ktfTaskMapSnapshot struct {
	Key  uint32
	Task int32
}

type ktfDeferredCardsSnapshot struct {
	Task  int32
	Cards []uint32
}

type ktfDeferredShownSnapshot struct {
	Task  int32
	Cards []uint32
}

// ktfMetadataSnapshot contains guest-neutral encodings of adapter-owned
// handles and continuation metadata. Large mapped memories and the shared
// service container are encoded separately so they remain fixed-width binary
// fields instead of expanding in the metadata representation.
type ktfMetadataSnapshot struct {
	Started bool
	Exe     ktfExecutableSnapshot

	NextHostCall uint32
	HostCalls    []ktfHostCallSnapshot

	KnlInterface, JBInterface, WIPICInterface uint32
	MXUserMemInterface                        uint32
	IncrementalMemory                         []ktfIncrementalMemoryRegionSnapshot

	ImageServices, JavaAssetServices         map[uint32]shared.ServiceID
	FontServices, GraphicsServices           map[uint32]shared.ServiceID
	WIPICSurfaceServices, WIPICAssetServices map[uint32]shared.ServiceID
	WIPICTimerServices, ClipServices         map[uint32]shared.ServiceID
	DatabaseServices                         map[string]shared.ServiceID
	FileServices, WIPICFileServices          map[uint32]shared.ServiceID

	JavaClasses          map[string]uint32
	JavaStrings          map[uint32]string
	JavaClassObjs        map[uint32]uint32
	ClassObjTarget       map[uint32]uint32
	HostJavaClass        map[uint32]bool
	JavaClassInit        map[uint32]uint8
	JVMContext           uint32
	ExceptionContext     uint32
	JavaEnvironment      uint32
	JavaVTables          map[uint32]uint32
	JavaVTableCapacity   map[uint32]uint32
	JavaVTableClasses    map[uint32]uint32
	HostJavaVirtualSlots map[uint32]uint16
	NextHostVirtualSlot  uint16

	LastJavaMethod          string
	LastJavaReturn          uint32
	LastJavaJump            uint32
	LastJavaCallLR          uint32
	FirstJavaThrowName      string
	FirstJavaThrowRegisters []uint32
	FirstJavaThrowSP        uint32
	FirstJavaThrowStack     []uint32
	LastJavaThrowName       string
	LastJavaThrowRegisters  []uint32
	LastJavaThrowSP         uint32
	LastJavaThrowStack      []uint32
	JavaReturnHigh          uint32
	JavaExceptionFrames     []string
	UnimplementedJava       map[string]uint64
	LastUnimplementedJava   string

	RandomSeeds       map[uint32]uint64
	IntegerValues     map[uint32]int32
	LongValues        map[uint32]int64
	ThrowableMessages map[uint32]uint32
	Dates             map[uint32]int64
	Vectors           map[uint32][]uint32
	Hashtables        map[uint32]map[string]ktfHashtableEntrySnapshot
	Enumerations      map[uint32]ktfEnumerationSnapshot
	Clips             map[uint32]ktfClipSnapshot
	Listeners         map[uint32]uint32
	LWCEventData      map[uint32]uint32
	LWCChildren       map[uint32][]uint32
	LWCMaxLengths     map[uint32]int32
	LWCComponents     map[uint32]ktfLWCSnapshot
	Databases         map[uint32]string
	DatabaseStores    map[string]ktfDatabaseSnapshot
	DefaultRuntime    uint32
	DefaultDisplay    uint32
	DisplayCards      map[uint32]uint32
	ThreadTargets     map[uint32]uint32
	CurrentThread     uint32
	StringBuffers     map[uint32]string
	InputStreams      map[uint32]ktfInputStreamSnapshot
	InputTargets      map[uint32]uint32
	OutputStreams     map[uint32][]byte
	OutputTargets     map[uint32]uint32
	Files             map[uint32]ktfFileSnapshot
	FileData          map[string][]byte
	FileStreamTargets map[uint32]uint32
	SystemInputStream uint32
	SystemPrintStream uint32

	Images                 []uint32
	DefaultFont            uint32
	Graphics               map[uint32]ktfGraphicsSnapshot
	ScreenGraphics         uint32
	WIPICFramebuffers      map[uint32]ktfWIPICFramebufferSnapshot
	WIPICScreenFramebuffer uint32
	WIPICImages            map[uint32]ktfWIPICImageSnapshot
	WIPICResources         map[uint32][]byte
	WIPICResourceIDs       map[string]uint32
	WIPICMemory            map[uint32]ktfWIPICMemorySnapshot
	WIPICTimers            map[uint32]ktfWIPICTimerSnapshot
	WIPICSystemProperties  map[string]string
	WIPICFiles             map[uint32]ktfFileSnapshot
	NextWIPICFile          uint32

	DirtyCards            map[uint32]bool
	PaintInitializedCards map[uint32]bool
	PaintTasks            []ktfTaskMapSnapshot
	DeferredPaintCards    []ktfDeferredCardsSnapshot
	DeferredShownCards    []ktfDeferredShownSnapshot
	PresentCount          uint32
	TickMS                uint64

	NativeParameterBase uint32
	DeferThreads        bool
	YieldRequested      bool
	Tasks               []ktfTaskSnapshot
	PendingJavaCalls    []ktfPendingJavaCallSnapshot
	TaskCursor          int32
	ActiveTask          int32
	ActiveInstructions  uint64
	ExecutionDepth      int32
}

func (m *Machine) writeKTFState(writer *stateWriter) error {
	if m.ktf == nil {
		writer.u8(0)
		writer.write([]byte{0, 0, 0})
		return nil
	}
	r := m.ktf
	if r.executionDepth != 0 || r.activeTask != nil {
		return fmt.Errorf("save KTF state while an adapter continuation is active")
	}
	for _, instance := range sortedUint32Keys(r.graphics) {
		if err := r.syncKTFGraphics(instance); err != nil {
			return fmt.Errorf(
				"sync KTF graphics 0x%08x before save: %w",
				instance,
				err,
			)
		}
	}
	serviceState, err := r.services.MarshalBinary()
	if err != nil {
		return fmt.Errorf("save KTF shared services: %w", err)
	}
	metadata, err := snapshotKTFMetadata(r, m.ktfStarted)
	if err != nil {
		return err
	}
	incremental := sortedKTFIncrementalHeaps(r)
	if err := validateKTFIncrementalMemory(
		metadata.IncrementalMemory,
		incremental,
	); err != nil {
		return fmt.Errorf("validate KTF incremental memory: %w", err)
	}
	if err := validateKTFMetadata(
		r,
		r.services,
		r.serviceOwner,
		metadata,
	); err != nil {
		return fmt.Errorf("validate KTF adapter state: %w", err)
	}
	metadataBytes, err := shared.MarshalStateComponent(metadata)
	if err != nil {
		return fmt.Errorf("encode KTF adapter metadata: %w", err)
	}
	if len(metadataBytes) > int(maxKTFStateMetadata) {
		return fmt.Errorf(
			"save KTF adapter metadata: %d bytes exceeds limit",
			len(metadataBytes),
		)
	}
	if len(serviceState) > int(^uint32(0)) {
		return fmt.Errorf("save KTF services: state is too large")
	}

	writer.u8(1)
	writer.write([]byte{0, 0, 0})
	writer.u32(ktfStateSchema)
	writer.u32(uint32(r.serviceOwner))
	writer.string16(r.serviceName)
	writer.u32(uint32(len(serviceState)))
	writer.write(serviceState)
	if err := m.writeMemoryState(writer, ktfHostBase, ktfHostSize); err != nil {
		return fmt.Errorf("save KTF host memory: %w", err)
	}
	writeHeapAllocations(writer, r.heap.allocations)
	if err := m.writeMemoryState(writer, guestHeapBase, guestHeapSize); err != nil {
		return fmt.Errorf("save KTF heap memory: %w", err)
	}
	writer.u32(uint32(len(incremental)))
	for _, current := range incremental {
		writer.u32(current.Base)
		writer.u32(current.Size)
		allocations := make(map[uint32]uint32, len(current.Allocations))
		for _, block := range current.Allocations {
			allocations[block.Address] = block.Size
		}
		writeHeapAllocations(writer, allocations)
	}
	writer.u32(uint32(len(metadataBytes)))
	writer.write(metadataBytes)
	return nil
}

func (m *Machine) parseKTFState(
	decoder *stateDecoder,
) (*ktfSavedState, error) {
	present := decoder.u8()
	decoder.reserved(3)
	if present > 1 {
		return nil, decoder.fail("invalid KTF state presence")
	}
	if present == 0 {
		if m.ktf != nil {
			return nil, decoder.fail("KTF state component is missing")
		}
		return nil, nil
	}
	if m.ktf == nil {
		return nil, decoder.fail("unexpected KTF state component")
	}
	if schema := decoder.u32(); schema != ktfStateSchema {
		return nil, decoder.fail(fmt.Sprintf("unsupported KTF state schema %d", schema))
	}
	owner := shared.OwnerID(decoder.u32())
	name := decoder.string16()
	serviceSize := decoder.u32()
	if uint64(serviceSize) > shared.MaxServicesStateBytes {
		return nil, decoder.fail("KTF shared service state exceeds limit")
	}
	serviceData := decoder.bytes(int(serviceSize))
	candidate, err := shared.NewServices(shared.Config{})
	if err != nil {
		return nil, decoder.fail(fmt.Sprintf("initialize KTF service candidate: %v", err))
	}
	if err := candidate.UnmarshalBinary(serviceData); err != nil {
		return nil, decoder.fail(fmt.Sprintf("invalid KTF shared services: %v", err))
	}
	if owner != m.ktf.serviceOwner || name != m.ktf.serviceName ||
		!reflect.DeepEqual(candidate.Config, m.ktf.serviceConfig) {
		return nil, decoder.fail("KTF service identity or configuration mismatch")
	}
	adapter, err := candidate.Coordinator.Adapter(owner)
	if err != nil || adapter.Name != name {
		return nil, decoder.fail("KTF coordinator adapter mismatch")
	}
	hostMemory := append([]byte(nil), decoder.bytes(int(ktfHostSize))...)
	heapAllocations, err := readHeapAllocations(
		decoder,
		guestHeapBase,
		guestHeapSize,
	)
	if err != nil {
		return nil, err
	}
	heapMemory := append([]byte(nil), decoder.bytes(int(guestHeapSize))...)
	incrementalCount := decoder.u32()
	if incrementalCount > maxKTFStateEntries {
		return nil, decoder.fail("KTF incremental heap count exceeds limit")
	}
	incremental := make([]ktfIncrementalHeapSnapshot, 0, incrementalCount)
	var previousBase uint32
	for index := uint32(0); index < incrementalCount; index++ {
		base, size := decoder.u32(), decoder.u32()
		if size == 0 || (index != 0 && base <= previousBase) {
			return nil, decoder.fail("invalid KTF incremental heap geometry")
		}
		blocks, readErr := readHeapAllocations(decoder, base, size)
		if readErr != nil {
			return nil, readErr
		}
		current := ktfIncrementalHeapSnapshot{Base: base, Size: size}
		for _, block := range blocks {
			current.Allocations = append(current.Allocations, heapBlockSnapshot{
				Address: block.address,
				Size:    block.size,
			})
		}
		incremental = append(incremental, current)
		previousBase = base
	}
	metadataSize := decoder.u32()
	if metadataSize > maxKTFStateMetadata {
		return nil, decoder.fail("KTF adapter metadata exceeds limit")
	}
	metadataBytes := decoder.bytes(int(metadataSize))
	var metadata ktfMetadataSnapshot
	if err := shared.UnmarshalStateComponent(metadataBytes, &metadata); err != nil {
		return nil, decoder.fail(fmt.Sprintf("invalid KTF adapter metadata: %v", err))
	}
	if err := validateKTFIncrementalMemory(metadata.IncrementalMemory, incremental); err != nil {
		return nil, decoder.fail(fmt.Sprintf("invalid KTF incremental memory graph: %v", err))
	}
	if err := validateKTFMetadata(m.ktf, candidate, owner, metadata); err != nil {
		return nil, decoder.fail(fmt.Sprintf("invalid KTF adapter graph: %v", err))
	}
	resolvedCalls, err := resolveKTFHostCalls(m.ktf, metadata.HostCalls)
	if err != nil {
		return nil, decoder.fail(fmt.Sprintf("invalid KTF host-call graph: %v", err))
	}
	if decoder.err != nil {
		return nil, decoder.err
	}
	return &ktfSavedState{
		owner:             owner,
		name:              name,
		services:          candidate,
		hostMemory:        hostMemory,
		heapMemory:        heapMemory,
		heapAllocations:   heapAllocations,
		incrementalHeaps:  incremental,
		metadata:          metadata,
		resolvedHostCalls: resolvedCalls,
	}, nil
}

func snapshotKTFMetadata(
	r *ktfRuntime,
	started bool,
) (ktfMetadataSnapshot, error) {
	taskIndices := make(map[*ktfTask]int32, len(r.tasks))
	for index, task := range r.tasks {
		if task == nil {
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF state: task %d is nil",
				index,
			)
		}
		taskIndices[task] = int32(index)
	}
	taskIndex := func(task *ktfTask) (int32, error) {
		if task == nil {
			return -1, nil
		}
		index, ok := taskIndices[task]
		if !ok {
			return -1, fmt.Errorf("task pointer is outside the task table")
		}
		return index, nil
	}
	incrementalMemory := make(
		[]ktfIncrementalMemoryRegionSnapshot,
		len(r.incrementalMemory),
	)
	for index, region := range r.incrementalMemory {
		incrementalMemory[index] = ktfIncrementalMemoryRegionSnapshot{
			Base: region.base,
			Size: region.size,
		}
	}
	sort.Slice(incrementalMemory, func(i, j int) bool {
		return incrementalMemory[i].Base < incrementalMemory[j].Base
	})

	meta := ktfMetadataSnapshot{
		Started: started,
		Exe: ktfExecutableSnapshot{
			WipiExeAddress:      r.exe.WipiExeAddress,
			ExeInterfaceAddress: r.exe.ExeInterfaceAddress,
			FunctionsAddress:    r.exe.FunctionsAddress,
			Name:                r.exe.Name,
			ExecutableInit:      r.exe.ExecutableInit,
			InterfaceInit:       r.exe.InterfaceInit,
			GetDefaultDLL:       r.exe.GetDefaultDLL,
			GetClass:            r.exe.GetClass,
			InterfaceUnknown2:   r.exe.InterfaceUnknown2,
			InterfaceUnknown3:   r.exe.InterfaceUnknown3,
		},
		NextHostCall: r.nextHostCall,

		KnlInterface:       r.knlInterface,
		JBInterface:        r.jbInterface,
		WIPICInterface:     r.wipicInterface,
		MXUserMemInterface: r.mxUserMemInterface,
		IncrementalMemory:  incrementalMemory,

		ImageServices:        cloneUint32ServiceMap(r.imageServices),
		JavaAssetServices:    cloneUint32ServiceMap(r.javaAssetServices),
		FontServices:         cloneUint32ServiceMap(r.fontServices),
		GraphicsServices:     cloneUint32ServiceMap(r.graphicsServices),
		WIPICSurfaceServices: cloneUint32ServiceMap(r.wipicSurfaceServices),
		WIPICAssetServices:   cloneUint32ServiceMap(r.wipicAssetServices),
		WIPICTimerServices:   cloneUint32ServiceMap(r.wipicTimerServices),
		ClipServices:         cloneUint32ServiceMap(r.clipServices),
		DatabaseServices:     cloneStringServiceMap(r.databaseServices),
		FileServices:         cloneUint32ServiceMap(r.fileServices),
		WIPICFileServices:    cloneUint32ServiceMap(r.wipicFileServices),

		JavaClasses:          cloneStringUint32Map(r.javaClasses),
		JavaStrings:          cloneKTFUint32StringMap(r.javaStrings),
		JavaClassObjs:        cloneUint32Map(r.javaClassObjs),
		ClassObjTarget:       cloneUint32Map(r.classObjTarget),
		HostJavaClass:        cloneUint32BoolMap(r.hostJavaClass),
		JavaClassInit:        cloneKTFUint32Uint8Map(r.javaClassInit),
		JVMContext:           r.jvmContext,
		ExceptionContext:     r.exceptionContext,
		JavaEnvironment:      r.javaEnvironment,
		JavaVTables:          cloneUint32Map(r.javaVTables),
		JavaVTableCapacity:   cloneUint32Map(r.javaVTableCapacity),
		JavaVTableClasses:    cloneUint32Map(r.javaVTableClasses),
		HostJavaVirtualSlots: cloneKTFUint32Uint16Map(r.hostJavaVirtualSlots),
		NextHostVirtualSlot:  r.nextHostVirtualSlot,

		LastJavaMethod:          r.lastJavaMethod,
		LastJavaReturn:          r.lastJavaReturn,
		LastJavaJump:            r.lastJavaJump,
		LastJavaCallLR:          r.lastJavaCallLR,
		FirstJavaThrowName:      r.firstJavaThrowName,
		FirstJavaThrowRegisters: append([]uint32(nil), r.firstJavaThrowRegisters...),
		FirstJavaThrowSP:        r.firstJavaThrowSP,
		FirstJavaThrowStack:     append([]uint32(nil), r.firstJavaThrowStack...),
		LastJavaThrowName:       r.lastJavaThrowName,
		LastJavaThrowRegisters:  append([]uint32(nil), r.lastJavaThrowRegisters...),
		LastJavaThrowSP:         r.lastJavaThrowSP,
		LastJavaThrowStack:      append([]uint32(nil), r.lastJavaThrowStack...),
		JavaReturnHigh:          r.javaReturnHigh,
		JavaExceptionFrames:     append([]string(nil), r.javaExceptionFrames...),
		UnimplementedJava:       cloneStringUint64Map(r.unimplementedJava),
		LastUnimplementedJava:   r.lastUnimplementedJava,

		RandomSeeds:       cloneKTFUint32Uint64Map(r.randomSeeds),
		IntegerValues:     cloneKTFUint32Int32Map(r.integerValues),
		LongValues:        cloneKTFUint32Int64Map(r.longValues),
		ThrowableMessages: cloneUint32Map(r.throwableMessages),
		Dates:             cloneKTFUint32Int64Map(r.dates),
		Vectors:           cloneKTFUint32SliceMap(r.vectors),
		Listeners:         cloneUint32Map(r.listeners),
		LWCEventData:      cloneUint32Map(r.lwcEventData),
		LWCChildren:       cloneKTFUint32SliceMap(r.lwcChildren),
		LWCMaxLengths:     cloneKTFUint32Int32Map(r.lwcMaxLengths),
		DefaultRuntime:    r.defaultRuntime,
		DefaultDisplay:    r.defaultDisplay,
		DisplayCards:      cloneUint32Map(r.displayCards),
		ThreadTargets:     cloneUint32Map(r.threadTargets),
		CurrentThread:     r.currentThread,
		StringBuffers:     cloneKTFUint32StringMap(r.stringBuffers),
		InputTargets:      cloneUint32Map(r.inputTargets),
		OutputStreams:     cloneKTFUint32BytesMap(r.outputStreams),
		OutputTargets:     cloneUint32Map(r.outputTargets),
		FileData:          cloneByteMap(r.fileData),
		FileStreamTargets: cloneUint32Map(r.fileStreamTargets),
		SystemInputStream: r.systemInputStream,
		SystemPrintStream: r.systemPrintStream,

		DefaultFont:            r.defaultFont,
		ScreenGraphics:         r.screenGraphics,
		WIPICScreenFramebuffer: r.wipicScreenFramebuffer,
		WIPICResources:         cloneKTFUint32BytesMap(r.wipicResources),
		WIPICResourceIDs:       cloneStringUint32Map(r.wipicResourceIDs),
		WIPICSystemProperties:  cloneKTFStringMap(r.wipicSystemProperties),
		NextWIPICFile:          r.nextWIPICFile,

		DirtyCards:            cloneUint32BoolMap(r.dirtyCards),
		PaintInitializedCards: cloneUint32BoolMap(r.paintInitializedCards),
		PresentCount:          r.presentCount,
		TickMS:                r.tickMS,

		NativeParameterBase: r.nativeParameterBase,
		DeferThreads:        r.deferThreads,
		YieldRequested:      r.yieldRequested,
		TaskCursor:          int32(r.taskCursor),
		ActiveTask:          -1,
		ActiveInstructions:  r.activeInstructions,
		ExecutionDepth:      int32(r.executionDepth),
	}
	if r.activeTask != nil {
		index, err := taskIndex(r.activeTask)
		if err != nil {
			return ktfMetadataSnapshot{}, fmt.Errorf("save KTF active task: %w", err)
		}
		meta.ActiveTask = index
	}

	hostAddresses := sortedUint32Keys(r.hostCalls)
	meta.HostCalls = make([]ktfHostCallSnapshot, 0, len(hostAddresses))
	for _, address := range hostAddresses {
		meta.HostCalls = append(meta.HostCalls, ktfHostCallSnapshot{
			Address: address,
			Name:    r.hostCalls[address].name,
		})
	}

	meta.Hashtables = make(
		map[uint32]map[string]ktfHashtableEntrySnapshot,
		len(r.hashtables),
	)
	for instance, table := range r.hashtables {
		saved := make(map[string]ktfHashtableEntrySnapshot, len(table))
		for key, entry := range table {
			saved[key] = ktfHashtableEntrySnapshot{
				Key: entry.key, Value: entry.value,
			}
		}
		meta.Hashtables[instance] = saved
	}
	meta.Enumerations = make(
		map[uint32]ktfEnumerationSnapshot,
		len(r.enumerations),
	)
	for instance, enumeration := range r.enumerations {
		if enumeration == nil {
			continue
		}
		meta.Enumerations[instance] = ktfEnumerationSnapshot{
			Values: append([]uint32(nil), enumeration.values...),
			Index:  enumeration.index,
		}
	}
	meta.Clips = make(map[uint32]ktfClipSnapshot, len(r.clips))
	for instance, clip := range r.clips {
		if clip == nil {
			continue
		}
		meta.Clips[instance] = ktfClipSnapshot{
			Volume: clip.volume, Listener: clip.listener,
			Playing: clip.playing, Data: append([]byte(nil), clip.data...),
		}
	}
	meta.LWCComponents = make(map[uint32]ktfLWCSnapshot, len(r.lwcComponents))
	for instance, component := range r.lwcComponents {
		if component != nil {
			meta.LWCComponents[instance] = snapshotKTFLWC(component)
		}
	}
	meta.DatabaseStores = make(
		map[string]ktfDatabaseSnapshot,
		len(r.databaseStores),
	)
	for name, database := range r.databaseStores {
		if database != nil {
			meta.DatabaseStores[name] = snapshotKTFDatabase(database)
		}
	}
	meta.Databases = make(map[uint32]string, len(r.databases))
	for instance, database := range r.databases {
		if database != nil {
			meta.Databases[instance] = database.name
		}
	}
	meta.InputStreams = make(
		map[uint32]ktfInputStreamSnapshot,
		len(r.inputStreams),
	)
	for instance, stream := range r.inputStreams {
		if stream != nil {
			meta.InputStreams[instance] = ktfInputStreamSnapshot{
				Data:     append([]byte(nil), stream.data...),
				Position: stream.position,
			}
		}
	}
	meta.Files = snapshotKTFFiles(r.files)
	meta.WIPICFiles = snapshotKTFFiles(r.wipicFiles)

	meta.Images = sortedUint32Keys(r.images)
	meta.Graphics = make(map[uint32]ktfGraphicsSnapshot, len(r.graphics))
	for instance, graphics := range r.graphics {
		if graphics == nil {
			continue
		}
		target, screen, err := ktfGraphicsTarget(r, graphics.target)
		if err != nil {
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF graphics 0x%08x: %w",
				instance,
				err,
			)
		}
		meta.Graphics[instance] = ktfGraphicsSnapshot{
			Target: target,
			Screen: screen,
			Clip: [4]int32{
				int32(graphics.clip.Min.X),
				int32(graphics.clip.Min.Y),
				int32(graphics.clip.Max.X),
				int32(graphics.clip.Max.Y),
			},
			Color: [4]uint8{
				graphics.color.R,
				graphics.color.G,
				graphics.color.B,
				graphics.color.A,
			},
			Translate: [2]int32{
				int32(graphics.translate.X),
				int32(graphics.translate.Y),
			},
		}
	}
	meta.WIPICFramebuffers = make(
		map[uint32]ktfWIPICFramebufferSnapshot,
		len(r.wipicFramebuffers),
	)
	for handle, framebuffer := range r.wipicFramebuffers {
		if framebuffer != nil {
			meta.WIPICFramebuffers[handle] =
				snapshotKTFWIPICFramebuffer(framebuffer)
		}
	}
	meta.WIPICImages = make(
		map[uint32]ktfWIPICImageSnapshot,
		len(r.wipicImages),
	)
	for object, value := range r.wipicImages {
		if value != nil {
			meta.WIPICImages[object] = ktfWIPICImageSnapshot{
				Object: value.object, Body: value.body,
				Framebuffer: value.framebuffer, Source: value.source,
			}
		}
	}
	meta.WIPICMemory = make(
		map[uint32]ktfWIPICMemorySnapshot,
		len(r.wipicMemory),
	)
	for handle, value := range r.wipicMemory {
		meta.WIPICMemory[handle] = ktfWIPICMemorySnapshot{
			Base: value.base, Data: value.data, Size: value.size,
		}
	}
	meta.WIPICTimers = make(
		map[uint32]ktfWIPICTimerSnapshot,
		len(r.wipicTimers),
	)
	for handle, timer := range r.wipicTimers {
		if timer != nil {
			meta.WIPICTimers[handle] = ktfWIPICTimerSnapshot{
				Callback: timer.callback, Parameter: timer.parameter,
				Deadline: timer.deadline, Active: timer.active,
			}
		}
	}

	meta.Tasks = make([]ktfTaskSnapshot, len(r.tasks))
	for index, task := range r.tasks {
		blocker, err := taskIndex(task.startBlocker)
		if err != nil {
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF task %d blocker: %w",
				index,
				err,
			)
		}
		meta.Tasks[index] = ktfTaskSnapshot{
			Context:         append([]byte(nil), task.context...),
			ExceptionFrame:  task.exceptionFrame,
			LastJavaMethod:  task.lastJavaMethod,
			Done:            task.done,
			PresentOnReturn: task.presentOnReturn,
			BestEffortPaint: task.bestEffortPaint,
			WIPICTimer:      task.wipicTimer,
			PaintCard:       task.paintCard,
			KeyCard:         task.keyCard,
			LayoutOnReturn:  task.layoutOnReturn,
			StartBlocker:    blocker,
			ChildStartGrace: task.childStartGrace,
		}
	}
	meta.PendingJavaCalls = make(
		[]ktfPendingJavaCallSnapshot,
		len(r.pendingJavaCalls),
	)
	for index, call := range r.pendingJavaCalls {
		meta.PendingJavaCalls[index] = ktfPendingJavaCallSnapshot{
			Instance:   call.instance,
			Name:       call.name,
			Descriptor: call.descriptor,
			Args:       append([]uint32(nil), call.args...),
		}
	}
	for card, task := range r.paintTasks {
		index, err := taskIndex(task)
		if err != nil {
			return ktfMetadataSnapshot{}, err
		}
		meta.PaintTasks = append(meta.PaintTasks, ktfTaskMapSnapshot{
			Key: card, Task: index,
		})
	}
	sort.Slice(meta.PaintTasks, func(i, j int) bool {
		return meta.PaintTasks[i].Key < meta.PaintTasks[j].Key
	})
	for task, cards := range r.deferredPaintCards {
		index, err := taskIndex(task)
		if err != nil {
			return ktfMetadataSnapshot{}, err
		}
		meta.DeferredPaintCards = append(
			meta.DeferredPaintCards,
			ktfDeferredCardsSnapshot{
				Task:  index,
				Cards: append([]uint32(nil), cards...),
			},
		)
	}
	sort.Slice(meta.DeferredPaintCards, func(i, j int) bool {
		return meta.DeferredPaintCards[i].Task < meta.DeferredPaintCards[j].Task
	})
	for task, cards := range r.deferredShownCards {
		index, err := taskIndex(task)
		if err != nil {
			return ktfMetadataSnapshot{}, err
		}
		values := make([]uint32, 0, len(cards))
		for card, shown := range cards {
			if shown {
				values = append(values, card)
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		meta.DeferredShownCards = append(
			meta.DeferredShownCards,
			ktfDeferredShownSnapshot{Task: index, Cards: values},
		)
	}
	sort.Slice(meta.DeferredShownCards, func(i, j int) bool {
		return meta.DeferredShownCards[i].Task < meta.DeferredShownCards[j].Task
	})
	return meta, nil
}

func sortedKTFIncrementalHeaps(r *ktfRuntime) []ktfIncrementalHeapSnapshot {
	keys := sortedUint32Keys(r.incrementalHeaps)
	result := make([]ktfIncrementalHeapSnapshot, 0, len(keys))
	for _, key := range keys {
		heap := r.incrementalHeaps[key]
		if heap == nil {
			continue
		}
		size := uint32(0)
		for _, region := range r.incrementalMemory {
			if region.base == key {
				size = region.size
				break
			}
		}
		current := ktfIncrementalHeapSnapshot{Base: key, Size: size}
		addresses := sortedUint32Keys(heap.allocations)
		for _, address := range addresses {
			current.Allocations = append(
				current.Allocations,
				heapBlockSnapshot{
					Address: address,
					Size:    heap.allocations[address],
				},
			)
		}
		result = append(result, current)
	}
	return result
}

func snapshotKTFLWC(value *ktfLWCComponent) ktfLWCSnapshot {
	return ktfLWCSnapshot{
		X: value.x, Y: value.y, Width: value.width, Height: value.height,
		PreferredWidth:  value.preferredWidth,
		PreferredHeight: value.preferredHeight,
		Background:      value.background, Foreground: value.foreground,
		Parent: value.parent, Card: value.card, Title: value.title,
		Command: value.command, Work: value.work, Focus: value.focus,
		Text: value.text, Gap: value.gap, Shown: value.shown,
		Valid: value.valid, Focused: value.focused,
		Vertical: value.vertical, Packed: value.packed,
	}
}

func snapshotKTFDatabase(value *ktfDatabase) ktfDatabaseSnapshot {
	return ktfDatabaseSnapshot{
		Name:       value.name,
		RecordSize: value.recordSize,
		Records:    cloneByteSlices(value.records),
	}
}

func snapshotKTFFiles(values map[uint32]*ktfFile) map[uint32]ktfFileSnapshot {
	result := make(map[uint32]ktfFileSnapshot, len(values))
	for handle, file := range values {
		if file != nil {
			result[handle] = ktfFileSnapshot{
				Name: file.name, Position: file.position,
				Mode: file.mode, Closed: file.closed,
			}
		}
	}
	return result
}

func snapshotKTFWIPICFramebuffer(
	value *ktfWIPICFramebuffer,
) ktfWIPICFramebufferSnapshot {
	return ktfWIPICFramebufferSnapshot{
		Object: value.object, Body: value.body,
		PixelObject: value.pixelObject, PixelHeader: value.pixelHeader,
		Pixels: value.pixels, Width: int32(value.width),
		Height: int32(value.height), Stride: int32(value.stride),
		Bits: int32(value.bits), Screen: value.screen,
	}
}

func ktfGraphicsTarget(
	r *ktfRuntime,
	target image.Image,
) (uint32, bool, error) {
	if target == r.frame {
		return 0, true, nil
	}
	for instance, candidate := range r.images {
		if candidate == target {
			return instance, false, nil
		}
	}
	return 0, false, fmt.Errorf("graphics target is not a saved image")
}

func validateKTFMetadata(
	r *ktfRuntime,
	services *shared.Services,
	owner shared.OwnerID,
	meta ktfMetadataSnapshot,
) error {
	counts := []int{
		len(meta.HostCalls), len(meta.IncrementalMemory),
		len(meta.ImageServices), len(meta.JavaAssetServices),
		len(meta.FontServices), len(meta.GraphicsServices),
		len(meta.WIPICSurfaceServices), len(meta.WIPICAssetServices),
		len(meta.WIPICTimerServices), len(meta.ClipServices),
		len(meta.DatabaseServices), len(meta.FileServices),
		len(meta.WIPICFileServices), len(meta.JavaClasses),
		len(meta.JavaStrings), len(meta.JavaClassObjs),
		len(meta.ClassObjTarget), len(meta.HostJavaClass),
		len(meta.JavaClassInit), len(meta.JavaVTables),
		len(meta.JavaVTableCapacity), len(meta.JavaVTableClasses),
		len(meta.HostJavaVirtualSlots), len(meta.UnimplementedJava),
		len(meta.RandomSeeds), len(meta.IntegerValues),
		len(meta.LongValues), len(meta.ThrowableMessages),
		len(meta.Dates), len(meta.Vectors), len(meta.Hashtables),
		len(meta.Enumerations), len(meta.Clips), len(meta.Listeners),
		len(meta.LWCEventData), len(meta.LWCChildren),
		len(meta.LWCMaxLengths), len(meta.LWCComponents),
		len(meta.Databases), len(meta.DatabaseStores),
		len(meta.DisplayCards), len(meta.ThreadTargets),
		len(meta.StringBuffers), len(meta.InputStreams),
		len(meta.InputTargets), len(meta.OutputStreams),
		len(meta.OutputTargets), len(meta.Files), len(meta.FileData),
		len(meta.FileStreamTargets), len(meta.Images), len(meta.Graphics),
		len(meta.WIPICFramebuffers), len(meta.WIPICImages),
		len(meta.WIPICResources), len(meta.WIPICResourceIDs),
		len(meta.WIPICMemory), len(meta.WIPICTimers),
		len(meta.WIPICSystemProperties), len(meta.WIPICFiles),
		len(meta.DirtyCards), len(meta.PaintInitializedCards),
		len(meta.PaintTasks), len(meta.DeferredPaintCards),
		len(meta.DeferredShownCards), len(meta.PendingJavaCalls),
	}
	for _, count := range counts {
		if count > maxKTFStateEntries {
			return fmt.Errorf("metadata table has %d entries", count)
		}
	}
	if len(meta.HostCalls) > maxKTFStateHostCalls ||
		len(meta.Tasks) > ktfMaxTasks ||
		meta.ExecutionDepth != 0 ||
		meta.NativeParameterBase != 0 ||
		meta.NextHostCall < ktfHostBase+4 ||
		meta.NextHostCall > ktfHostBase+ktfHostSize ||
		meta.NextHostCall&3 != 0 ||
		meta.NextHostVirtualSlot < ktfHostVirtualSlotBase {
		return fmt.Errorf("invalid KTF scheduler or host-call limits")
	}
	if meta.TaskCursor < 0 ||
		(len(meta.Tasks) == 0 && meta.TaskCursor != 0) ||
		(len(meta.Tasks) != 0 && meta.TaskCursor >= int32(len(meta.Tasks))) ||
		meta.ActiveTask < -1 || meta.ActiveTask >= int32(len(meta.Tasks)) {
		return fmt.Errorf("invalid KTF task cursor")
	}
	for index, task := range meta.Tasks {
		if len(task.Context) > maxStateContext ||
			task.StartBlocker < -1 ||
			task.StartBlocker >= int32(len(meta.Tasks)) ||
			task.StartBlocker == int32(index) {
			return fmt.Errorf("invalid task %d", index)
		}
	}
	for _, call := range meta.PendingJavaCalls {
		if len(call.Args) > maxKTFStateEntries ||
			len(call.Name) > 4096 || len(call.Descriptor) > 4096 {
			return fmt.Errorf("invalid pending Java call")
		}
	}
	for index, call := range meta.HostCalls {
		if call.Address < ktfHostBase ||
			call.Address >= meta.NextHostCall ||
			call.Address&3 != 0 ||
			call.Name == "" || len(call.Name) > 4096 ||
			(index != 0 &&
				call.Address <= meta.HostCalls[index-1].Address) {
			return fmt.Errorf("invalid host-call entry %d", index)
		}
	}
	if meta.TickMS != uint64(
		services.Clock.Monotonic()/1_000_000,
	) {
		return fmt.Errorf("KTF clock mirror does not match shared clock")
	}
	if meta.Exe.WipiExeAddress != r.exe.WipiExeAddress ||
		meta.Exe.ExeInterfaceAddress != r.exe.ExeInterfaceAddress ||
		meta.Exe.FunctionsAddress != r.exe.FunctionsAddress ||
		meta.Exe.Name != r.exe.Name {
		return fmt.Errorf("KTF executable identity mismatch")
	}
	for _, mapping := range []struct {
		name   string
		values map[uint32]shared.ServiceID
		kind   shared.ObjectKind
	}{
		{"image surface", meta.ImageServices, shared.KindSurface},
		{"Java asset", meta.JavaAssetServices, shared.KindImage},
		{"font", meta.FontServices, shared.KindFont},
		{"graphics", meta.GraphicsServices, shared.KindSurface},
		{"WIPI-C surface", meta.WIPICSurfaceServices, shared.KindSurface},
		{"WIPI-C asset", meta.WIPICAssetServices, shared.KindImage},
		{"WIPI-C timer", meta.WIPICTimerServices, shared.KindTimer},
		{"clip", meta.ClipServices, shared.KindClip},
		{"file", meta.FileServices, shared.KindFile},
		{"WIPI-C file", meta.WIPICFileServices, shared.KindFile},
	} {
		for guest, id := range mapping.values {
			if guest == 0 || services.Registry.Validate(id, owner, mapping.kind) != nil {
				return fmt.Errorf("%s mapping 0x%08x is invalid", mapping.name, guest)
			}
		}
	}
	for name, id := range meta.DatabaseServices {
		if strings.TrimSpace(name) == "" ||
			services.Registry.Validate(id, owner, shared.KindRecordBase) != nil {
			return fmt.Errorf("database service mapping %q is invalid", name)
		}
	}
	for _, object := range meta.Images {
		if object == 0 || meta.ImageServices[object] == 0 {
			return fmt.Errorf("image 0x%08x has no shared surface", object)
		}
	}
	for instance, graphics := range meta.Graphics {
		if meta.GraphicsServices[instance] == 0 ||
			(!graphics.Screen && meta.ImageServices[graphics.Target] == 0) {
			return fmt.Errorf("graphics 0x%08x has an invalid target", instance)
		}
		if graphics.Clip[2] < graphics.Clip[0] ||
			graphics.Clip[3] < graphics.Clip[1] {
			return fmt.Errorf("graphics 0x%08x has an invalid clip", instance)
		}
	}
	for handle, framebuffer := range meta.WIPICFramebuffers {
		if handle == 0 || framebuffer.Object != handle ||
			framebuffer.Width <= 0 || framebuffer.Height <= 0 ||
			framebuffer.Stride < framebuffer.Width*2 ||
			framebuffer.Bits != 16 ||
			meta.WIPICSurfaceServices[handle] == 0 {
			return fmt.Errorf("invalid WIPI-C framebuffer 0x%08x", handle)
		}
	}
	for object, value := range meta.WIPICImages {
		if object == 0 || value.Object != object ||
			meta.WIPICFramebuffers[value.Framebuffer].Object == 0 ||
			meta.WIPICAssetServices[object] == 0 {
			return fmt.Errorf("invalid WIPI-C image 0x%08x", object)
		}
	}
	for instance, name := range meta.Databases {
		if instance == 0 || meta.DatabaseStores[name].Name != name {
			return fmt.Errorf("invalid database instance 0x%08x", instance)
		}
	}
	for name, database := range meta.DatabaseStores {
		if name == "" || database.Name != name ||
			len(database.Records) > maxKTFStateEntries {
			return fmt.Errorf("invalid database store %q", name)
		}
		for _, record := range database.Records {
			if len(record) > int(services.Config.Limits.Storage.MaxRecordBytes) {
				return fmt.Errorf("database store %q record exceeds limit", name)
			}
		}
	}
	for _, reference := range meta.PaintTasks {
		if reference.Task < 0 || reference.Task >= int32(len(meta.Tasks)) {
			return fmt.Errorf("invalid paint-task reference")
		}
	}
	for _, reference := range meta.DeferredPaintCards {
		if reference.Task < 0 || reference.Task >= int32(len(meta.Tasks)) {
			return fmt.Errorf("invalid deferred-paint task reference")
		}
	}
	for _, reference := range meta.DeferredShownCards {
		if reference.Task < 0 || reference.Task >= int32(len(meta.Tasks)) {
			return fmt.Errorf("invalid deferred-shown task reference")
		}
	}
	return nil
}

func validateKTFIncrementalMemory(
	regions []ktfIncrementalMemoryRegionSnapshot,
	heaps []ktfIncrementalHeapSnapshot,
) error {
	if len(regions) != len(heaps) {
		return fmt.Errorf(
			"region count %d does not match heap count %d",
			len(regions),
			len(heaps),
		)
	}
	var previousEnd uint64
	for index, region := range regions {
		end := uint64(region.Base) + uint64(region.Size)
		if region.Base == 0 || region.Size == 0 ||
			end > uint64(^uint32(0))+1 ||
			(index != 0 &&
				(region.Base <= regions[index-1].Base ||
					uint64(region.Base) < previousEnd)) ||
			region.Base != heaps[index].Base ||
			region.Size != heaps[index].Size {
			return fmt.Errorf("invalid incremental region %d", index)
		}
		previousEnd = end
	}
	return nil
}

func resolveKTFHostCalls(
	r *ktfRuntime,
	saved []ktfHostCallSnapshot,
) (map[uint32]ktfHostCall, error) {
	byName := make(map[string]ktfHostHandler, len(r.hostCalls))
	for _, call := range r.hostCalls {
		if call.name != "" && call.handler != nil {
			byName[call.name] = call.handler
		}
	}
	result := make(map[uint32]ktfHostCall, len(saved))
	for _, current := range saved {
		handler := byName[current.Name]
		if handler == nil {
			var err error
			handler, err = deriveKTFHostHandler(current.Name)
			if err != nil {
				return nil, err
			}
		}
		result[current.Address] = ktfHostCall{
			name:    current.Name,
			handler: handler,
		}
	}
	return result, nil
}

func deriveKTFHostHandler(name string) (ktfHostHandler, error) {
	if strings.HasPrefix(name, "java.method.") {
		signature := strings.TrimPrefix(name, "java.method.")
		open := strings.IndexByte(signature, '(')
		if open <= 0 {
			return nil, fmt.Errorf("malformed Java host call %q", name)
		}
		dot := strings.LastIndexByte(signature[:open], '.')
		if dot <= 0 || dot+1 >= open {
			return nil, fmt.Errorf("malformed Java host call %q", name)
		}
		return ktfHostJavaMethod(
			signature[:dot],
			signature[dot+1:open],
			signature[open:],
		), nil
	}
	if strings.HasPrefix(name, "wipic.") {
		parts := strings.Split(name, ".")
		if len(parts) == 3 {
			table, tableErr := strconv.Atoi(parts[1])
			slot, slotErr := strconv.Atoi(parts[2])
			if tableErr == nil && slotErr == nil &&
				table >= 0 && table < 17 && slot >= 0 && slot < 64 {
				return ktfWIPICHandler(table, slot), nil
			}
		}
	}
	return nil, fmt.Errorf("cannot reconstruct host call %q", name)
}

func (m *Machine) restoreKTFState(saved *ktfSavedState) error {
	if m.ktf == nil {
		if saved != nil {
			return fmt.Errorf("restore KTF state: adapter is absent")
		}
		return nil
	}
	if saved == nil {
		return fmt.Errorf("restore KTF state: component is missing")
	}
	r := m.ktf
	meta := saved.metadata
	if err := m.cpu.WriteMemory(ktfHostBase, saved.hostMemory); err != nil {
		return fmt.Errorf("restore KTF host memory: %w", err)
	}
	if err := m.cpu.WriteMemory(guestHeapBase, saved.heapMemory); err != nil {
		return fmt.Errorf("restore KTF heap memory: %w", err)
	}
	if err := restoreHeapMetadata(
		&r.heap,
		guestHeapBase,
		guestHeapSize,
		saved.heapAllocations,
	); err != nil {
		return fmt.Errorf("restore KTF heap metadata: %w", err)
	}
	r.incrementalHeaps = make(
		map[uint32]*guestHeap,
		len(saved.incrementalHeaps),
	)
	for _, current := range saved.incrementalHeaps {
		heap := newGuestHeap(r.cpu, current.Base, current.Size)
		blocks := make([]heapBlock, len(current.Allocations))
		for index, block := range current.Allocations {
			blocks[index] = heapBlock{
				address: block.Address,
				size:    block.Size,
			}
		}
		if err := restoreHeapMetadata(
			&heap,
			current.Base,
			current.Size,
			blocks,
		); err != nil {
			return fmt.Errorf(
				"restore KTF incremental heap 0x%08x: %w",
				current.Base,
				err,
			)
		}
		r.incrementalHeaps[current.Base] = &heap
	}

	r.services = saved.services
	r.serviceConfig = saved.services.Config
	r.serviceOwner = saved.owner
	r.serviceName = saved.name
	r.imageServices = cloneUint32ServiceMap(meta.ImageServices)
	r.javaAssetServices = cloneUint32ServiceMap(meta.JavaAssetServices)
	r.fontServices = cloneUint32ServiceMap(meta.FontServices)
	r.graphicsServices = cloneUint32ServiceMap(meta.GraphicsServices)
	r.wipicSurfaceServices = cloneUint32ServiceMap(meta.WIPICSurfaceServices)
	r.wipicAssetServices = cloneUint32ServiceMap(meta.WIPICAssetServices)
	r.wipicTimerServices = cloneUint32ServiceMap(meta.WIPICTimerServices)
	r.clipServices = cloneUint32ServiceMap(meta.ClipServices)
	r.databaseServices = cloneStringServiceMap(meta.DatabaseServices)
	r.fileServices = cloneUint32ServiceMap(meta.FileServices)
	r.wipicFileServices = cloneUint32ServiceMap(meta.WIPICFileServices)

	r.exe = ktfExecutable{
		WipiExeAddress:      meta.Exe.WipiExeAddress,
		ExeInterfaceAddress: meta.Exe.ExeInterfaceAddress,
		FunctionsAddress:    meta.Exe.FunctionsAddress,
		Name:                meta.Exe.Name,
		ExecutableInit:      meta.Exe.ExecutableInit,
		InterfaceInit:       meta.Exe.InterfaceInit,
		GetDefaultDLL:       meta.Exe.GetDefaultDLL,
		GetClass:            meta.Exe.GetClass,
		InterfaceUnknown2:   meta.Exe.InterfaceUnknown2,
		InterfaceUnknown3:   meta.Exe.InterfaceUnknown3,
	}
	r.nextHostCall = meta.NextHostCall
	r.hostCalls = saved.resolvedHostCalls
	r.knlInterface = meta.KnlInterface
	r.jbInterface = meta.JBInterface
	r.wipicInterface = meta.WIPICInterface
	r.mxUserMemInterface = meta.MXUserMemInterface
	r.incrementalMemory = make(
		[]ktfIncrementalMemoryRegion,
		len(meta.IncrementalMemory),
	)
	for index, region := range meta.IncrementalMemory {
		r.incrementalMemory[index] = ktfIncrementalMemoryRegion{
			base: region.Base,
			size: region.Size,
		}
	}

	r.javaClasses = cloneStringUint32Map(meta.JavaClasses)
	r.javaStrings = cloneKTFUint32StringMap(meta.JavaStrings)
	r.javaClassObjs = cloneUint32Map(meta.JavaClassObjs)
	r.classObjTarget = cloneUint32Map(meta.ClassObjTarget)
	r.hostJavaClass = cloneUint32BoolMap(meta.HostJavaClass)
	r.javaClassInit = cloneKTFUint32Uint8Map(meta.JavaClassInit)
	r.jvmContext = meta.JVMContext
	r.exceptionContext = meta.ExceptionContext
	r.javaEnvironment = meta.JavaEnvironment
	r.javaVTables = cloneUint32Map(meta.JavaVTables)
	r.javaVTableCapacity = cloneUint32Map(meta.JavaVTableCapacity)
	r.javaVTableClasses = cloneUint32Map(meta.JavaVTableClasses)
	r.hostJavaVirtualSlots = cloneKTFUint32Uint16Map(meta.HostJavaVirtualSlots)
	r.nextHostVirtualSlot = meta.NextHostVirtualSlot
	r.lastJavaMethod = meta.LastJavaMethod
	r.lastJavaReturn = meta.LastJavaReturn
	r.lastJavaJump = meta.LastJavaJump
	r.lastJavaCallLR = meta.LastJavaCallLR
	r.firstJavaThrowName = meta.FirstJavaThrowName
	r.firstJavaThrowRegisters = append(
		[]uint32(nil),
		meta.FirstJavaThrowRegisters...,
	)
	r.firstJavaThrowSP = meta.FirstJavaThrowSP
	r.firstJavaThrowStack = append([]uint32(nil), meta.FirstJavaThrowStack...)
	r.lastJavaThrowName = meta.LastJavaThrowName
	r.lastJavaThrowRegisters = append(
		[]uint32(nil),
		meta.LastJavaThrowRegisters...,
	)
	r.lastJavaThrowSP = meta.LastJavaThrowSP
	r.lastJavaThrowStack = append([]uint32(nil), meta.LastJavaThrowStack...)
	r.javaReturnHigh = meta.JavaReturnHigh
	r.javaExceptionFrames = append([]string(nil), meta.JavaExceptionFrames...)
	r.unimplementedJava = cloneStringUint64Map(meta.UnimplementedJava)
	r.lastUnimplementedJava = meta.LastUnimplementedJava

	r.randomSeeds = cloneKTFUint32Uint64Map(meta.RandomSeeds)
	r.integerValues = cloneKTFUint32Int32Map(meta.IntegerValues)
	r.longValues = cloneKTFUint32Int64Map(meta.LongValues)
	r.throwableMessages = cloneUint32Map(meta.ThrowableMessages)
	r.dates = cloneKTFUint32Int64Map(meta.Dates)
	r.vectors = cloneKTFUint32SliceMap(meta.Vectors)
	r.hashtables = make(
		map[uint32]map[string]ktfHashtableEntry,
		len(meta.Hashtables),
	)
	for instance, table := range meta.Hashtables {
		restored := make(map[string]ktfHashtableEntry, len(table))
		for key, entry := range table {
			restored[key] = ktfHashtableEntry{
				key: entry.Key, value: entry.Value,
			}
		}
		r.hashtables[instance] = restored
	}
	r.enumerations = make(
		map[uint32]*ktfEnumeration,
		len(meta.Enumerations),
	)
	for instance, enumeration := range meta.Enumerations {
		r.enumerations[instance] = &ktfEnumeration{
			values: append([]uint32(nil), enumeration.Values...),
			index:  enumeration.Index,
		}
	}
	r.clips = make(map[uint32]*ktfClip, len(meta.Clips))
	for instance, clip := range meta.Clips {
		r.clips[instance] = &ktfClip{
			volume: clip.Volume, listener: clip.Listener,
			playing: clip.Playing, data: append([]byte(nil), clip.Data...),
		}
	}
	r.listeners = cloneUint32Map(meta.Listeners)
	r.lwcEventData = cloneUint32Map(meta.LWCEventData)
	r.lwcChildren = cloneKTFUint32SliceMap(meta.LWCChildren)
	r.lwcMaxLengths = cloneKTFUint32Int32Map(meta.LWCMaxLengths)
	r.lwcComponents = make(
		map[uint32]*ktfLWCComponent,
		len(meta.LWCComponents),
	)
	for instance, component := range meta.LWCComponents {
		r.lwcComponents[instance] = restoreKTFLWC(component)
	}
	r.databaseStores = make(
		map[string]*ktfDatabase,
		len(meta.DatabaseStores),
	)
	for name, database := range meta.DatabaseStores {
		r.databaseStores[name] = restoreKTFDatabase(database)
	}
	r.databases = make(map[uint32]*ktfDatabase, len(meta.Databases))
	for instance, name := range meta.Databases {
		r.databases[instance] = r.databaseStores[name]
	}
	r.defaultRuntime = meta.DefaultRuntime
	r.defaultDisplay = meta.DefaultDisplay
	r.displayCards = cloneUint32Map(meta.DisplayCards)
	r.threadTargets = cloneUint32Map(meta.ThreadTargets)
	r.currentThread = meta.CurrentThread
	r.stringBuffers = cloneKTFUint32StringMap(meta.StringBuffers)
	r.inputStreams = make(
		map[uint32]*ktfInputStream,
		len(meta.InputStreams),
	)
	for instance, stream := range meta.InputStreams {
		r.inputStreams[instance] = &ktfInputStream{
			data:     append([]byte(nil), stream.Data...),
			position: stream.Position,
		}
	}
	r.inputTargets = cloneUint32Map(meta.InputTargets)
	r.outputStreams = cloneKTFUint32BytesMap(meta.OutputStreams)
	r.outputTargets = cloneUint32Map(meta.OutputTargets)
	r.files = restoreKTFFiles(meta.Files)
	r.fileData = cloneByteMap(meta.FileData)
	r.fileStreamTargets = cloneUint32Map(meta.FileStreamTargets)
	r.systemInputStream = meta.SystemInputStream
	r.systemPrintStream = meta.SystemPrintStream

	if err := restoreKTFImagesAndGraphics(r, meta); err != nil {
		return err
	}
	r.defaultFont = meta.DefaultFont
	r.screenGraphics = meta.ScreenGraphics
	r.wipicFramebuffers = restoreKTFWIPICFramebuffers(meta.WIPICFramebuffers)
	r.wipicScreenFramebuffer = meta.WIPICScreenFramebuffer
	r.wipicImages = make(
		map[uint32]*ktfWIPICImage,
		len(meta.WIPICImages),
	)
	for object, value := range meta.WIPICImages {
		r.wipicImages[object] = &ktfWIPICImage{
			object: value.Object, body: value.Body,
			framebuffer: value.Framebuffer, source: value.Source,
		}
	}
	r.wipicResources = cloneKTFUint32BytesMap(meta.WIPICResources)
	r.wipicResourceIDs = cloneStringUint32Map(meta.WIPICResourceIDs)
	r.wipicMemory = make(
		map[uint32]ktfWIPICMemory,
		len(meta.WIPICMemory),
	)
	for handle, value := range meta.WIPICMemory {
		r.wipicMemory[handle] = ktfWIPICMemory{
			base: value.Base, data: value.Data, size: value.Size,
		}
	}
	r.wipicTimers = make(
		map[uint32]*ktfWIPICTimer,
		len(meta.WIPICTimers),
	)
	for handle, timer := range meta.WIPICTimers {
		r.wipicTimers[handle] = &ktfWIPICTimer{
			callback: timer.Callback, parameter: timer.Parameter,
			deadline: timer.Deadline, active: timer.Active,
		}
	}
	r.wipicSystemProperties = cloneKTFStringMap(meta.WIPICSystemProperties)
	r.wipicFiles = restoreKTFFiles(meta.WIPICFiles)
	r.nextWIPICFile = meta.NextWIPICFile
	r.dirtyCards = cloneUint32BoolMap(meta.DirtyCards)
	r.paintInitializedCards = cloneUint32BoolMap(meta.PaintInitializedCards)
	r.presentCount = meta.PresentCount
	r.tickMS = meta.TickMS

	r.nativeParameterBase = meta.NativeParameterBase
	r.deferThreads = meta.DeferThreads
	r.yieldRequested = meta.YieldRequested
	r.tasks = make([]*ktfTask, len(meta.Tasks))
	for index, task := range meta.Tasks {
		r.tasks[index] = &ktfTask{
			context:         append([]byte(nil), task.Context...),
			exceptionFrame:  task.ExceptionFrame,
			lastJavaMethod:  task.LastJavaMethod,
			done:            task.Done,
			presentOnReturn: task.PresentOnReturn,
			bestEffortPaint: task.BestEffortPaint,
			wipicTimer:      task.WIPICTimer,
			paintCard:       task.PaintCard,
			keyCard:         task.KeyCard,
			layoutOnReturn:  task.LayoutOnReturn,
			childStartGrace: task.ChildStartGrace,
		}
	}
	for index, task := range meta.Tasks {
		if task.StartBlocker >= 0 {
			r.tasks[index].startBlocker = r.tasks[task.StartBlocker]
		}
	}
	r.pendingJavaCalls = make(
		[]ktfPendingJavaCall,
		len(meta.PendingJavaCalls),
	)
	for index, call := range meta.PendingJavaCalls {
		r.pendingJavaCalls[index] = ktfPendingJavaCall{
			instance: call.Instance, name: call.Name,
			descriptor: call.Descriptor,
			args:       append([]uint32(nil), call.Args...),
		}
	}
	r.taskCursor = int(meta.TaskCursor)
	r.activeTask = nil
	if meta.ActiveTask >= 0 {
		r.activeTask = r.tasks[meta.ActiveTask]
	}
	r.activeInstructions = meta.ActiveInstructions
	r.executionDepth = int(meta.ExecutionDepth)
	r.paintTasks = make(map[uint32]*ktfTask, len(meta.PaintTasks))
	for _, value := range meta.PaintTasks {
		r.paintTasks[value.Key] = r.tasks[value.Task]
	}
	r.deferredPaintCards = make(
		map[*ktfTask][]uint32,
		len(meta.DeferredPaintCards),
	)
	for _, value := range meta.DeferredPaintCards {
		r.deferredPaintCards[r.tasks[value.Task]] =
			append([]uint32(nil), value.Cards...)
	}
	r.deferredShownCards = make(
		map[*ktfTask]map[uint32]bool,
		len(meta.DeferredShownCards),
	)
	for _, value := range meta.DeferredShownCards {
		cards := make(map[uint32]bool, len(value.Cards))
		for _, card := range value.Cards {
			cards[card] = true
		}
		r.deferredShownCards[r.tasks[value.Task]] = cards
	}
	m.ktfStarted = meta.Started
	return nil
}

func restoreKTFLWC(value ktfLWCSnapshot) *ktfLWCComponent {
	return &ktfLWCComponent{
		x: value.X, y: value.Y, width: value.Width, height: value.Height,
		preferredWidth:  value.PreferredWidth,
		preferredHeight: value.PreferredHeight,
		background:      value.Background, foreground: value.Foreground,
		parent: value.Parent, card: value.Card, title: value.Title,
		command: value.Command, work: value.Work, focus: value.Focus,
		text: value.Text, gap: value.Gap, shown: value.Shown,
		valid: value.Valid, focused: value.Focused,
		vertical: value.Vertical, packed: value.Packed,
	}
}

func restoreKTFDatabase(value ktfDatabaseSnapshot) *ktfDatabase {
	return &ktfDatabase{
		name:       value.Name,
		recordSize: value.RecordSize,
		records:    cloneByteSlices(value.Records),
	}
}

func restoreKTFFiles(values map[uint32]ktfFileSnapshot) map[uint32]*ktfFile {
	result := make(map[uint32]*ktfFile, len(values))
	for handle, file := range values {
		result[handle] = &ktfFile{
			name: file.Name, position: file.Position,
			mode: file.Mode, closed: file.Closed,
		}
	}
	return result
}

func restoreKTFWIPICFramebuffers(
	values map[uint32]ktfWIPICFramebufferSnapshot,
) map[uint32]*ktfWIPICFramebuffer {
	result := make(map[uint32]*ktfWIPICFramebuffer, len(values))
	for handle, value := range values {
		result[handle] = &ktfWIPICFramebuffer{
			object: value.Object, body: value.Body,
			pixelObject: value.PixelObject, pixelHeader: value.PixelHeader,
			pixels: value.Pixels, width: int(value.Width),
			height: int(value.Height), stride: int(value.Stride),
			bits: int(value.Bits), screen: value.Screen,
		}
	}
	return result
}

func restoreKTFImagesAndGraphics(
	r *ktfRuntime,
	meta ktfMetadataSnapshot,
) error {
	r.images = make(map[uint32]image.Image, len(meta.Images))
	for _, object := range meta.Images {
		surface := meta.ImageServices[object]
		descriptor, err := r.services.Graphics.Descriptor(r.serviceOwner, surface)
		if err != nil {
			return fmt.Errorf("restore KTF image 0x%08x: %w", object, err)
		}
		pixels, err := r.services.Graphics.RGBA(r.serviceOwner, surface)
		if err != nil {
			return fmt.Errorf("restore KTF image 0x%08x pixels: %w", object, err)
		}
		restored := image.NewRGBA(image.Rect(
			0,
			0,
			int(descriptor.Width),
			int(descriptor.Height),
		))
		copy(restored.Pix, pixels)
		r.images[object] = restored
	}
	r.graphics = make(map[uint32]*ktfGraphics, len(meta.Graphics))
	for instance, value := range meta.Graphics {
		var target image.Image = r.frame
		if !value.Screen {
			target = r.images[value.Target]
		}
		drawTarget, ok := target.(interface {
			image.Image
			Set(int, int, color.Color)
		})
		if !ok {
			return fmt.Errorf(
				"restore KTF graphics 0x%08x: target is not drawable",
				instance,
			)
		}
		r.graphics[instance] = &ktfGraphics{
			target: drawTarget,
			clip: image.Rect(
				int(value.Clip[0]),
				int(value.Clip[1]),
				int(value.Clip[2]),
				int(value.Clip[3]),
			),
			color: color.RGBA{
				R: value.Color[0], G: value.Color[1],
				B: value.Color[2], A: value.Color[3],
			},
			translate: image.Pt(
				int(value.Translate[0]),
				int(value.Translate[1]),
			),
		}
	}
	return nil
}

func cloneKTFUint32StringMap(
	source map[uint32]string,
) map[uint32]string {
	result := make(map[uint32]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFUint32Uint8Map(
	source map[uint32]uint8,
) map[uint32]uint8 {
	result := make(map[uint32]uint8, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFUint32Uint16Map(
	source map[uint32]uint16,
) map[uint32]uint16 {
	result := make(map[uint32]uint16, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFUint32Uint64Map(
	source map[uint32]uint64,
) map[uint32]uint64 {
	result := make(map[uint32]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFUint32Int32Map(
	source map[uint32]int32,
) map[uint32]int32 {
	result := make(map[uint32]int32, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFUint32Int64Map(
	source map[uint32]int64,
) map[uint32]int64 {
	result := make(map[uint32]int64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneKTFUint32SliceMap(
	source map[uint32][]uint32,
) map[uint32][]uint32 {
	result := make(map[uint32][]uint32, len(source))
	for key, value := range source {
		result[key] = append([]uint32(nil), value...)
	}
	return result
}

func cloneKTFUint32BytesMap(
	source map[uint32][]byte,
) map[uint32][]byte {
	result := make(map[uint32][]byte, len(source))
	for key, value := range source {
		result[key] = append([]byte(nil), value...)
	}
	return result
}
