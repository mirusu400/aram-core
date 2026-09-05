package ktf

import (
	"fmt"
	"github.com/mirusu400/aram-core/cpu"
	"image"
	"reflect"
	"sort"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ktfStateSchemaV2     = uint32(2)
	ktfStateSchemaV3     = uint32(3)
	ktfStateSchemaV4     = uint32(4)
	ktfStateSchema       = uint32(5)
	maxKTFStateMetadata  = uint32(64 << 20)
	maxKTFStateEntries   = 16_384
	maxKTFStateHostCalls = int(HostSize / 4)
)

type SavedState struct {
	owner              shared.OwnerID
	name               string
	Services           *shared.Services
	hostMemory         []byte
	heapMemory         []byte
	heapAllocations    []guest.Block
	incrementalHeaps   []ktfIncrementalHeapSnapshot
	metadata           ktfMetadataSnapshot
	taskWakeAtMS       []uint64
	pendingTimers      []*ktfPendingTimer
	WipicScreenPending bool
	resolvedHostCalls  map[uint32]ktfHostCall
}

type ktfPersistentState struct {
	owner          shared.OwnerID
	storage        shared.StoragePersistenceState
	FileData       map[string][]byte
	DatabaseStores map[string]*Database
}

func (r *Runtime) CapturePersistentState() (ktfPersistentState, error) {
	if r == nil || r.Services == nil {
		return ktfPersistentState{}, fmt.Errorf("KTF services are missing")
	}
	state := ktfPersistentState{
		owner:          r.ServiceOwner,
		storage:        r.Services.Storage.ExportPersistence(),
		FileData:       make(map[string][]byte),
		DatabaseStores: make(map[string]*Database),
	}
	for _, file := range state.storage.Files {
		if file.Namespace == shared.NamespacePrivate {
			state.FileData[file.Path] = append([]byte(nil), file.Data...)
		}
	}
	if len(state.storage.RecordStores) != len(r.DatabaseStores) {
		return ktfPersistentState{}, fmt.Errorf(
			"KTF database persistence count differs",
		)
	}
	for _, saved := range state.storage.RecordStores {
		database := r.DatabaseStores[saved.Name]
		if saved.Owner != state.owner || database == nil ||
			database.Name != saved.Name {
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
		clone := &Database{
			Name:       saved.Name,
			RecordSize: database.RecordSize,
			Records:    make([][]byte, len(saved.Records)),
		}
		for index, record := range saved.Records {
			if record.ID != uint32(index) {
				return ktfPersistentState{}, fmt.Errorf(
					"KTF database %q has non-contiguous record %d",
					saved.Name,
					record.ID,
				)
			}
			clone.Records[index] = append([]byte(nil), record.Data...)
		}
		state.DatabaseStores[saved.Name] = clone
	}
	return state, nil
}

func (r *Runtime) RestorePersistentState(state ktfPersistentState) error {
	if r == nil || r.Services == nil {
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
		state.storage.RecordStores[index].Owner = r.ServiceOwner
	}
	if err := r.Services.Storage.ImportPersistence(state.storage); err != nil {
		return fmt.Errorf("restore KTF persistence: %w", err)
	}
	r.FileData = guest.CloneSliceMap(state.FileData)
	r.DatabaseStores = make(
		map[string]*Database,
		len(state.DatabaseStores),
	)
	r.DatabaseServices = make(
		map[string]shared.ServiceID,
		len(state.DatabaseStores),
	)
	for _, name := range guest.SortedStringKeys(state.DatabaseStores) {
		database := state.DatabaseStores[name]
		r.DatabaseStores[name] = &Database{
			Name:       database.Name,
			RecordSize: database.RecordSize,
			Records:    guest.CloneByteSlices(database.Records),
		}
		serviceID, err := r.Services.Storage.OpenRecordStore(
			r.ServiceOwner,
			name,
		)
		if err != nil {
			return fmt.Errorf(
				"reopen KTF database %q after reset: %w",
				name,
				err,
			)
		}
		r.DatabaseServices[name] = serviceID
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
	Gap, ProgressValue, ProgressMax      int32
	ProgressStep, ProgressTop            int32
	ProgressBottom, DialogType           int32
	DialogTimeout, DialogAction          int32
	DialogOK, DialogCancel               uint32
	Font, Image, ImageActive             uint32
	Group, Date                          uint32
	Mode, Minimum, ViewAmount            int32
	ChangeAmount, Delay, ActiveIndex     int32
	Shown, Valid, Focused                bool
	Vertical, Packed, Annunciator        bool
	Transparent, ProgressInput, Selected bool
}

type ktfDatabaseSnapshot struct {
	Name       string
	RecordSize uint32
	Records    [][]byte
}

type ktfInputStreamSnapshot struct {
	Data     []byte
	Position uint32
	Mark     uint32
}

type ktfFileSnapshot struct {
	Namespace uint8
	Name      string
	Position  uint32
	Mode      uint32
	Closed    bool
}

type ktfGraphicsSnapshot struct {
	Target    uint32
	Screen    bool
	Clip      [4]int32
	Color     [4]uint8
	Translate [2]int32
	Origin    [2]int32
}

type ktfWIPICFramebufferSnapshot struct {
	Object, Body, PixelObject, PixelHeader, Pixels uint32
	Width, Height, Stride, Bits                    int32
	Screen                                         bool
}

type ktfWIPICImageSnapshot struct {
	Object, Body, Framebuffer, Source uint32
	FrameIndex                        uint32
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
	MainJlet          uint32
	EventQueue        uint32
	SharedBuffers     map[string]uint32
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
	WIPICDatabases         map[uint32]string
	NextWIPICDatabase      uint32

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

func WriteState(r *Runtime, backend cpu.Backend, started bool, writer *guest.StateWriter) error {
	if r == nil {
		writer.U8(0)
		writer.Write([]byte{0, 0, 0})
		return nil
	}
	if r.executionDepth != 0 || r.activeTask != nil {
		return fmt.Errorf("save KTF state while an adapter continuation is active")
	}
	for index, task := range r.Tasks {
		if err := r.materializeTaskContext(task); err != nil {
			return fmt.Errorf("materialize KTF task %d context: %w", index, err)
		}
	}
	for _, instance := range guest.SortedUint32Keys(r.Graphics) {
		if err := r.syncKTFGraphics(instance); err != nil {
			return fmt.Errorf(
				"sync KTF graphics 0x%08x before save: %w",
				instance,
				err,
			)
		}
	}
	serviceState, err := r.Services.MarshalBinary()
	if err != nil {
		return fmt.Errorf("save KTF shared services: %w", err)
	}
	metadata, err := snapshotKTFMetadata(r, started)
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
		r.Services,
		r.ServiceOwner,
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

	writer.U8(1)
	writer.Write([]byte{0, 0, 0})
	writer.U32(ktfStateSchema)
	writer.U32(uint32(r.ServiceOwner))
	writer.String16(r.serviceName)
	writer.U32(uint32(len(serviceState)))
	writer.Write(serviceState)
	if err := guest.WriteMemoryState(writer, backend, HostBase, HostSize); err != nil {
		return fmt.Errorf("save KTF host memory: %w", err)
	}
	guest.WriteHeapAllocations(writer, r.Heap.Root().Allocations)
	if err := guest.WriteMemoryState(writer, backend, guest.HeapBase, guest.HeapSize); err != nil {
		return fmt.Errorf("save KTF heap memory: %w", err)
	}
	writer.U32(uint32(len(incremental)))
	for _, current := range incremental {
		writer.U32(current.Base)
		writer.U32(current.Size)
		allocations := make(map[uint32]uint32, len(current.Allocations))
		for _, block := range current.Allocations {
			allocations[block.Address] = block.Size
		}
		guest.WriteHeapAllocations(writer, allocations)
	}
	writer.U32(uint32(len(metadataBytes)))
	writer.Write(metadataBytes)
	writer.U32(uint32(len(r.Tasks)))
	for _, task := range r.Tasks {
		writer.U64(task.WakeAtMS)
	}
	if r.WipicScreenPending {
		writer.U8(1)
	} else {
		writer.U8(0)
	}
	writer.Write([]byte{0, 0, 0})
	// A parked java.util.Timer schedule carries what its Task will be given
	// once a slot frees. The pending-call snapshot has no room for it, so it
	// travels alongside, one entry per pending call.
	writer.U32(uint32(len(r.PendingJavaCalls)))
	for _, call := range r.PendingJavaCalls {
		if call.timer == nil {
			writer.U8(0)
			writer.Write([]byte{0, 0, 0})
			continue
		}
		writer.U8(1)
		if call.timer.fixedRate {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.Write([]byte{0, 0})
		writer.U32(call.timer.owner)
		writer.U64(call.timer.periodMS)
		writer.U64(call.timer.deadlineMS)
		writer.U64(call.timer.wakeAtMS)
	}
	return nil
}

func ParseState(r *Runtime,
	decoder *guest.StateDecoder,
) (*SavedState, error) {
	present := decoder.U8()
	decoder.Reserved(3)
	if present > 1 {
		return nil, decoder.Fail("invalid KTF state presence")
	}
	if present == 0 {
		if r != nil {
			return nil, decoder.Fail("KTF state component is missing")
		}
		return nil, nil
	}
	if r == nil {
		return nil, decoder.Fail("unexpected KTF state component")
	}
	schema := decoder.U32()
	if schema != ktfStateSchemaV2 && schema != ktfStateSchemaV3 &&
		schema != ktfStateSchemaV4 && schema != ktfStateSchema {
		return nil, decoder.Fail(fmt.Sprintf("unsupported KTF state schema %d", schema))
	}
	owner := shared.OwnerID(decoder.U32())
	name := decoder.String16()
	serviceSize := decoder.U32()
	if uint64(serviceSize) > shared.MaxServicesStateBytes {
		return nil, decoder.Fail("KTF shared service state exceeds limit")
	}
	serviceData := decoder.Bytes(int(serviceSize))
	candidate, err := shared.NewServices(shared.Config{})
	if err != nil {
		return nil, decoder.Fail(fmt.Sprintf("initialize KTF service candidate: %v", err))
	}
	if err := candidate.UnmarshalBinary(serviceData); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid KTF shared Services: %v", err))
	}
	if owner != r.ServiceOwner || name != r.serviceName ||
		!reflect.DeepEqual(candidate.Config, r.serviceConfig) {
		return nil, decoder.Fail("KTF service identity or configuration mismatch")
	}
	adapter, err := candidate.Coordinator.Adapter(owner)
	if err != nil || adapter.Name != name {
		return nil, decoder.Fail("KTF coordinator adapter mismatch")
	}
	hostMemory := append([]byte(nil), decoder.Bytes(int(HostSize))...)
	heapAllocations, err := guest.ReadHeapAllocations(
		decoder,
		guest.HeapBase,
		guest.HeapSize,
	)
	if err != nil {
		return nil, err
	}
	heapMemory := append([]byte(nil), decoder.Bytes(int(guest.HeapSize))...)
	incrementalCount := decoder.U32()
	if incrementalCount > maxKTFStateEntries {
		return nil, decoder.Fail("KTF incremental heap count exceeds limit")
	}
	incremental := make([]ktfIncrementalHeapSnapshot, 0, incrementalCount)
	var previousBase uint32
	for index := uint32(0); index < incrementalCount; index++ {
		base, size := decoder.U32(), decoder.U32()
		if size == 0 || (index != 0 && base <= previousBase) {
			return nil, decoder.Fail("invalid KTF incremental heap geometry")
		}
		blocks, readErr := guest.ReadHeapAllocations(decoder, base, size)
		if readErr != nil {
			return nil, readErr
		}
		current := ktfIncrementalHeapSnapshot{Base: base, Size: size}
		for _, block := range blocks {
			current.Allocations = append(current.Allocations, heapBlockSnapshot{
				Address: block.Address,
				Size:    block.Size,
			})
		}
		incremental = append(incremental, current)
		previousBase = base
	}
	metadataSize := decoder.U32()
	if metadataSize > maxKTFStateMetadata {
		return nil, decoder.Fail("KTF adapter metadata exceeds limit")
	}
	metadataBytes := decoder.Bytes(int(metadataSize))
	var metadata ktfMetadataSnapshot
	if err := shared.UnmarshalStateComponent(metadataBytes, &metadata); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid KTF adapter metadata: %v", err))
	}
	if err := validateKTFIncrementalMemory(metadata.IncrementalMemory, incremental); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid KTF incremental memory graph: %v", err))
	}
	if err := validateKTFMetadata(r, candidate, owner, metadata); err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid KTF adapter graph: %v", err))
	}
	var taskWakeAtMS []uint64
	if schema >= ktfStateSchemaV3 {
		taskCount := decoder.U32()
		if taskCount > maxKTFStateEntries ||
			int(taskCount) != len(metadata.Tasks) {
			return nil, decoder.Fail("KTF sleeping task count differs")
		}
		taskWakeAtMS = make([]uint64, taskCount)
		for index := range taskWakeAtMS {
			taskWakeAtMS[index] = decoder.U64()
		}
	}
	var wipicScreenPending bool
	if schema >= ktfStateSchemaV4 {
		pending := decoder.U8()
		decoder.Reserved(3)
		if pending > 1 {
			return nil, decoder.Fail("invalid pending KTF WIPI-C screen state")
		}
		wipicScreenPending = pending != 0
		if wipicScreenPending && metadata.WIPICScreenFramebuffer == 0 {
			return nil, decoder.Fail(
				"pending KTF WIPI-C screen state has no framebuffer",
			)
		}
	}
	var pendingTimers []*ktfPendingTimer
	if schema >= ktfStateSchema {
		count := decoder.U32()
		if count > maxKTFStateEntries ||
			int(count) != len(metadata.PendingJavaCalls) {
			return nil, decoder.Fail("KTF pending timer count differs")
		}
		pendingTimers = make([]*ktfPendingTimer, count)
		for index := range pendingTimers {
			present := decoder.U8()
			fixedRate := decoder.U8()
			decoder.Reserved(2)
			if present > 1 || fixedRate > 1 {
				return nil, decoder.Fail("invalid KTF pending timer state")
			}
			if present == 0 {
				if fixedRate != 0 {
					return nil, decoder.Fail("invalid KTF pending timer state")
				}
				continue
			}
			pendingTimers[index] = &ktfPendingTimer{
				fixedRate: fixedRate != 0,
			}
			pendingTimers[index].owner = decoder.U32()
			pendingTimers[index].periodMS = decoder.U64()
			pendingTimers[index].deadlineMS = decoder.U64()
			pendingTimers[index].wakeAtMS = decoder.U64()
		}
	}
	resolvedCalls, err := resolveKTFHostCalls(r, metadata.HostCalls)
	if err != nil {
		return nil, decoder.Fail(fmt.Sprintf("invalid KTF host-call graph: %v", err))
	}
	if decoder.Err != nil {
		return nil, decoder.Err
	}
	return &SavedState{
		owner:              owner,
		name:               name,
		Services:           candidate,
		hostMemory:         hostMemory,
		heapMemory:         heapMemory,
		heapAllocations:    heapAllocations,
		incrementalHeaps:   incremental,
		metadata:           metadata,
		taskWakeAtMS:       taskWakeAtMS,
		pendingTimers:      pendingTimers,
		WipicScreenPending: wipicScreenPending,
		resolvedHostCalls:  resolvedCalls,
	}, nil
}

