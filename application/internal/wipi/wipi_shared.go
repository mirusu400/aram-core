package wipi

import (
	"bytes"
	"fmt"
	"path"
	"reflect"
	"sort"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

type PersistentState struct {
	owner       shared.OwnerID
	storage     shared.StoragePersistenceState
	Files       map[string][]byte
	Directories map[string]bool
	FileTimes   map[string]uint32
	Databases   map[string]*Database
}

func (r *Runtime) CapturePersistentState() (PersistentState, error) {
	if r == nil || r.Services == nil {
		return PersistentState{}, fmt.Errorf("public WIPI services are missing")
	}
	state := PersistentState{
		owner:       r.ServiceOwner,
		storage:     r.Services.Storage.ExportPersistence(),
		Files:       make(map[string][]byte),
		Directories: make(map[string]bool),
		FileTimes:   make(map[string]uint32),
		Databases:   make(map[string]*Database),
	}
	for _, directory := range state.storage.Directories {
		name, err := wipiPersistentGuestPath(
			directory.Namespace,
			directory.Path,
		)
		if err != nil {
			return PersistentState{}, err
		}
		state.Directories[name] = true
		if modified, ok := r.FileTimes[name]; ok {
			state.FileTimes[name] = modified
		}
	}
	for _, file := range state.storage.Files {
		name, err := wipiPersistentGuestPath(file.Namespace, file.Path)
		if err != nil {
			return PersistentState{}, err
		}
		if state.Directories[name] {
			return PersistentState{}, fmt.Errorf(
				"persistent WIPI path %q is both a file and directory",
				name,
			)
		}
		state.Files[name] = append([]byte(nil), file.Data...)
		if modified, ok := r.FileTimes[name]; ok {
			state.FileTimes[name] = modified
		}
	}
	if len(state.storage.RecordStores) != len(r.Databases) {
		return PersistentState{}, fmt.Errorf(
			"public WIPI database persistence count differs",
		)
	}
	for _, saved := range state.storage.RecordStores {
		database := r.Databases[saved.Name]
		if saved.Owner != state.owner || database == nil ||
			database.Name == "" || database.RecordSize == 0 ||
			database.RecordSize > maxWIPIString ||
			saved.NextID == 0 || saved.NextID > uint32(1<<31-1) {
			return PersistentState{}, fmt.Errorf(
				"public WIPI database %q has invalid persistent metadata",
				saved.Name,
			)
		}
		clone := &Database{
			Name:       database.Name,
			RecordSize: database.RecordSize,
			Mode:       database.Mode,
			NextRecord: int32(saved.NextID),
			Records:    make(map[int32][]byte, len(saved.Records)),
		}
		for _, record := range saved.Records {
			if record.ID > uint32(1<<31-1) ||
				uint64(len(record.Data)) != uint64(database.RecordSize) {
				return PersistentState{}, fmt.Errorf(
					"public WIPI database %q has invalid persistent record %d",
					saved.Name,
					record.ID,
				)
			}
			clone.Records[int32(record.ID)] = append(
				[]byte(nil),
				record.Data...,
			)
		}
		state.Databases[saved.Name] = clone
	}
	return state, nil
}

func (r *Runtime) RestorePersistentState(state PersistentState) error {
	if r == nil || r.Services == nil {
		return fmt.Errorf("public WIPI services are missing")
	}
	for index := range state.storage.RecordStores {
		if state.storage.RecordStores[index].Owner != state.owner {
			return fmt.Errorf(
				"public WIPI record store %q belongs to owner %d, want %d",
				state.storage.RecordStores[index].Name,
				state.storage.RecordStores[index].Owner,
				state.owner,
			)
		}
		state.storage.RecordStores[index].Owner = r.ServiceOwner
	}
	if err := r.Services.Storage.ImportPersistence(state.storage); err != nil {
		return fmt.Errorf("restore public WIPI persistence: %w", err)
	}
	r.Files = guest.CloneSliceMap(state.Files)
	r.Directories = map[string]bool{
		"/private": true,
		"/shared":  true,
		"/system":  true,
	}
	for name, exists := range state.Directories {
		if exists {
			r.Directories[name] = true
		}
	}
	r.FileTimes = guest.CloneMap(state.FileTimes)
	r.Databases = cloneDatabases(state.Databases)
	r.DatabaseServices = make(map[string]shared.ServiceID, len(r.Databases))
	for _, name := range guest.SortedStringKeys(r.Databases) {
		serviceID, err := r.Services.Storage.OpenRecordStore(
			r.ServiceOwner,
			name,
		)
		if err != nil {
			return fmt.Errorf(
				"reopen public WIPI database %q after Reset: %w",
				name,
				err,
			)
		}
		r.DatabaseServices[name] = serviceID
	}
	return nil
}

func wipiPersistentGuestPath(
	namespace shared.Namespace,
	servicePath string,
) (string, error) {
	var root string
	switch namespace {
	case shared.NamespacePrivate:
		root = "/private"
	case shared.NamespaceShared:
		root = "/shared"
	default:
		return "", fmt.Errorf(
			"non-persistent WIPI namespace %q in persistence",
			namespace,
		)
	}
	if servicePath == "/" {
		return root, nil
	}
	if servicePath == "" || servicePath[0] != '/' {
		return "", fmt.Errorf(
			"invalid persistent WIPI service path %q",
			servicePath,
		)
	}
	return root + servicePath, nil
}

func (r *Runtime) BeginServiceExecution() error {
	if _, err := r.Services.Coordinator.BeginQuantum(); err != nil {
		return err
	}
	return r.Services.Coordinator.Transition(
		r.ServiceOwner,
		shared.LifecycleRunning,
		r.Services.Clock.Monotonic(),
		r.Services.Events,
	)
}

func (r *Runtime) FinishServiceExecution(
	state machinecore.State,
	instructions uint64,
	fault string,
) error {
	adapter, err := r.Services.Coordinator.Adapter(r.ServiceOwner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle == shared.LifecycleRunning {
		if err := r.Services.Coordinator.Consume(
			r.ServiceOwner,
			instructions,
		); err != nil {
			return err
		}
	}
	if state == machinecore.StateFaulted {
		if fault == "" {
			fault = "guest execution fault"
		}
		return r.Services.Coordinator.Fault(
			r.ServiceOwner,
			fault,
			r.Services.Clock.Monotonic(),
			r.Services.Events,
		)
	}
	target := shared.LifecyclePaused
	if state == machinecore.StateStopped {
		target = shared.LifecycleStopped
	}
	adapter, err = r.Services.Coordinator.Adapter(r.ServiceOwner)
	if err != nil || adapter.Lifecycle == target {
		return err
	}
	return r.Services.Coordinator.Transition(
		r.ServiceOwner,
		target,
		r.Services.Clock.Monotonic(),
		r.Services.Events,
	)
}

// prepareServicesForSave reconciles adapter-side compatibility mirrors that
// tests and a few legacy call paths may update directly. Guest-neutral state
// remains authoritative in the serialized component container.
func (r *Runtime) prepareServicesForSave() ([]byte, error) {
	if r.Services == nil {
		return nil, fmt.Errorf("shared services are missing")
	}
	before := r.Services.Snapshot()
	surfaceBefore := guest.CloneMap(r.surfaceServices)
	timerBefore := guest.CloneMap(r.TimerServices)
	fileBefore := guest.CloneMap(r.fileServices)
	databaseBefore := guest.CloneMap(r.DatabaseServices)
	mediaBefore := guest.CloneMap(r.MediaServices)
	rollback := func() {
		_ = r.Services.Restore(before)
		r.surfaceServices = surfaceBefore
		r.TimerServices = timerBefore
		r.fileServices = fileBefore
		r.DatabaseServices = databaseBefore
		r.MediaServices = mediaBefore
	}
	fail := func(err error) ([]byte, error) {
		rollback()
		return nil, err
	}

	if r.TickMS > uint64((time.Duration(1<<63-1))/time.Millisecond) ||
		time.Duration(r.TickMS)*time.Millisecond != r.Services.Clock.Monotonic() {
		return fail(fmt.Errorf("adapter and shared clocks differ"))
	}
	r.Services.Device.SetNetworkAvailable(r.networkConnected)
	for _, handle := range guest.SortedUint32Keys(r.Framebuffers) {
		if err := r.syncFramebufferToService(r.Framebuffers[handle]); err != nil {
			return fail(err)
		}
	}
	resources := make(map[string][]byte, len(r.Resources))
	for name, resource := range r.Resources {
		if resource == nil {
			return fail(fmt.Errorf("resource %q is nil", name))
		}
		resources[name] = resource.Data
	}
	if err := r.Services.Storage.ReplacePackage(resources); err != nil {
		return fail(err)
	}
	for _, name := range guest.SortedStringKeys(r.Directories) {
		namespace, servicePath := wipiStoragePath(name)
		if servicePath == "/" {
			continue
		}
		if r.Services.Storage.DirectoryExists(namespace, servicePath) {
			continue
		}
		if namespace == shared.NamespacePackage {
			return fail(fmt.Errorf(
				"package directory %q has no resource",
				name,
			))
		}
		if err := r.Services.Storage.MakeDirectory(
			namespace,
			servicePath,
		); err != nil {
			return fail(err)
		}
	}
	for _, name := range guest.SortedStringKeys(r.Files) {
		data := r.Files[name]
		namespace, servicePath := wipiStoragePath(name)
		if namespace == shared.NamespacePackage {
			return fail(fmt.Errorf("mutable file %q uses the package namespace", name))
		}
		if err := r.Services.Storage.WriteFile(namespace, servicePath, data); err != nil {
			return fail(err)
		}
	}
	fileDescriptors := make([]int, 0, len(r.fileHandles))
	for descriptor := range r.fileHandles {
		fileDescriptors = append(fileDescriptors, int(descriptor))
	}
	sort.Ints(fileDescriptors)
	for _, rawDescriptor := range fileDescriptors {
		descriptor := int32(rawDescriptor)
		handle := r.fileHandles[descriptor]
		if r.fileServices[descriptor] != 0 {
			continue
		}
		mode := shared.OpenMode(0)
		if handle.readable {
			mode |= shared.OpenRead
		}
		if handle.writable {
			mode |= shared.OpenWrite
		}
		namespace, servicePath := wipiStoragePath(handle.path)
		serviceID, err := r.Services.Storage.Open(
			r.ServiceOwner,
			namespace,
			servicePath,
			mode,
		)
		if err != nil {
			return fail(err)
		}
		if _, err := r.Services.Storage.Seek(
			r.ServiceOwner,
			serviceID,
			int64(handle.offset),
			shared.SeekStart,
		); err != nil {
			return fail(err)
		}
		r.fileServices[descriptor] = serviceID
	}
	for _, key := range guest.SortedStringKeys(r.Databases) {
		database := r.Databases[key]
		if database == nil || database.NextRecord <= 0 {
			return fail(fmt.Errorf("database %q has invalid metadata", key))
		}
		serviceID := r.DatabaseServices[key]
		if serviceID == 0 {
			var err error
			serviceID, err = r.Services.Storage.CreateRecordStore(
				r.ServiceOwner,
				key,
			)
			if err != nil {
				return fail(err)
			}
			r.DatabaseServices[key] = serviceID
		}
		records := make(map[uint32][]byte, len(database.Records))
		for recordID, data := range database.Records {
			if recordID < 0 {
				return fail(fmt.Errorf("database %q has negative record ID", key))
			}
			records[uint32(recordID)] = data
		}
		if err := r.Services.Storage.ReplaceRecords(
			r.ServiceOwner,
			serviceID,
			uint32(database.NextRecord),
			records,
		); err != nil {
			return fail(err)
		}
	}
	for _, handle := range guest.SortedUint32Keys(r.MediaClips) {
		clip := r.MediaClips[handle]
		if clip == nil {
			return fail(fmt.Errorf("media clip 0x%08x is nil", handle))
		}
		serviceID := r.MediaServices[handle]
		if serviceID == 0 {
			var err error
			serviceID, err = r.Services.Media.CreateClip(
				r.ServiceOwner,
				string(clip.mediaType),
				uint64(max(0, int(clip.capacity))),
			)
			if err != nil {
				return fail(err)
			}
			r.MediaServices[handle] = serviceID
		}
		if err := r.Services.Media.Clear(r.ServiceOwner, serviceID); err != nil {
			return fail(err)
		}
		if _, err := r.Services.Media.Append(
			r.ServiceOwner,
			serviceID,
			clip.Data,
		); err != nil {
			return fail(err)
		}
		if err := r.Services.Media.SetClipGain(
			r.ServiceOwner,
			serviceID,
			uint8(clip.volume),
			false,
			0,
		); err != nil {
			return fail(err)
		}
		if clip.position >= 0 {
			_ = r.Services.Media.Seek(
				r.ServiceOwner,
				serviceID,
				time.Duration(clip.position)*time.Millisecond,
			)
		}
		switch clip.State {
		case 0:
			if err := r.Services.Media.Stop(r.ServiceOwner, serviceID); err != nil {
				return fail(err)
			}
		case 1:
			plays := int32(1)
			if clip.Repeat {
				plays = -1
			}
			if err := r.Services.Media.Play(r.ServiceOwner, serviceID, plays); err != nil {
				return fail(err)
			}
		case 2:
			if err := r.Services.Media.Play(r.ServiceOwner, serviceID, 1); err != nil {
				return fail(err)
			}
			if err := r.Services.Media.Pause(r.ServiceOwner, serviceID); err != nil {
				return fail(err)
			}
		case 3:
			// Recording remains adapter state until an explicit input
			// provider is configured.
		}
	}
	for _, address := range guest.SortedUint32Keys(r.Timers) {
		timer := r.Timers[address]
		serviceID := r.TimerServices[address]
		if serviceID == 0 {
			var err error
			serviceID, err = r.Services.Timers.Define(
				r.ServiceOwner,
				fmt.Sprintf("wipi.timer.%08x", address),
			)
			if err != nil {
				return fail(err)
			}
			r.TimerServices[address] = serviceID
		}
		if timer.Deadline > uint64((time.Duration(1<<63-1))/time.Millisecond) {
			return fail(fmt.Errorf("timer deadline overflows virtual time"))
		}
		if err := r.Services.Timers.Set(
			serviceID,
			r.ServiceOwner,
			time.Duration(timer.Deadline)*time.Millisecond,
			0,
			int64(address),
		); err != nil {
			return fail(err)
		}
	}
	state, err := r.Services.MarshalBinary()
	if err != nil {
		return fail(err)
	}
	return state, nil
}

func (r *Runtime) validateSavedServices(saved *SavedState) error {
	if saved.ServiceOwner == 0 || saved.ServiceOwner != r.ServiceOwner ||
		saved.serviceName != r.serviceName || len(saved.Services) == 0 {
		return fmt.Errorf("adapter identity mismatch")
	}
	candidate, err := shared.NewServices(r.serviceConfig)
	if err != nil {
		return err
	}
	if err := candidate.UnmarshalBinary(saved.Services); err != nil {
		return err
	}
	if !reflect.DeepEqual(candidate.Config, r.serviceConfig) {
		return fmt.Errorf("service configuration or profile mismatch")
	}
	adapter, err := candidate.Coordinator.Adapter(saved.ServiceOwner)
	if err != nil || adapter.Name != saved.serviceName {
		return fmt.Errorf("coordinator adapter identity mismatch")
	}
	if candidate.Clock.Monotonic() != time.Duration(saved.TickMS)*time.Millisecond {
		return fmt.Errorf("adapter and service clocks differ")
	}
	if candidate.Device.NetworkAvailable() != saved.networkConnected {
		return fmt.Errorf("adapter and service network states differ")
	}

	if len(saved.surfaceServices) != len(saved.Framebuffers) ||
		len(saved.fileServices) != len(saved.fileHandles) ||
		len(saved.DatabaseServices) != len(saved.Databases) ||
		len(saved.MediaServices) != len(saved.MediaClips) ||
		len(saved.serialServices) != len(saved.serialPorts) ||
		len(saved.socketServices) != len(saved.sockets) ||
		len(saved.httpServices) != len(saved.http) {
		return fmt.Errorf("adapter service-mapping count mismatch")
	}
	framebuffers := make(map[uint32]Framebuffer, len(saved.Framebuffers))
	for _, framebuffer := range saved.Framebuffers {
		framebuffers[framebuffer.Handle] = framebuffer
		serviceID := saved.surfaceServices[framebuffer.Handle]
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindSurface,
		); err != nil {
			return err
		}
		descriptor, err := candidate.Graphics.Descriptor(saved.ServiceOwner, serviceID)
		if err != nil {
			return err
		}
		format := shared.PixelBGRX8888
		if framebuffer.BitsPerPixel == 16 {
			format = shared.PixelRGB565
		}
		if descriptor.Width != int32(framebuffer.Width) ||
			descriptor.Height != int32(framebuffer.Height) ||
			descriptor.Format != format {
			return fmt.Errorf("surface geometry mismatch for 0x%08x", framebuffer.Handle)
		}
		start := uint64(framebuffer.Pixels - guest.HeapBase)
		size := uint64(framebuffer.Width) * uint64(framebuffer.Height) *
			uint64(framebuffer.bytesPerPixel())
		if framebuffer.Pixels < guest.HeapBase ||
			start+size < start || start+size > uint64(len(saved.heapMemory)) {
			return fmt.Errorf("surface memory range is invalid")
		}
		pixels, err := candidate.Graphics.Pixels(saved.ServiceOwner, serviceID)
		if err != nil || !bytes.Equal(pixels, saved.heapMemory[start:start+size]) {
			return fmt.Errorf("surface pixels differ for 0x%08x", framebuffer.Handle)
		}
	}
	if saved.ScreenHandle != 0 {
		if candidate.Graphics.Screen() != saved.surfaceServices[saved.ScreenHandle] {
			return fmt.Errorf("screen surface mapping mismatch")
		}
	}
	for handle, serviceID := range saved.assetServices {
		if saved.heapAllocations == nil ||
			!savedHeapContains(saved.heapAllocations, handle, wipiImageDescriptorSize) {
			return fmt.Errorf("asset handle 0x%08x is not allocated", handle)
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindImage,
		); err != nil {
			return err
		}
	}
	for address, serviceID := range saved.TimerServices {
		if !savedHeapContains(saved.heapAllocations, address, 28) {
			return fmt.Errorf("timer address 0x%08x is not allocated", address)
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindTimer,
		); err != nil {
			return err
		}
	}
	for address, timer := range saved.Timers {
		current, err := candidate.Timers.Get(
			saved.TimerServices[address],
			saved.ServiceOwner,
		)
		if err != nil || !current.Active ||
			current.Deadline != time.Duration(timer.Deadline)*time.Millisecond ||
			current.Value != int64(address) {
			return fmt.Errorf("active timer 0x%08x differs", address)
		}
	}
	if err := validateSavedWIPIStorage(candidate, saved); err != nil {
		return err
	}
	for handle, serviceID := range saved.MediaServices {
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindClip,
		); err != nil {
			return err
		}
		source, err := candidate.Media.Source(saved.ServiceOwner, serviceID)
		if err != nil || !bytes.Equal(source, saved.MediaClips[handle].Data) {
			return fmt.Errorf("media source differs for 0x%08x", handle)
		}
	}
	for descriptor, serviceID := range saved.serialServices {
		if saved.serialPorts[descriptor] == nil {
			return fmt.Errorf("serial mapping refers to an absent descriptor")
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindSerial,
		); err != nil {
			return err
		}
	}
	for descriptor, serviceID := range saved.socketServices {
		if saved.sockets[descriptor] == nil {
			return fmt.Errorf("socket mapping refers to an absent descriptor")
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindSocket,
		); err != nil {
			return err
		}
	}
	for descriptor, serviceID := range saved.httpServices {
		if saved.http[descriptor] == nil {
			return fmt.Errorf("HTTP mapping refers to an absent descriptor")
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindHTTP,
		); err != nil {
			return err
		}
	}
	saved.validatedServices = candidate
	return nil
}

