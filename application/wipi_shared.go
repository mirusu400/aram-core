package application

import (
	"bytes"
	"fmt"
	"path"
	"reflect"
	"sort"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

type wipiPersistentState struct {
	owner       shared.OwnerID
	storage     shared.StoragePersistenceState
	files       map[string][]byte
	directories map[string]bool
	fileTimes   map[string]uint32
	databases   map[string]*wipiDatabase
}

func (r *wipiRuntime) capturePersistentState() (wipiPersistentState, error) {
	if r == nil || r.services == nil {
		return wipiPersistentState{}, fmt.Errorf("public WIPI services are missing")
	}
	state := wipiPersistentState{
		owner:       r.serviceOwner,
		storage:     r.services.Storage.ExportPersistence(),
		files:       make(map[string][]byte),
		directories: make(map[string]bool),
		fileTimes:   make(map[string]uint32),
		databases:   make(map[string]*wipiDatabase),
	}
	for _, directory := range state.storage.Directories {
		name, err := wipiPersistentGuestPath(
			directory.Namespace,
			directory.Path,
		)
		if err != nil {
			return wipiPersistentState{}, err
		}
		state.directories[name] = true
		if modified, ok := r.fileTimes[name]; ok {
			state.fileTimes[name] = modified
		}
	}
	for _, file := range state.storage.Files {
		name, err := wipiPersistentGuestPath(file.Namespace, file.Path)
		if err != nil {
			return wipiPersistentState{}, err
		}
		if state.directories[name] {
			return wipiPersistentState{}, fmt.Errorf(
				"persistent WIPI path %q is both a file and directory",
				name,
			)
		}
		state.files[name] = append([]byte(nil), file.Data...)
		if modified, ok := r.fileTimes[name]; ok {
			state.fileTimes[name] = modified
		}
	}
	if len(state.storage.RecordStores) != len(r.databases) {
		return wipiPersistentState{}, fmt.Errorf(
			"public WIPI database persistence count differs",
		)
	}
	for _, saved := range state.storage.RecordStores {
		database := r.databases[saved.Name]
		if saved.Owner != state.owner || database == nil ||
			database.name == "" || database.recordSize == 0 ||
			database.recordSize > maxWIPIString ||
			saved.NextID == 0 || saved.NextID > uint32(1<<31-1) {
			return wipiPersistentState{}, fmt.Errorf(
				"public WIPI database %q has invalid persistent metadata",
				saved.Name,
			)
		}
		clone := &wipiDatabase{
			name:       database.name,
			recordSize: database.recordSize,
			mode:       database.mode,
			nextRecord: int32(saved.NextID),
			records:    make(map[int32][]byte, len(saved.Records)),
		}
		for _, record := range saved.Records {
			if record.ID > uint32(1<<31-1) ||
				uint64(len(record.Data)) != uint64(database.recordSize) {
				return wipiPersistentState{}, fmt.Errorf(
					"public WIPI database %q has invalid persistent record %d",
					saved.Name,
					record.ID,
				)
			}
			clone.records[int32(record.ID)] = append(
				[]byte(nil),
				record.Data...,
			)
		}
		state.databases[saved.Name] = clone
	}
	return state, nil
}

func (r *wipiRuntime) restorePersistentState(state wipiPersistentState) error {
	if r == nil || r.services == nil {
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
		state.storage.RecordStores[index].Owner = r.serviceOwner
	}
	if err := r.services.Storage.ImportPersistence(state.storage); err != nil {
		return fmt.Errorf("restore public WIPI persistence: %w", err)
	}
	r.files = cloneByteMap(state.files)
	r.directories = map[string]bool{
		"/private": true,
		"/shared":  true,
		"/system":  true,
	}
	for name, exists := range state.directories {
		if exists {
			r.directories[name] = true
		}
	}
	r.fileTimes = cloneStringUint32Map(state.fileTimes)
	r.databases = cloneDatabases(state.databases)
	r.databaseServices = make(map[string]shared.ServiceID, len(r.databases))
	for _, name := range sortedStringKeys(r.databases) {
		serviceID, err := r.services.Storage.OpenRecordStore(
			r.serviceOwner,
			name,
		)
		if err != nil {
			return fmt.Errorf(
				"reopen public WIPI database %q after reset: %w",
				name,
				err,
			)
		}
		r.databaseServices[name] = serviceID
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

func (r *wipiRuntime) beginServiceExecution() error {
	if _, err := r.services.Coordinator.BeginQuantum(); err != nil {
		return err
	}
	return r.services.Coordinator.Transition(
		r.serviceOwner,
		shared.LifecycleRunning,
		r.services.Clock.Monotonic(),
		r.services.Events,
	)
}

func (r *wipiRuntime) finishServiceExecution(
	state machinecore.State,
	instructions uint64,
	fault string,
) error {
	adapter, err := r.services.Coordinator.Adapter(r.serviceOwner)
	if err != nil {
		return err
	}
	if adapter.Lifecycle == shared.LifecycleRunning {
		if err := r.services.Coordinator.Consume(
			r.serviceOwner,
			instructions,
		); err != nil {
			return err
		}
	}
	if state == machinecore.StateFaulted {
		if fault == "" {
			fault = "guest execution fault"
		}
		return r.services.Coordinator.Fault(
			r.serviceOwner,
			fault,
			r.services.Clock.Monotonic(),
			r.services.Events,
		)
	}
	target := shared.LifecyclePaused
	if state == machinecore.StateStopped {
		target = shared.LifecycleStopped
	}
	adapter, err = r.services.Coordinator.Adapter(r.serviceOwner)
	if err != nil || adapter.Lifecycle == target {
		return err
	}
	return r.services.Coordinator.Transition(
		r.serviceOwner,
		target,
		r.services.Clock.Monotonic(),
		r.services.Events,
	)
}

// prepareServicesForSave reconciles adapter-side compatibility mirrors that
// tests and a few legacy call paths may update directly. Guest-neutral state
// remains authoritative in the serialized component container.
func (r *wipiRuntime) prepareServicesForSave() ([]byte, error) {
	if r.services == nil {
		return nil, fmt.Errorf("shared services are missing")
	}
	before := r.services.Snapshot()
	surfaceBefore := cloneUint32ServiceMap(r.surfaceServices)
	timerBefore := cloneUint32ServiceMap(r.timerServices)
	fileBefore := cloneInt32ServiceMap(r.fileServices)
	databaseBefore := cloneStringServiceMap(r.databaseServices)
	mediaBefore := cloneUint32ServiceMap(r.mediaServices)
	rollback := func() {
		_ = r.services.Restore(before)
		r.surfaceServices = surfaceBefore
		r.timerServices = timerBefore
		r.fileServices = fileBefore
		r.databaseServices = databaseBefore
		r.mediaServices = mediaBefore
	}
	fail := func(err error) ([]byte, error) {
		rollback()
		return nil, err
	}

	if r.tickMS > uint64((time.Duration(1<<63-1))/time.Millisecond) ||
		time.Duration(r.tickMS)*time.Millisecond != r.services.Clock.Monotonic() {
		return fail(fmt.Errorf("adapter and shared clocks differ"))
	}
	r.services.Device.SetNetworkAvailable(r.networkConnected)
	for _, handle := range sortedUint32Keys(r.framebuffers) {
		if err := r.syncFramebufferToService(r.framebuffers[handle]); err != nil {
			return fail(err)
		}
	}
	resources := make(map[string][]byte, len(r.resources))
	for name, resource := range r.resources {
		if resource == nil {
			return fail(fmt.Errorf("resource %q is nil", name))
		}
		resources[name] = resource.data
	}
	if err := r.services.Storage.ReplacePackage(resources); err != nil {
		return fail(err)
	}
	for _, name := range sortedStringKeys(r.directories) {
		namespace, servicePath := wipiStoragePath(name)
		if servicePath == "/" {
			continue
		}
		if r.services.Storage.DirectoryExists(namespace, servicePath) {
			continue
		}
		if namespace == shared.NamespacePackage {
			return fail(fmt.Errorf(
				"package directory %q has no resource",
				name,
			))
		}
		if err := r.services.Storage.MakeDirectory(
			namespace,
			servicePath,
		); err != nil {
			return fail(err)
		}
	}
	for _, name := range sortedStringKeys(r.files) {
		data := r.files[name]
		namespace, servicePath := wipiStoragePath(name)
		if namespace == shared.NamespacePackage {
			return fail(fmt.Errorf("mutable file %q uses the package namespace", name))
		}
		if err := r.services.Storage.WriteFile(namespace, servicePath, data); err != nil {
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
		serviceID, err := r.services.Storage.Open(
			r.serviceOwner,
			namespace,
			servicePath,
			mode,
		)
		if err != nil {
			return fail(err)
		}
		if _, err := r.services.Storage.Seek(
			r.serviceOwner,
			serviceID,
			int64(handle.offset),
			shared.SeekStart,
		); err != nil {
			return fail(err)
		}
		r.fileServices[descriptor] = serviceID
	}
	for _, key := range sortedStringKeys(r.databases) {
		database := r.databases[key]
		if database == nil || database.nextRecord <= 0 {
			return fail(fmt.Errorf("database %q has invalid metadata", key))
		}
		serviceID := r.databaseServices[key]
		if serviceID == 0 {
			var err error
			serviceID, err = r.services.Storage.CreateRecordStore(
				r.serviceOwner,
				key,
			)
			if err != nil {
				return fail(err)
			}
			r.databaseServices[key] = serviceID
		}
		records := make(map[uint32][]byte, len(database.records))
		for recordID, data := range database.records {
			if recordID < 0 {
				return fail(fmt.Errorf("database %q has negative record ID", key))
			}
			records[uint32(recordID)] = data
		}
		if err := r.services.Storage.ReplaceRecords(
			r.serviceOwner,
			serviceID,
			uint32(database.nextRecord),
			records,
		); err != nil {
			return fail(err)
		}
	}
	for _, handle := range sortedUint32Keys(r.mediaClips) {
		clip := r.mediaClips[handle]
		if clip == nil {
			return fail(fmt.Errorf("media clip 0x%08x is nil", handle))
		}
		serviceID := r.mediaServices[handle]
		if serviceID == 0 {
			var err error
			serviceID, err = r.services.Media.CreateClip(
				r.serviceOwner,
				string(clip.mediaType),
				uint64(max(0, int(clip.capacity))),
			)
			if err != nil {
				return fail(err)
			}
			r.mediaServices[handle] = serviceID
		}
		if err := r.services.Media.Clear(r.serviceOwner, serviceID); err != nil {
			return fail(err)
		}
		if _, err := r.services.Media.Append(
			r.serviceOwner,
			serviceID,
			clip.data,
		); err != nil {
			return fail(err)
		}
		if err := r.services.Media.SetClipGain(
			r.serviceOwner,
			serviceID,
			uint8(clip.volume),
			false,
			0,
		); err != nil {
			return fail(err)
		}
		if clip.position >= 0 {
			_ = r.services.Media.Seek(
				r.serviceOwner,
				serviceID,
				time.Duration(clip.position)*time.Millisecond,
			)
		}
		switch clip.state {
		case 0:
			if err := r.services.Media.Stop(r.serviceOwner, serviceID); err != nil {
				return fail(err)
			}
		case 1:
			plays := int32(1)
			if clip.repeat {
				plays = -1
			}
			if err := r.services.Media.Play(r.serviceOwner, serviceID, plays); err != nil {
				return fail(err)
			}
		case 2:
			if err := r.services.Media.Play(r.serviceOwner, serviceID, 1); err != nil {
				return fail(err)
			}
			if err := r.services.Media.Pause(r.serviceOwner, serviceID); err != nil {
				return fail(err)
			}
		case 3:
			// Recording remains adapter state until an explicit input
			// provider is configured.
		}
	}
	for _, address := range sortedUint32Keys(r.timers) {
		timer := r.timers[address]
		serviceID := r.timerServices[address]
		if serviceID == 0 {
			var err error
			serviceID, err = r.services.Timers.Define(
				r.serviceOwner,
				fmt.Sprintf("wipi.timer.%08x", address),
			)
			if err != nil {
				return fail(err)
			}
			r.timerServices[address] = serviceID
		}
		if timer.deadline > uint64((time.Duration(1<<63-1))/time.Millisecond) {
			return fail(fmt.Errorf("timer deadline overflows virtual time"))
		}
		if err := r.services.Timers.Set(
			serviceID,
			r.serviceOwner,
			time.Duration(timer.deadline)*time.Millisecond,
			0,
			int64(address),
		); err != nil {
			return fail(err)
		}
	}
	state, err := r.services.MarshalBinary()
	if err != nil {
		return fail(err)
	}
	return state, nil
}

func (r *wipiRuntime) validateSavedServices(saved *wipiSavedState) error {
	if saved.serviceOwner == 0 || saved.serviceOwner != r.serviceOwner ||
		saved.serviceName != r.serviceName || len(saved.services) == 0 {
		return fmt.Errorf("adapter identity mismatch")
	}
	candidate, err := shared.NewServices(r.serviceConfig)
	if err != nil {
		return err
	}
	if err := candidate.UnmarshalBinary(saved.services); err != nil {
		return err
	}
	if !reflect.DeepEqual(candidate.Config, r.serviceConfig) {
		return fmt.Errorf("service configuration or profile mismatch")
	}
	adapter, err := candidate.Coordinator.Adapter(saved.serviceOwner)
	if err != nil || adapter.Name != saved.serviceName {
		return fmt.Errorf("coordinator adapter identity mismatch")
	}
	if candidate.Clock.Monotonic() != time.Duration(saved.tickMS)*time.Millisecond {
		return fmt.Errorf("adapter and service clocks differ")
	}
	if candidate.Device.NetworkAvailable() != saved.networkConnected {
		return fmt.Errorf("adapter and service network states differ")
	}

	if len(saved.surfaceServices) != len(saved.framebuffers) ||
		len(saved.fileServices) != len(saved.fileHandles) ||
		len(saved.databaseServices) != len(saved.databases) ||
		len(saved.mediaServices) != len(saved.mediaClips) ||
		len(saved.serialServices) != len(saved.serialPorts) ||
		len(saved.socketServices) != len(saved.sockets) ||
		len(saved.httpServices) != len(saved.http) {
		return fmt.Errorf("adapter service-mapping count mismatch")
	}
	framebuffers := make(map[uint32]wipiFramebuffer, len(saved.framebuffers))
	for _, framebuffer := range saved.framebuffers {
		framebuffers[framebuffer.handle] = framebuffer
		serviceID := saved.surfaceServices[framebuffer.handle]
		if err := candidate.Registry.Validate(
			serviceID,
			saved.serviceOwner,
			shared.KindSurface,
		); err != nil {
			return err
		}
		descriptor, err := candidate.Graphics.Descriptor(saved.serviceOwner, serviceID)
		if err != nil {
			return err
		}
		format := shared.PixelBGRX8888
		if framebuffer.bitsPerPixel == 16 {
			format = shared.PixelRGB565
		}
		if descriptor.Width != int32(framebuffer.width) ||
			descriptor.Height != int32(framebuffer.height) ||
			descriptor.Format != format {
			return fmt.Errorf("surface geometry mismatch for 0x%08x", framebuffer.handle)
		}
		start := uint64(framebuffer.pixels - guestHeapBase)
		size := uint64(framebuffer.width) * uint64(framebuffer.height) *
			uint64(framebuffer.bytesPerPixel())
		if framebuffer.pixels < guestHeapBase ||
			start+size < start || start+size > uint64(len(saved.heapMemory)) {
			return fmt.Errorf("surface memory range is invalid")
		}
		pixels, err := candidate.Graphics.Pixels(saved.serviceOwner, serviceID)
		if err != nil || !bytes.Equal(pixels, saved.heapMemory[start:start+size]) {
			return fmt.Errorf("surface pixels differ for 0x%08x", framebuffer.handle)
		}
	}
	if saved.screenHandle != 0 {
		if candidate.Graphics.Screen() != saved.surfaceServices[saved.screenHandle] {
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
			saved.serviceOwner,
			shared.KindImage,
		); err != nil {
			return err
		}
	}
	for address, serviceID := range saved.timerServices {
		if !savedHeapContains(saved.heapAllocations, address, 28) {
			return fmt.Errorf("timer address 0x%08x is not allocated", address)
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.serviceOwner,
			shared.KindTimer,
		); err != nil {
			return err
		}
	}
	for address, timer := range saved.timers {
		current, err := candidate.Timers.Get(
			saved.timerServices[address],
			saved.serviceOwner,
		)
		if err != nil || !current.Active ||
			current.Deadline != time.Duration(timer.deadline)*time.Millisecond ||
			current.Value != int64(address) {
			return fmt.Errorf("active timer 0x%08x differs", address)
		}
	}
	if err := validateSavedWIPIStorage(candidate, saved); err != nil {
		return err
	}
	for handle, serviceID := range saved.mediaServices {
		if err := candidate.Registry.Validate(
			serviceID,
			saved.serviceOwner,
			shared.KindClip,
		); err != nil {
			return err
		}
		source, err := candidate.Media.Source(saved.serviceOwner, serviceID)
		if err != nil || !bytes.Equal(source, saved.mediaClips[handle].data) {
			return fmt.Errorf("media source differs for 0x%08x", handle)
		}
	}
	for descriptor, serviceID := range saved.serialServices {
		if saved.serialPorts[descriptor] == nil {
			return fmt.Errorf("serial mapping refers to an absent descriptor")
		}
		if err := candidate.Registry.Validate(
			serviceID,
			saved.serviceOwner,
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
			saved.serviceOwner,
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
			saved.serviceOwner,
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
	saved *wipiSavedState,
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
	for name := range saved.directories {
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
	resources := make([]string, 0, len(saved.resources))
	for name := range saved.resources {
		resources = append(resources, name)
	}
	sort.Strings(resources)
	for _, name := range resources {
		data, err := candidate.Storage.ReadFile(shared.NamespacePackage, name)
		if err != nil || !bytes.Equal(data, saved.resources[name].data) {
			return fmt.Errorf("package resource %q differs", name)
		}
		normalized, err := candidate.Storage.NormalizePath(name)
		if err != nil {
			return fmt.Errorf("package resource %q has invalid path", name)
		}
		addParents(shared.NamespacePackage, normalized)
	}
	for name, expected := range saved.files {
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
			saved.serviceOwner,
			shared.KindFile,
		); err != nil {
			return err
		}
		handle, ok := openFiles[serviceID]
		if !ok || handle.Position != uint64(expected.offset) {
			return fmt.Errorf("file descriptor %d position differs", descriptor)
		}
	}
	for key, database := range saved.databases {
		serviceID := saved.databaseServices[key]
		if err := candidate.Registry.Validate(
			serviceID,
			saved.serviceOwner,
			shared.KindRecordBase,
		); err != nil {
			return err
		}
		nextID, err := candidate.Storage.NextRecordID(saved.serviceOwner, serviceID)
		if err != nil || nextID != uint32(database.nextRecord) {
			return fmt.Errorf("database %q next record differs", key)
		}
		ids, err := candidate.Storage.RecordIDs(saved.serviceOwner, serviceID)
		if err != nil || len(ids) != len(database.records) {
			return fmt.Errorf("database %q record count differs", key)
		}
		for _, recordID := range ids {
			data, err := candidate.Storage.Record(
				saved.serviceOwner,
				serviceID,
				recordID,
			)
			if err != nil || !bytes.Equal(data, database.records[int32(recordID)]) {
				return fmt.Errorf("database %q record %d differs", key, recordID)
			}
		}
	}
	return nil
}

func savedHeapContains(blocks []heapBlock, address, size uint32) bool {
	for _, block := range blocks {
		if block.address == address && block.size >= size {
			return true
		}
	}
	return false
}