func snapshotKTFMetadata(
	r *Runtime,
	started bool,
) (ktfMetadataSnapshot, error) {
	taskIndices := make(map[*Task]int32, len(r.Tasks))
	for index, task := range r.Tasks {
		if task == nil {
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF state: task %d is nil",
				index,
			)
		}
		taskIndices[task] = int32(index)
	}
	taskIndex := func(task *Task) (int32, error) {
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
			WipiExeAddress:      r.Exe.WipiExeAddress,
			ExeInterfaceAddress: r.Exe.ExeInterfaceAddress,
			FunctionsAddress:    r.Exe.FunctionsAddress,
			Name:                r.Exe.Name,
			ExecutableInit:      r.Exe.ExecutableInit,
			InterfaceInit:       r.Exe.InterfaceInit,
			GetDefaultDLL:       r.Exe.GetDefaultDLL,
			GetClass:            r.Exe.GetClass,
			InterfaceUnknown2:   r.Exe.InterfaceUnknown2,
			InterfaceUnknown3:   r.Exe.InterfaceUnknown3,
		},
		NextHostCall: r.nextHostCall,

		KnlInterface:       r.knlInterface,
		JBInterface:        r.jbInterface,
		WIPICInterface:     r.wipicInterface,
		MXUserMemInterface: r.mxUserMemInterface,
		IncrementalMemory:  incrementalMemory,

		ImageServices:        guest.CloneMap(r.imageServices),
		JavaAssetServices:    guest.CloneMap(r.javaAssetServices),
		FontServices:         guest.CloneMap(r.FontServices),
		GraphicsServices:     guest.CloneMap(r.GraphicsServices),
		WIPICSurfaceServices: guest.CloneMap(r.wipicSurfaceServices),
		WIPICAssetServices:   guest.CloneMap(r.wipicAssetServices),
		WIPICTimerServices:   guest.CloneMap(r.wipicTimerServices),
		ClipServices:         guest.CloneMap(r.clipServices),
		DatabaseServices:     guest.CloneMap(r.DatabaseServices),
		FileServices:         guest.CloneMap(r.fileServices),
		WIPICFileServices:    guest.CloneMap(r.wipicFileServices),

		JavaClasses:          guest.CloneMap(r.JavaClasses),
		JavaStrings:          guest.CloneMap(r.JavaStrings),
		JavaClassObjs:        guest.CloneMap(r.javaClassObjs),
		ClassObjTarget:       guest.CloneMap(r.classObjTarget),
		HostJavaClass:        guest.CloneMap(r.hostJavaClass),
		JavaClassInit:        guest.CloneMap(r.javaClassInit),
		JVMContext:           r.JvmContext,
		ExceptionContext:     r.exceptionContext,
		JavaEnvironment:      r.javaEnvironment,
		JavaVTables:          guest.CloneMap(r.javaVTables),
		JavaVTableCapacity:   guest.CloneMap(r.javaVTableCapacity),
		JavaVTableClasses:    guest.CloneMap(r.javaVTableClasses),
		HostJavaVirtualSlots: guest.CloneMap(r.hostJavaVirtualSlots),
		NextHostVirtualSlot:  r.nextHostVirtualSlot,

		LastJavaMethod:          r.LastJavaMethod,
		LastJavaReturn:          r.LastJavaReturn,
		LastJavaJump:            r.lastJavaJump,
		LastJavaCallLR:          r.LastJavaCallLR,
		FirstJavaThrowName:      r.FirstJavaThrowName,
		FirstJavaThrowRegisters: append([]uint32(nil), r.FirstJavaThrowRegisters...),
		FirstJavaThrowSP:        r.FirstJavaThrowSP,
		FirstJavaThrowStack:     append([]uint32(nil), r.FirstJavaThrowStack...),
		LastJavaThrowName:       r.LastJavaThrowName,
		LastJavaThrowRegisters:  append([]uint32(nil), r.LastJavaThrowRegisters...),
		LastJavaThrowSP:         r.LastJavaThrowSP,
		LastJavaThrowStack:      append([]uint32(nil), r.LastJavaThrowStack...),
		JavaReturnHigh:          r.JavaReturnHigh,
		JavaExceptionFrames:     append([]string(nil), r.JavaExceptionFrames...),
		UnimplementedJava:       guest.CloneMap(r.UnimplementedJava),
		LastUnimplementedJava:   r.LastUnimplementedJava,

		RandomSeeds:       guest.CloneMap(r.randomSeeds),
		IntegerValues:     guest.CloneMap(r.integerValues),
		LongValues:        guest.CloneMap(r.longValues),
		ThrowableMessages: guest.CloneMap(r.throwableMessages),
		Dates:             guest.CloneMap(r.dates),
		Vectors:           guest.CloneSliceMap(r.Vectors),
		Listeners:         guest.CloneMap(r.listeners),
		LWCEventData:      guest.CloneMap(r.lwcEventData),
		LWCChildren:       guest.CloneSliceMap(r.lwcChildren),
		LWCMaxLengths:     guest.CloneMap(r.lwcMaxLengths),
		DefaultRuntime:    r.defaultRuntime,
		DefaultDisplay:    r.DefaultDisplay,
		MainJlet:          r.MainJlet,
		EventQueue:        r.eventQueue,
		SharedBuffers:     guest.CloneMap(r.sharedBuffers),
		DisplayCards:      guest.CloneMap(r.DisplayCards),
		ThreadTargets:     guest.CloneMap(r.ThreadTargets),
		CurrentThread:     r.currentThread,
		StringBuffers:     guest.CloneMap(r.stringBuffers),
		InputTargets:      guest.CloneMap(r.inputTargets),
		OutputStreams:     guest.CloneSliceMap(r.outputStreams),
		OutputTargets:     guest.CloneMap(r.outputTargets),
		FileData:          guest.CloneSliceMap(r.FileData),
		FileStreamTargets: guest.CloneMap(r.fileStreamTargets),
		SystemInputStream: r.systemInputStream,
		SystemPrintStream: r.systemPrintStream,

		DefaultFont:            r.defaultFont,
		ScreenGraphics:         r.ScreenGraphics,
		WIPICScreenFramebuffer: r.WipicScreenFramebuffer,
		WIPICResources:         guest.CloneSliceMap(r.wipicResources),
		WIPICResourceIDs:       guest.CloneMap(r.wipicResourceIDs),
		WIPICSystemProperties:  guest.CloneMap(r.wipicSystemProperties),
		NextWIPICFile:          r.nextWIPICFile,
		WIPICDatabases:         guest.CloneMap(r.wipicDatabases),
		NextWIPICDatabase:      r.nextWIPICDatabase,

		DirtyCards:            guest.CloneMap(r.dirtyCards),
		PaintInitializedCards: guest.CloneMap(r.paintInitializedCards),
		PresentCount:          r.PresentCount,
		TickMS:                r.TickMS,

		NativeParameterBase: r.NativeParameterBase,
		DeferThreads:        r.DeferThreads,
		YieldRequested:      r.yieldRequested,
		TaskCursor:          int32(r.taskCursor),
		ActiveTask:          -1,
		ActiveInstructions:  r.ActiveInstructions,
		ExecutionDepth:      int32(r.executionDepth),
	}
	if r.activeTask != nil {
		index, err := taskIndex(r.activeTask)
		if err != nil {
			return ktfMetadataSnapshot{}, fmt.Errorf("save KTF active task: %w", err)
		}
		meta.ActiveTask = index
	}

	hostAddresses := guest.SortedUint32Keys(r.hostCalls)
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
		len(r.DatabaseStores),
	)
	for name, database := range r.DatabaseStores {
		if database != nil {
			meta.DatabaseStores[name] = snapshotKTFDatabase(database)
		}
	}
	meta.Databases = make(map[uint32]string, len(r.databases))
	for instance, database := range r.databases {
		if database != nil {
			meta.Databases[instance] = database.Name
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
				Mark:     stream.mark,
			}
		}
	}
	meta.Files = snapshotKTFFiles(r.files)
	meta.WIPICFiles = snapshotKTFFiles(r.wipicFiles)

	meta.Images = guest.SortedUint32Keys(r.images)
	meta.Graphics = make(map[uint32]ktfGraphicsSnapshot, len(r.Graphics))
	for instance, graphics := range r.Graphics {
		if graphics == nil {
			continue
		}
		target, screen, err := ktfGraphicsTarget(r, graphics.Target)
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
			Origin: [2]int32{
				int32(graphics.origin.X),
				int32(graphics.origin.Y),
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
				FrameIndex: value.frameIndex,
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

	meta.Tasks = make([]ktfTaskSnapshot, len(r.Tasks))
	for index, task := range r.Tasks {
		blocker, err := taskIndex(task.startBlocker)
		if err != nil {
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF task %d blocker: %w",
				index,
				err,
			)
		}
		meta.Tasks[index] = ktfTaskSnapshot{
			Context:         append([]byte(nil), task.Context...),
			ExceptionFrame:  task.exceptionFrame,
			LastJavaMethod:  task.LastJavaMethod,
			Done:            task.Done,
			PresentOnReturn: task.presentOnReturn,
			BestEffortPaint: task.bestEffortPaint,
			WIPICTimer:      task.WipicTimer,
			PaintCard:       task.paintCard,
			KeyCard:         task.KeyCard,
			LayoutOnReturn:  task.layoutOnReturn,
			StartBlocker:    blocker,
			ChildStartGrace: task.childStartGrace,
		}
	}
	meta.PendingJavaCalls = make(
		[]ktfPendingJavaCallSnapshot,
		len(r.PendingJavaCalls),
	)
	for index, call := range r.PendingJavaCalls {
		meta.PendingJavaCalls[index] = ktfPendingJavaCallSnapshot{
			Instance:   call.instance,
			Name:       call.name,
			Descriptor: call.descriptor,
			Args:       append([]uint32(nil), call.args...),
		}
	}
	for card, task := range r.PaintTasks {
		index, err := taskIndex(task)
		if err != nil {
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF paint task 0x%08x: %w", card, err,
			)
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
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF deferred paint cards: %w", err,
			)
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
			return ktfMetadataSnapshot{}, fmt.Errorf(
				"save KTF deferred shown cards: %w", err,
			)
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

func sortedKTFIncrementalHeaps(r *Runtime) []ktfIncrementalHeapSnapshot {
	keys := guest.SortedUint32Keys(r.incrementalHeaps)
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
		addresses := guest.SortedUint32Keys(heap.Allocations)
		for _, address := range addresses {
			current.Allocations = append(
				current.Allocations,
				heapBlockSnapshot{
					Address: address,
					Size:    heap.Allocations[address],
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
		Parent: value.Parent, Card: value.card, Title: value.title,
		Command: value.command, Work: value.work, Focus: value.focus,
		Text: value.text, Gap: value.gap, Shown: value.shown,
		Valid: value.valid, Focused: value.focused,
		Vertical: value.vertical, Packed: value.packed,
		Annunciator: value.annunciator, Transparent: value.transparent,
		ProgressValue: value.progressValue,
		ProgressMax:   value.progressMax, ProgressStep: value.progressStep,
		ProgressTop: value.progressTop, ProgressBottom: value.progressBottom,
		DialogType: value.dialogType, DialogTimeout: value.dialogTimeout,
		DialogAction: value.dialogAction,
		DialogOK:     value.dialogOK, DialogCancel: value.dialogCancel,
		ProgressInput: value.progressInput,
		Font:          value.font, Image: value.image,
		ImageActive: value.imageActive, Group: value.group,
		Date: value.date, Mode: value.mode, Minimum: value.minimum,
		ViewAmount: value.viewAmount, ChangeAmount: value.changeAmount,
		Delay: value.delay, ActiveIndex: value.activeIndex,
		Selected: value.selected,
	}
}

func snapshotKTFDatabase(value *Database) ktfDatabaseSnapshot {
	return ktfDatabaseSnapshot{
		Name:       value.Name,
		RecordSize: value.RecordSize,
		Records:    guest.CloneByteSlices(value.Records),
	}
}

func snapshotKTFFiles(values map[uint32]*ktfFile) map[uint32]ktfFileSnapshot {
	result := make(map[uint32]ktfFileSnapshot, len(values))
	for handle, file := range values {
		if file != nil {
			result[handle] = ktfFileSnapshot{
				Namespace: uint8(file.namespace),
				Name:      file.name, Position: file.position,
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
	r *Runtime,
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
