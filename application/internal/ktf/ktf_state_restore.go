package ktf

import (
	"fmt"
	"github.com/mirusu400/aram-core/cpu"
	"image"
	"image/color"
	"strconv"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func validateKTFMetadata(
	r *Runtime,
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
		len(meta.Tasks) > MaxTasks ||
		meta.ExecutionDepth != 0 ||
		meta.NativeParameterBase != 0 ||
		meta.NextHostCall < HostBase+4 ||
		meta.NextHostCall > HostBase+HostSize ||
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
		if len(task.Context) > guest.MaxStateContext ||
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
		if call.Address < HostBase ||
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
	if meta.Exe.WipiExeAddress != r.Exe.WipiExeAddress ||
		meta.Exe.ExeInterfaceAddress != r.Exe.ExeInterfaceAddress ||
		meta.Exe.FunctionsAddress != r.Exe.FunctionsAddress ||
		meta.Exe.Name != r.Exe.Name {
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
	if meta.WIPICScreenFramebuffer != 0 {
		framebuffer, ok := meta.WIPICFramebuffers[meta.WIPICScreenFramebuffer]
		if !ok || !framebuffer.Screen {
			return fmt.Errorf("invalid WIPI-C screen framebuffer")
		}
	}
	for object, value := range meta.WIPICImages {
		assetID := meta.WIPICAssetServices[object]
		if object == 0 || value.Object != object ||
			meta.WIPICFramebuffers[value.Framebuffer].Object == 0 ||
			assetID == 0 {
			return fmt.Errorf("invalid WIPI-C image 0x%08x", object)
		}
		asset, err := services.Assets.Info(owner, assetID)
		if err != nil || value.FrameIndex >= uint32(len(asset.Frames)) {
			return fmt.Errorf("invalid WIPI-C image 0x%08x frame", object)
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
	r *Runtime,
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
		return HostJavaMethod(
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

func RestoreState(r *Runtime, backend cpu.Backend, saved *SavedState, started *bool) error {
	if r == nil {
		if saved != nil {
			return fmt.Errorf("restore KTF state: adapter is absent")
		}
		return nil
	}
	if saved == nil {
		return fmt.Errorf("restore KTF state: component is missing")
	}
	meta := saved.metadata
	if err := backend.WriteMemory(HostBase, saved.hostMemory); err != nil {
		return fmt.Errorf("restore KTF host memory: %w", err)
	}
	if err := backend.WriteMemory(guest.HeapBase, saved.heapMemory); err != nil {
		return fmt.Errorf("restore KTF heap memory: %w", err)
	}
	if err := guest.RestoreHeapMetadata(
		&r.Heap,
		guest.HeapBase,
		guest.HeapSize,
		saved.heapAllocations,
	); err != nil {
		return fmt.Errorf("restore KTF heap metadata: %w", err)
	}
	r.incrementalHeaps = make(
		map[uint32]*guest.Heap,
		len(saved.incrementalHeaps),
	)
	for _, current := range saved.incrementalHeaps {
		heap := guest.NewHeap(r.CPU, current.Base, current.Size)
		blocks := make([]guest.Block, len(current.Allocations))
		for index, block := range current.Allocations {
			blocks[index] = guest.Block{
				Address: block.Address,
				Size:    block.Size,
			}
		}
		if err := guest.RestoreHeapMetadata(
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

	r.Services = saved.Services
	r.serviceConfig = saved.Services.Config
	r.ServiceOwner = saved.owner
	r.serviceName = saved.name
	r.imageServices = guest.CloneMap(meta.ImageServices)
	r.javaAssetServices = guest.CloneMap(meta.JavaAssetServices)
	r.FontServices = guest.CloneMap(meta.FontServices)
	r.GraphicsServices = guest.CloneMap(meta.GraphicsServices)
	r.wipicSurfaceServices = guest.CloneMap(meta.WIPICSurfaceServices)
	r.wipicAssetServices = guest.CloneMap(meta.WIPICAssetServices)
	r.wipicTimerServices = guest.CloneMap(meta.WIPICTimerServices)
	r.clipServices = guest.CloneMap(meta.ClipServices)
	r.DatabaseServices = guest.CloneMap(meta.DatabaseServices)
	r.fileServices = guest.CloneMap(meta.FileServices)
	r.wipicFileServices = guest.CloneMap(meta.WIPICFileServices)

	r.Exe = ktfExecutable{
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

	r.JavaClasses = guest.CloneMap(meta.JavaClasses)
	// A restored class set invalidates any resolved native signature. The
	// counter is host bookkeeping, so it stays out of the save format.
	r.javaClassGeneration++
	r.JavaStrings = guest.CloneMap(meta.JavaStrings)
	r.javaClassObjs = guest.CloneMap(meta.JavaClassObjs)
	r.classObjTarget = guest.CloneMap(meta.ClassObjTarget)
	r.hostJavaClass = guest.CloneMap(meta.HostJavaClass)
	r.javaClassInit = guest.CloneMap(meta.JavaClassInit)
	r.JvmContext = meta.JVMContext
	r.exceptionContext = meta.ExceptionContext
	r.javaEnvironment = meta.JavaEnvironment
	r.javaVTables = guest.CloneMap(meta.JavaVTables)
	r.javaVTableCapacity = guest.CloneMap(meta.JavaVTableCapacity)
	r.javaVTableClasses = guest.CloneMap(meta.JavaVTableClasses)
	r.hostJavaVirtualSlots = guest.CloneMap(meta.HostJavaVirtualSlots)
	r.nextHostVirtualSlot = meta.NextHostVirtualSlot
	r.LastJavaMethod = meta.LastJavaMethod
	r.LastJavaReturn = meta.LastJavaReturn
	r.lastJavaJump = meta.LastJavaJump
	r.LastJavaCallLR = meta.LastJavaCallLR
	r.FirstJavaThrowName = meta.FirstJavaThrowName
	r.FirstJavaThrowRegisters = append(
		[]uint32(nil),
		meta.FirstJavaThrowRegisters...,
	)
	r.FirstJavaThrowSP = meta.FirstJavaThrowSP
	r.FirstJavaThrowStack = append([]uint32(nil), meta.FirstJavaThrowStack...)
	r.LastJavaThrowName = meta.LastJavaThrowName
	r.LastJavaThrowRegisters = append(
		[]uint32(nil),
		meta.LastJavaThrowRegisters...,
	)
	r.LastJavaThrowSP = meta.LastJavaThrowSP
	r.LastJavaThrowStack = append([]uint32(nil), meta.LastJavaThrowStack...)
	r.JavaReturnHigh = meta.JavaReturnHigh
	r.JavaExceptionFrames = append([]string(nil), meta.JavaExceptionFrames...)
	r.UnimplementedJava = guest.CloneMap(meta.UnimplementedJava)
	r.LastUnimplementedJava = meta.LastUnimplementedJava

	r.randomSeeds = guest.CloneMap(meta.RandomSeeds)
	r.integerValues = guest.CloneMap(meta.IntegerValues)
	r.longValues = guest.CloneMap(meta.LongValues)
	r.throwableMessages = guest.CloneMap(meta.ThrowableMessages)
	r.dates = guest.CloneMap(meta.Dates)
	r.Vectors = guest.CloneSliceMap(meta.Vectors)
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
	r.listeners = guest.CloneMap(meta.Listeners)
	r.lwcEventData = guest.CloneMap(meta.LWCEventData)
	r.lwcChildren = guest.CloneSliceMap(meta.LWCChildren)
	r.lwcMaxLengths = guest.CloneMap(meta.LWCMaxLengths)
	r.lwcComponents = make(
		map[uint32]*ktfLWCComponent,
		len(meta.LWCComponents),
	)
	for instance, component := range meta.LWCComponents {
		r.lwcComponents[instance] = restoreKTFLWC(component)
	}
	r.DatabaseStores = make(
		map[string]*Database,
		len(meta.DatabaseStores),
	)
	for name, database := range meta.DatabaseStores {
		r.DatabaseStores[name] = restoreKTFDatabase(database)
	}
	r.databases = make(map[uint32]*Database, len(meta.Databases))
	for instance, name := range meta.Databases {
		r.databases[instance] = r.DatabaseStores[name]
	}
	r.defaultRuntime = meta.DefaultRuntime
	r.DefaultDisplay = meta.DefaultDisplay
	r.MainJlet = meta.MainJlet
	r.eventQueue = meta.EventQueue
	r.sharedBuffers = guest.CloneMap(meta.SharedBuffers)
	if r.sharedBuffers == nil {
		r.sharedBuffers = make(map[string]uint32)
	}
	r.DisplayCards = guest.CloneMap(meta.DisplayCards)
	r.ThreadTargets = guest.CloneMap(meta.ThreadTargets)
	r.currentThread = meta.CurrentThread
	r.stringBuffers = guest.CloneMap(meta.StringBuffers)
	r.inputStreams = make(
		map[uint32]*ktfInputStream,
		len(meta.InputStreams),
	)
	for instance, stream := range meta.InputStreams {
		r.inputStreams[instance] = &ktfInputStream{
			data:     append([]byte(nil), stream.Data...),
			position: stream.Position,
			mark:     stream.Mark,
		}
	}
	r.inputTargets = guest.CloneMap(meta.InputTargets)
	r.outputStreams = guest.CloneSliceMap(meta.OutputStreams)
	r.outputTargets = guest.CloneMap(meta.OutputTargets)
	r.files = restoreKTFFiles(meta.Files)
	r.FileData = guest.CloneSliceMap(meta.FileData)
	r.fileStreamTargets = guest.CloneMap(meta.FileStreamTargets)
	r.systemInputStream = meta.SystemInputStream
	r.systemPrintStream = meta.SystemPrintStream

	if err := restoreKTFImagesAndGraphics(r, meta); err != nil {
		return err
	}
	r.defaultFont = meta.DefaultFont
	r.ScreenGraphics = meta.ScreenGraphics
	r.wipicFramebuffers = restoreKTFWIPICFramebuffers(meta.WIPICFramebuffers)
	r.WipicScreenFramebuffer = meta.WIPICScreenFramebuffer
	r.WipicScreenPending = saved.WipicScreenPending
	r.wipicImages = make(
		map[uint32]*ktfWIPICImage,
		len(meta.WIPICImages),
	)
	for object, value := range meta.WIPICImages {
		r.wipicImages[object] = &ktfWIPICImage{
			object: value.Object, body: value.Body,
			framebuffer: value.Framebuffer, source: value.Source,
			frameIndex: value.FrameIndex,
			// The color key is derived from the restored pixels rather than
			// serialized, so an older save without the field still keys its
			// sprites correctly after loading.
			transparentKey: r.wipicImageColorKey(value.Framebuffer),
		}
	}
	r.wipicResources = guest.CloneSliceMap(meta.WIPICResources)
	r.wipicResourceIDs = guest.CloneMap(meta.WIPICResourceIDs)
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
	r.wipicSystemProperties = guest.CloneMap(meta.WIPICSystemProperties)
	r.wipicFiles = restoreKTFFiles(meta.WIPICFiles)
	r.nextWIPICFile = meta.NextWIPICFile
	r.dirtyCards = guest.CloneMap(meta.DirtyCards)
	r.paintInitializedCards = guest.CloneMap(meta.PaintInitializedCards)
	r.PresentCount = meta.PresentCount
	r.TickMS = meta.TickMS

	r.NativeParameterBase = meta.NativeParameterBase
	r.DeferThreads = meta.DeferThreads
	r.yieldRequested = meta.YieldRequested
	r.Tasks = make([]*Task, len(meta.Tasks))
	for index, task := range meta.Tasks {
		r.Tasks[index] = &Task{
			Context:         append([]byte(nil), task.Context...),
			exceptionFrame:  task.ExceptionFrame,
			LastJavaMethod:  task.LastJavaMethod,
			Done:            task.Done,
			presentOnReturn: task.PresentOnReturn,
			bestEffortPaint: task.BestEffortPaint,
			WipicTimer:      task.WIPICTimer,
			paintCard:       task.PaintCard,
			KeyCard:         task.KeyCard,
			layoutOnReturn:  task.LayoutOnReturn,
			childStartGrace: task.ChildStartGrace,
		}
		if len(saved.taskWakeAtMS) != 0 {
			r.Tasks[index].WakeAtMS = saved.taskWakeAtMS[index]
		}
	}
	for index, task := range meta.Tasks {
		if task.StartBlocker >= 0 {
			r.Tasks[index].startBlocker = r.Tasks[task.StartBlocker]
		}
	}
	r.PendingJavaCalls = make(
		[]ktfPendingJavaCall,
		len(meta.PendingJavaCalls),
	)
	for index, call := range meta.PendingJavaCalls {
		r.PendingJavaCalls[index] = ktfPendingJavaCall{
			instance: call.Instance, name: call.Name,
			descriptor: call.Descriptor,
			args:       append([]uint32(nil), call.Args...),
		}
	}
	r.taskCursor = int(meta.TaskCursor)
	r.activeTask = nil
	if meta.ActiveTask >= 0 {
		r.activeTask = r.Tasks[meta.ActiveTask]
	}
	r.ActiveInstructions = meta.ActiveInstructions
	r.executionDepth = int(meta.ExecutionDepth)
	r.PaintTasks = make(map[uint32]*Task, len(meta.PaintTasks))
	for _, value := range meta.PaintTasks {
		r.PaintTasks[value.Key] = r.Tasks[value.Task]
	}
	r.deferredPaintCards = make(
		map[*Task][]uint32,
		len(meta.DeferredPaintCards),
	)
	for _, value := range meta.DeferredPaintCards {
		r.deferredPaintCards[r.Tasks[value.Task]] =
			append([]uint32(nil), value.Cards...)
	}
	r.deferredShownCards = make(
		map[*Task]map[uint32]bool,
		len(meta.DeferredShownCards),
	)
	for _, value := range meta.DeferredShownCards {
		cards := make(map[uint32]bool, len(value.Cards))
		for _, card := range value.Cards {
			cards[card] = true
		}
		r.deferredShownCards[r.Tasks[value.Task]] = cards
	}
	*started = meta.Started
	return nil
}

func restoreKTFLWC(value ktfLWCSnapshot) *ktfLWCComponent {
	return &ktfLWCComponent{
		x: value.X, y: value.Y, width: value.Width, height: value.Height,
		preferredWidth:  value.PreferredWidth,
		preferredHeight: value.PreferredHeight,
		background:      value.Background, foreground: value.Foreground,
		Parent: value.Parent, card: value.Card, title: value.Title,
		command: value.Command, work: value.Work, focus: value.Focus,
		text: value.Text, gap: value.Gap, shown: value.Shown,
		valid: value.Valid, focused: value.Focused,
		vertical: value.Vertical, packed: value.Packed,
		annunciator: value.Annunciator, transparent: value.Transparent,
		progressValue: value.ProgressValue,
		progressMax:   value.ProgressMax, progressStep: value.ProgressStep,
		progressTop: value.ProgressTop, progressBottom: value.ProgressBottom,
		dialogType: value.DialogType, dialogTimeout: value.DialogTimeout,
		dialogAction: value.DialogAction,
		dialogOK:     value.DialogOK, dialogCancel: value.DialogCancel,
		progressInput: value.ProgressInput,
		font:          value.Font, image: value.Image,
		imageActive: value.ImageActive, group: value.Group,
		date: value.Date, mode: value.Mode, minimum: value.Minimum,
		viewAmount: value.ViewAmount, changeAmount: value.ChangeAmount,
		delay: value.Delay, activeIndex: value.ActiveIndex,
		selected: value.Selected,
	}
}

func restoreKTFDatabase(value ktfDatabaseSnapshot) *Database {
	return &Database{
		Name:       value.Name,
		RecordSize: value.RecordSize,
		Records:    guest.CloneByteSlices(value.Records),
	}
}

func restoreKTFFiles(values map[uint32]ktfFileSnapshot) map[uint32]*ktfFile {
	result := make(map[uint32]*ktfFile, len(values))
	for handle, file := range values {
		namespace := shared.Namespace(file.Namespace)
		if !namespace.Valid() {
			namespace = shared.NamespacePrivate
		}
		result[handle] = &ktfFile{
			namespace: namespace,
			name:      file.Name, position: file.Position,
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
	r *Runtime,
	meta ktfMetadataSnapshot,
) error {
	r.images = make(map[uint32]image.Image, len(meta.Images))
	for _, object := range meta.Images {
		surface := meta.ImageServices[object]
		descriptor, err := r.Services.Graphics.Descriptor(r.ServiceOwner, surface)
		if err != nil {
			return fmt.Errorf("restore KTF image 0x%08x: %w", object, err)
		}
		pixels, err := r.Services.Graphics.RGBA(r.ServiceOwner, surface)
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
	r.Graphics = make(map[uint32]*ktfGraphics, len(meta.Graphics))
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
		r.Graphics[instance] = &ktfGraphics{
			Target: drawTarget,
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
			origin: image.Pt(
				int(value.Origin[0]),
				int(value.Origin[1]),
			),
			translate: image.Pt(
				int(value.Translate[0]),
				int(value.Translate[1]),
			),
		}
	}
	if r.menuForegroundCompat != nil {
		// The compatibility cache is derived from live image objects and draw
		// calls. Rebuild it after restoring those objects instead of retaining
		// coordinates from the state that was active before this load.
		r.menuForegroundCompat.overlayImage = 0
		r.menuForegroundCompat.pending = nil
	}
	return nil
}