func validateSavedWIPIStorage(
	candidate *shared.Services,
	saved *SavedState,
) error {
	type directoryKey struct {
		namespace shared.Namespace
		path      string
	}
	expectedDirectories := make(map[directoryKey]struct{})
	addParents := func(namespace shared.Namespace, name string) {
		for parent := path.Dir(name); parent != "/" && parent != "."; parent = path.Dir(parent) {
			expectedDirectories[directoryKey{
				namespace: namespace,
				path:      parent,
			}] = struct{}{}
		}
	}
	for name := range saved.Directories {
		namespace, servicePath := wipiStoragePath(name)
		if !candidate.Storage.DirectoryExists(namespace, servicePath) {
			return fmt.Errorf("directory %q differs", name)
		}
		if servicePath != "/" {
			expectedDirectories[directoryKey{
				namespace: namespace,
				path:      servicePath,
			}] = struct{}{}
		}
	}
	resources := make([]string, 0, len(saved.Resources))
	for name := range saved.Resources {
		resources = append(resources, name)
	}
	sort.Strings(resources)
	for _, name := range resources {
		data, err := candidate.Storage.ReadFile(shared.NamespacePackage, name)
		if err != nil || !bytes.Equal(data, saved.Resources[name].Data) {
			return fmt.Errorf("package resource %q differs", name)
		}
		normalized, err := candidate.Storage.NormalizePath(name)
		if err != nil {
			return fmt.Errorf("package resource %q has invalid path", name)
		}
		addParents(shared.NamespacePackage, normalized)
	}
	for name, expected := range saved.Files {
		namespace, servicePath := wipiStoragePath(name)
		data, err := candidate.Storage.ReadFile(namespace, servicePath)
		if err != nil || !bytes.Equal(data, expected) {
			return fmt.Errorf("mutable file %q differs", name)
		}
		addParents(namespace, servicePath)
	}
	openFiles := make(map[shared.ServiceID]shared.OpenFileState)
	storageState := candidate.Storage.Snapshot()
	if len(storageState.Directories) != len(expectedDirectories) {
		return fmt.Errorf("storage directory count differs")
	}
	for _, directory := range storageState.Directories {
		key := directoryKey{
			namespace: directory.Namespace,
			path:      directory.Path,
		}
		if _, ok := expectedDirectories[key]; !ok {
			return fmt.Errorf(
				"unexpected storage directory %s:%s",
				directory.Namespace,
				directory.Path,
			)
		}
	}
	for _, handle := range storageState.OpenFiles {
		openFiles[handle.ID] = handle
	}
	for descriptor, expected := range saved.fileHandles {
		serviceID := saved.fileServices[descriptor]
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindFile,
		); err != nil {
			return err
		}
		handle, ok := openFiles[serviceID]
		if !ok || handle.Position != uint64(expected.offset) {
			return fmt.Errorf("file descriptor %d position differs", descriptor)
		}
	}
	for key, database := range saved.Databases {
		serviceID := saved.DatabaseServices[key]
		if err := candidate.Registry.Validate(
			serviceID,
			saved.ServiceOwner,
			shared.KindRecordBase,
		); err != nil {
			return err
		}
		nextID, err := candidate.Storage.NextRecordID(saved.ServiceOwner, serviceID)
		if err != nil || nextID != uint32(database.NextRecord) {
			return fmt.Errorf("database %q next record differs", key)
		}
		ids, err := candidate.Storage.RecordIDs(saved.ServiceOwner, serviceID)
		if err != nil || len(ids) != len(database.Records) {
			return fmt.Errorf("database %q record count differs", key)
		}
		for _, recordID := range ids {
			data, err := candidate.Storage.Record(
				saved.ServiceOwner,
				serviceID,
				recordID,
			)
			if err != nil || !bytes.Equal(data, database.Records[int32(recordID)]) {
				return fmt.Errorf("database %q record %d differs", key, recordID)
			}
		}
	}
	return nil
}

func savedHeapContains(blocks []guest.Block, address, size uint32) bool {
	for _, block := range blocks {
		if block.Address == address && block.Size >= size {
			return true
		}
	}
	return false
}
