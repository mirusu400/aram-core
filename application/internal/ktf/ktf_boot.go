package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

// ProfileID is the default KTF handset profile.
const ProfileID = "wipi-1.2.1/ktf/generic"

// FrameDuration is the KTF presentation quantum.
const FrameDuration = (time.Second + 30) / 60

func NewRuntime(backend cpu.Backend, pkg ktf.Package) (*Runtime, error) {
	return NewRuntimeForProfile(
		backend,
		pkg,
		nil,
		ProfileID,
	)
}

func NewRuntimeForProfile(
	backend cpu.Backend,
	pkg ktf.Package,
	frame *image.RGBA,
	profileID string,
) (*Runtime, error) {
	if backend == nil {
		return nil, fmt.Errorf("initialize KTF runtime: CPU is nil")
	}
	if len(pkg.Client) == 0 {
		return nil, fmt.Errorf("initialize KTF runtime: client image is empty")
	}
	imageSize := uint64(len(pkg.Client)) + uint64(pkg.BSSSize)
	if imageSize > uint64(^uint32(0))-uint64(ImageBase) {
		return nil, fmt.Errorf("initialize KTF runtime: image range exceeds guest address space")
	}
	databaseStores := loadKTFDatabaseStores(pkg.Files)
	fileData := loadKTFPrivateFiles(pkg.JARName, pkg.Files)
	if profileID == "" {
		profileID = ProfileID
	}
	serviceConfig := shared.DefaultConfig()
	serviceConfig.Device.ProfileID = profileID
	serviceConfig.Device.Carrier = "ktf"
	serviceConfig.Device.Manufacturer = "LG"
	serviceConfig.Device.Model = "LG-KH1300"
	serviceConfig.Device.ScreenFormat = shared.PixelRGBA8888
	if frame != nil {
		serviceConfig.Device.ScreenWidth = int32(frame.Bounds().Dx())
		serviceConfig.Device.ScreenHeight = int32(frame.Bounds().Dy())
	}
	serviceConfig.Device.Capabilities = []shared.DeviceCapability{
		{Name: "audio", Enabled: true},
		{Name: "backlight", Enabled: true},
		{Name: "graphics", Enabled: true},
		{Name: "images", Enabled: true},
		{Name: "text", Enabled: true},
		{Name: "vibration", Enabled: true},
	}
	services, err := shared.NewServices(serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize KTF shared Services: %w", err)
	}
	owner, err := services.Coordinator.Register(
		"ktf",
		serviceConfig.Limits.Coordinator.MaxRunBudget,
	)
	if err != nil {
		return nil, fmt.Errorf("register KTF adapter: %w", err)
	}
	if err := services.Coordinator.Transition(
		owner,
		shared.LifecycleReady,
		services.Clock.Monotonic(),
		services.Events,
	); err != nil {
		return nil, fmt.Errorf("ready KTF adapter: %w", err)
	}
	if err := services.Storage.MountPackage(pkg.Resources); err != nil {
		return nil, fmt.Errorf("mount KTF package resources: %w", err)
	}
	fileNames := make([]string, 0, len(fileData))
	for name := range fileData {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		if err := services.Storage.WriteFile(
			shared.NamespacePrivate,
			name,
			fileData[name],
		); err != nil {
			return nil, fmt.Errorf("import KTF private file %q: %w", name, err)
		}
	}
	databaseServices := make(map[string]shared.ServiceID, len(databaseStores))
	databaseNames := make([]string, 0, len(databaseStores))
	for name := range databaseStores {
		databaseNames = append(databaseNames, name)
	}
	sort.Strings(databaseNames)
	for _, name := range databaseNames {
		store, err := services.Storage.CreateRecordStore(owner, name)
		if err != nil {
			return nil, fmt.Errorf("import KTF database %q: %w", name, err)
		}
		records := make(map[uint32][]byte, len(databaseStores[name].Records))
		for recordID, record := range databaseStores[name].Records {
			records[uint32(recordID)] = record
		}
		nextID := max(uint32(1), uint32(len(records)))
		if err := services.Storage.ReplaceRecords(
			owner,
			store,
			nextID,
			records,
		); err != nil {
			return nil, fmt.Errorf("import KTF database %q: %w", name, err)
		}
		databaseServices[name] = store
	}
	return &Runtime{
		CPU:                   backend,
		Pkg:                   pkg,
		ImageSz:               uint32(imageSize),
		frame:                 frame,
		Services:              services,
		serviceConfig:         services.Config,
		ServiceOwner:          owner,
		serviceName:           "ktf",
		imageServices:         make(map[uint32]shared.ServiceID),
		javaAssetServices:     make(map[uint32]shared.ServiceID),
		FontServices:          make(map[uint32]shared.ServiceID),
		GraphicsServices:      make(map[uint32]shared.ServiceID),
		wipicSurfaceServices:  make(map[uint32]shared.ServiceID),
		wipicAssetServices:    make(map[uint32]shared.ServiceID),
		wipicTimerServices:    make(map[uint32]shared.ServiceID),
		wipicMediaServices:    make(map[uint32]shared.ServiceID),
		clipServices:          make(map[uint32]shared.ServiceID),
		DatabaseServices:      databaseServices,
		fileServices:          make(map[uint32]shared.ServiceID),
		wipicFileServices:     make(map[uint32]shared.ServiceID),
		nextHostCall:          HostBase + 4,
		hostCalls:             make(map[uint32]ktfHostCall),
		JavaClasses:           make(map[string]uint32),
		JavaStrings:           make(map[uint32]string),
		javaClassObjs:         make(map[uint32]uint32),
		classObjTarget:        make(map[uint32]uint32),
		hostJavaClass:         make(map[uint32]bool),
		javaClassInit:         make(map[uint32]uint8),
		javaVTables:           make(map[uint32]uint32),
		javaVTableCapacity:    make(map[uint32]uint32),
		javaVTableClasses:     make(map[uint32]uint32),
		hostJavaVirtualSlots:  make(map[uint32]uint16),
		nextHostVirtualSlot:   ktfHostVirtualSlotBase,
		UnimplementedJava:     make(map[string]uint64),
		randomSeeds:           make(map[uint32]uint64),
		integerValues:         make(map[uint32]int32),
		longValues:            make(map[uint32]int64),
		throwableMessages:     make(map[uint32]uint32),
		dates:                 make(map[uint32]int64),
		Vectors:               make(map[uint32][]uint32),
		hashtables:            make(map[uint32]map[string]ktfHashtableEntry),
		enumerations:          make(map[uint32]*ktfEnumeration),
		clips:                 make(map[uint32]*ktfClip),
		listeners:             make(map[uint32]uint32),
		lwcEventData:          make(map[uint32]uint32),
		lwcChildren:           make(map[uint32][]uint32),
		lwcMaxLengths:         make(map[uint32]int32),
		lwcComponents:         make(map[uint32]*ktfLWCComponent),
		databases:             make(map[uint32]*Database),
		DatabaseStores:        databaseStores,
		DisplayCards:          make(map[uint32]uint32),
		ThreadTargets:         make(map[uint32]uint32),
		javaTimerTasks:        make(map[uint32]*Task),
		javaTimerTaskStates:   make(map[uint32]uint8),
		stringBuffers:         make(map[uint32]string),
		inputStreams:          make(map[uint32]*ktfInputStream),
		inputTargets:          make(map[uint32]uint32),
		outputStreams:         make(map[uint32][]byte),
		outputTargets:         make(map[uint32]uint32),
		files:                 make(map[uint32]*ktfFile),
		FileData:              fileData,
		fileStreamTargets:     make(map[uint32]uint32),
		images:                make(map[uint32]image.Image),
		Graphics:              make(map[uint32]*ktfGraphics),
		menuForegroundCompat:  newKTFMenuForegroundCompat(pkg),
		wipicFramebuffers:     make(map[uint32]*ktfWIPICFramebuffer),
		wipicImages:           make(map[uint32]*ktfWIPICImage),
		wipicResources:        make(map[uint32][]byte),
		wipicResourceIDs:      make(map[string]uint32),
		wipicMemory:           make(map[uint32]ktfWIPICMemory),
		wipicTimers:           make(map[uint32]*ktfWIPICTimer),
		wipicMediaClips:       make(map[uint32]*ktfWIPICMediaClip),
		wipicSystemProperties: make(map[string]string),
		wipicFiles:            make(map[uint32]*ktfFile),
		nextWIPICFile:         1,
		dirtyCards:            make(map[uint32]bool),
		paintInitializedCards: make(map[uint32]bool),
		PaintTasks:            make(map[uint32]*Task),
		deferredPaintCards:    make(map[*Task][]uint32),
		deferredShownCards:    make(map[*Task]map[uint32]bool),
	}, nil
}

func loadKTFPrivateFiles(jarName string, files map[string][]byte) map[string][]byte {
	privateFiles := make(map[string][]byte)
	jarName = path.Clean(strings.ReplaceAll(jarName, `\`, "/"))
	packageRoot := path.Dir(jarName)
	for archiveName, data := range files {
		name := path.Clean(strings.ReplaceAll(archiveName, `\`, "/"))
		relative := name
		if packageRoot != "." {
			prefix := packageRoot + "/"
			if len(name) <= len(prefix) ||
				!strings.EqualFold(name[:len(prefix)], prefix) {
				continue
			}
			relative = name[len(prefix):]
		}
		separator := strings.IndexByte(relative, '/')
		if separator < 0 || !strings.EqualFold(relative[:separator], "P") {
			continue
		}
		privateName := normalizeKTFFileName(relative[separator+1:])
		if privateName == "/" {
			continue
		}
		privateFiles[privateName] = bytes.Clone(data)
	}
	addKTFEmptyFunterPatchFiles(privateFiles)
	return privateFiles
}

func addKTFEmptyFunterPatchFiles(files map[string][]byte) {
	hasFunterDatabase := false
	existing := make(map[string]bool, len(files))
	for name := range files {
		lower := strings.ToLower(name)
		existing[lower] = true
		if lower == "/funter_dl.db" {
			hasFunterDatabase = true
		}
	}
	if !hasFunterDatabase {
		return
	}
	const (
		funterData = 1 << iota
		funterMap
		funterResource
		funterSprite
		funterBaseBundle = funterData | funterMap | funterResource | funterSprite
	)
	type bundle struct {
		stem string
		mask int
	}
	bundles := make(map[string]bundle)
	for name := range files {
		extension := strings.ToLower(path.Ext(name))
		var bit int
		switch extension {
		case ".dat":
			bit = funterData
		case ".map":
			bit = funterMap
		case ".res":
			bit = funterResource
		case ".spr":
			bit = funterSprite
		default:
			continue
		}
		stem := strings.TrimSuffix(name, path.Ext(name))
		key := strings.ToLower(stem)
		current := bundles[key]
		if current.stem == "" || extension == ".dat" {
			current.stem = stem
		}
		current.mask |= bit
		bundles[key] = current
	}
	for key, candidate := range bundles {
		patchName := key + ".pch"
		if candidate.mask != funterBaseBundle || existing[patchName] {
			continue
		}
		// Funter's native bundle reader unconditionally opens the optional
		// patch sidecar even when a distribution carries no patch entries.
		// A zero-entry file is four zero catalog words followed by the
		// two-word header of an empty raw block.
		files[candidate.stem+".pch"] = make([]byte, 6*4)
	}
}

func loadKTFDatabaseStores(files map[string][]byte) map[string]*Database {
	stores := make(map[string]*Database)
	normalized := make(map[string][]byte, len(files))
	for name, data := range files {
		name = path.Clean(strings.ReplaceAll(name, `\`, "/"))
		normalized[strings.ToLower(name)] = data
	}
	for originalName, indexData := range files {
		name := path.Clean(strings.ReplaceAll(originalName, `\`, "/"))
		if !strings.EqualFold(path.Ext(name), ".idx") ||
			len(indexData) < 13 ||
			!bytes.Equal(indexData[:5], []byte("qtpdb")) {
			continue
		}
		base := strings.TrimSuffix(name, path.Ext(name))
		databaseData, ok := normalized[strings.ToLower(base+".db")]
		if !ok {
			continue
		}
		recordSize := binary.BigEndian.Uint32(indexData[5:9])
		recordCount := binary.BigEndian.Uint32(indexData[9:13])
		required := uint64(recordSize) * uint64(recordCount)
		if recordSize == 0 || required > uint64(len(databaseData)) {
			continue
		}
		databaseName := strings.TrimSuffix(path.Base(name), path.Ext(name))
		store := &Database{
			Name:       databaseName,
			RecordSize: recordSize,
			Records:    make([][]byte, 0, recordCount),
		}
		for index := uint32(0); index < recordCount; index++ {
			start := uint64(index) * uint64(recordSize)
			end := start + uint64(recordSize)
			store.Records = append(
				store.Records,
				bytes.Clone(databaseData[int(start):int(end)]),
			)
		}
		stores[databaseName] = store
	}
	return stores
}

func (r *Runtime) MapImageAndHost() error {
	if r.Mapped {
		return nil
	}
	if err := r.CPU.Map(
		ImageBase,
		r.ImageSz,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		return fmt.Errorf("map KTF client image: %w", err)
	}
	if err := r.CPU.WriteMemory(ImageBase, r.Pkg.Client); err != nil {
		return fmt.Errorf("copy KTF client image: %w", err)
	}
	if err := r.CPU.Map(
		guest.DefaultStackBase,
		guest.DefaultStackSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map KTF application stack: %w", err)
	}
	if err := r.CPU.Map(
		HostBase,
		HostSize,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		return fmt.Errorf("map KTF host-call page: %w", err)
	}
	stubs := make([]byte, HostSize)
	for offset := 0; offset < len(stubs); offset += 4 {
		copy(stubs[offset:], []byte{0x00, 0xbe, 0x00, 0xbf})
	}
	if err := r.CPU.WriteMemory(HostBase, stubs); err != nil {
		return fmt.Errorf("install KTF host-call stubs: %w", err)
	}
	if err := r.CPU.Map(
		guest.HeapBase,
		guest.HeapSize,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		return fmt.Errorf("map KTF guest heap: %w", err)
	}
	r.Heap = guest.NewHeap(r.CPU, guest.HeapBase, guest.HeapSize)
	r.Mapped = true
	return nil
}

// resetMappedMemory installs a pristine KTF image into mappings already owned
// by the machine. It is the reset counterpart of mapImageAndHost; remapping is
// deliberately avoided because CPU backends reject overlapping regions.
func (r *Runtime) ResetMappedMemory() error {
	for _, region := range []struct {
		address uint32
		size    uint32
		label   string
	}{
		{ImageBase, r.ImageSz, "client image"},
		{guest.DefaultStackBase, guest.DefaultStackSize, "application stack"},
		{HostBase, HostSize, "host-call page"},
		{guest.HeapBase, guest.HeapSize, "guest heap"},
	} {
		if err := guest.ZeroMemory(r.CPU, region.address, region.size); err != nil {
			return fmt.Errorf("reset KTF %s: %w", region.label, err)
		}
	}
	if err := r.CPU.WriteMemory(ImageBase, r.Pkg.Client); err != nil {
		return fmt.Errorf("restore KTF client image: %w", err)
	}
	stubs := make([]byte, HostSize)
	for offset := 0; offset < len(stubs); offset += 4 {
		copy(stubs[offset:], []byte{0x00, 0xbe, 0x00, 0xbf})
	}
	if err := r.CPU.WriteMemory(HostBase, stubs); err != nil {
		return fmt.Errorf("restore KTF host-call stubs: %w", err)
	}
	r.Heap = guest.NewHeap(r.CPU, guest.HeapBase, guest.HeapSize)
	r.Mapped = true
	return nil
}

func (r *Runtime) Bootstrap(ctx context.Context) (cpu.Result, uint32, error) {
	result, address, err := r.call(
		ctx,
		ImageBase|1,
		[]uint32{r.Pkg.BSSSize},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return result, address, err
	}
	executable, err := r.inspectExecutable(address)
	if err != nil {
		return result, address, err
	}
	r.Exe = executable
	return result, address, nil
}

func (r *Runtime) Initialize(ctx context.Context) error {
	if r.Exe.InterfaceInit == 0 || r.Exe.ExecutableInit == 0 {
		return errors.New("KTF executable has no initialization procedures")
	}
	getInterface := r.RegisterHostCall("get_interface", ktfGetInterface)
	javaThrow := r.RegisterHostCall("java_throw", ktfJavaThrow)
	javaThrowObject := r.RegisterHostCall("java_throw_object", ktfJavaThrowObject)
	javaCheckType := r.RegisterHostCall("java_check_type", ktfJavaCheckType)
	javaNew := r.RegisterHostCall("java_new", ktfJavaNew)
	javaArrayNew := r.RegisterHostCall("java_array_new", ktfJavaArrayNew)
	javaClassLoad := r.RegisterHostCall("java_class_load", ktfJavaClassLoad)
	javaStringCopy := r.RegisterHostCall(
		"java_string_copy",
		ktfJavaStringCopy,
	)
	alloc := r.RegisterHostCall("alloc", ktfAlloc)

	param0, err := r.AllocateWords(1)
	if err != nil {
		return err
	}
	exceptionContext, err := r.AllocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		return err
	}
	r.exceptionContext = exceptionContext
	param1, err := r.AllocateWords(1)
	if err != nil {
		return err
	}
	r.javaEnvironment = param1
	if err := r.writeWords(param1, []uint32{exceptionContext}); err != nil {
		return err
	}
	param2, err := r.AllocateWords(3 + 128)
	if err != nil {
		return err
	}
	r.JvmContext = param2
	param3, err := r.AllocateWords(12)
	if err != nil {
		return err
	}
	if err := r.writeWords(param3, []uint32{
		0, 0, 0, 0,
		'Z', 'C', 'F', 'D', 'B', 'S', 'I', 'J',
	}); err != nil {
		return err
	}
	param4, err := r.AllocateWords(12)
	if err != nil {
		return err
	}
	if err := r.writeWords(param4, []uint32{
		getInterface,
		javaThrow,
		javaThrowObject,
		0,
		javaCheckType,
		javaNew,
		javaArrayNew,
		0,
		javaClassLoad,
		0,
		javaStringCopy,
		alloc,
	}); err != nil {
		return err
	}
	_, value, err := r.call(
		ctx,
		r.Exe.InterfaceInit,
		[]uint32{param0, param1, param2, param3, param4},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf("initialize KTF executable interface: %w", err)
	}
	if value != 0 {
		return fmt.Errorf("initialize KTF executable interface: status 0x%08x", value)
	}
	_, value, err = r.call(
		ctx,
		r.Exe.ExecutableInit,
		nil,
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf("initialize KTF application: %w", err)
	}
	if value != 0 {
		return fmt.Errorf("initialize KTF application: status 0x%08x", value)
	}
	return nil
}

func (r *Runtime) LoadClass(ctx context.Context, name string) (JavaClass, error) {
	if r.Exe.GetClass == 0 {
		return JavaClass{}, errors.New("KTF executable has no class lookup procedure")
	}
	candidates := []string{name}
	if strings.Contains(name, ".") {
		candidates = append(candidates, strings.ReplaceAll(name, ".", "/"))
	}
	for _, candidate := range candidates {
		nameAddress, err := r.allocateBytes([]byte(candidate), true)
		if err != nil {
			return JavaClass{}, err
		}
		_, address, err := r.call(
			ctx,
			r.Exe.GetClass,
			[]uint32{nameAddress},
			ktfBootstrapInstructionMax,
		)
		if err != nil {
			return JavaClass{}, fmt.Errorf(
				"load KTF Java class %q: %w",
				candidate,
				err,
			)
		}
		if address != 0 {
			if candidate != name {
				r.tracef("java_main_class_alias:%s=%s", name, candidate)
			}
			class, err := r.InspectJavaClass(address)
			if err != nil {
				return JavaClass{}, err
			}
			r.rememberRegisteredJavaClass(class.Name, class.Address)
			return class, nil
		}
	}
	return JavaClass{}, fmt.Errorf("KTF Java class %q was not found", name)
}

// ensureJavaInspectionCache resets the inspection caches whenever the class
// set changes. javaClassGeneration is bumped by class registration, host
// method patching, and savestate restore, so a stale parse cannot survive any
// of those.
func (r *Runtime) ensureJavaInspectionCache() {
	if r.javaClassInspections == nil ||
		r.javaInspectGen != r.javaClassGeneration {
		r.javaClassInspections = make(map[uint32]*ktfJavaClassInspection)
		r.javaMethodInspections = make(map[uint32]*ktfJavaMethodInspection)
		r.javaInspectGen = r.javaClassGeneration
	}
}

// readInspectionWords is readWords without the per-call slice allocations;
// class and method inspection runs on every host Java bridge call.
func (r *Runtime) readInspectionWords(address uint32, words []uint32) error {
	var buffer [36]byte
	data := buffer[:len(words)*4]
	if err := r.CPU.ReadMemory(address, data); err != nil {
		return fmt.Errorf("read KTF structure at 0x%08x: %w", address, err)
	}
	for index := range words {
		words[index] = binary.LittleEndian.Uint32(data[index*4:])
	}
	return nil
}

func (r *Runtime) InspectJavaClass(address uint32) (JavaClass, error) {
	r.ensureJavaInspectionCache()
	var classWords [5]uint32
	if err := r.readInspectionWords(address, classWords[:]); err != nil {
		return JavaClass{}, err
	}
	var descriptorWords [9]uint32
	if err := r.readInspectionWords(
		classWords[2],
		descriptorWords[:],
	); err != nil {
		return JavaClass{}, err
	}
	if cached, ok := r.javaClassInspections[address]; ok &&
		cached.classWords == classWords &&
		cached.descriptorWords == descriptorWords {
		return cached.class, nil
	}
	name, err := r.readCString(descriptorWords[0], 1024)
	if err != nil {
		return JavaClass{}, err
	}
	methodCount := uint16(descriptorWords[6])
	if methodCount > 4096 {
		return JavaClass{}, fmt.Errorf(
			"KTF Java class %q has excessive method count %d",
			name,
			methodCount,
		)
	}
	methods := make([]JavaMethod, 0, methodCount)
	for index := uint16(0); index < methodCount; index++ {
		methodAddress, err := r.ReadU32(descriptorWords[3] + uint32(index)*4)
		if err != nil {
			return JavaClass{}, err
		}
		if methodAddress == 0 {
			continue
		}
		method, err := r.InspectJavaMethod(methodAddress)
		if err != nil {
			return JavaClass{}, err
		}
		methods = append(methods, method)
	}
	entry := &ktfJavaClassInspection{
		class: JavaClass{
			Address:     address,
			Name:        name,
			Parent:      descriptorWords[2],
			VTable:      classWords[3],
			FieldSize:   uint16(descriptorWords[6] >> 16),
			AccessFlags: uint16(descriptorWords[7]),
			// Clamp the capacity so a caller appending to the returned
			// slice copies instead of writing into the cached array.
			Methods: methods[:len(methods):len(methods)],
		},
		classWords:      classWords,
		descriptorWords: descriptorWords,
	}
	r.javaClassInspections[address] = entry
	return entry.class, nil
}

func (r *Runtime) InspectJavaMethod(address uint32) (JavaMethod, error) {
	r.ensureJavaInspectionCache()
	var words [7]uint32
	if err := r.readInspectionWords(address, words[:]); err != nil {
		return JavaMethod{}, err
	}
	if cached, ok := r.javaMethodInspections[address]; ok &&
		cached.words == words {
		return cached.method, nil
	}
	fullName, err := r.readCString(words[3]+1, 4096)
	if err != nil {
		return JavaMethod{}, err
	}
	separator := strings.IndexByte(fullName, '+')
	if separator < 0 {
		return JavaMethod{}, fmt.Errorf(
			"KTF Java method at 0x%08x has malformed name %q",
			address,
			fullName,
		)
	}
	entry := &ktfJavaMethodInspection{
		method: JavaMethod{
			Address:           address,
			DeclaringClass:    words[1],
			Name:              fullName[separator+1:],
			Descriptor:        fullName[:separator],
			Body:              words[0],
			NativeBody:        words[2],
			ExceptionCount:    uint16(words[4]),
			ExceptionTableRaw: words[2],
			VTableIndex:       uint16(words[5]),
			AccessFlags:       uint16(words[5] >> 16),
		},
		words: words,
	}
	r.javaMethodInspections[address] = entry
	return entry.method, nil
}

func (r *Runtime) StartMainClass(ctx context.Context) error {
	class, err := r.LoadClass(ctx, r.Pkg.Descriptor.MainClass)
	if err != nil {
		return err
	}
	if err := r.ensureJavaClassInitialized(ctx, class); err != nil {
		return fmt.Errorf("initialize KTF MClass %q: %w", class.Name, err)
	}
	instance, err := r.NewJavaInstanceForClass(class)
	if err != nil {
		return err
	}
	constructor, ok := findKTFJavaMethod(class, "<init>", "()V")
	if !ok {
		return fmt.Errorf("KTF MClass %q has no default constructor", class.Name)
	}
	result, _, err := r.call(
		ctx,
		constructor.Body,
		[]uint32{0, instance},
		ktfBootstrapInstructionMax,
	)
	if err != nil {
		return fmt.Errorf(
			"construct KTF MClass %q at PC 0x%08x after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	mainName, err := r.NewJavaString(r.Pkg.Descriptor.MainClass)
	if err != nil {
		return err
	}
	args, err := r.newJavaReferenceArray("[Ljava/lang/String;", []uint32{mainName})
	if err != nil {
		return err
	}
	start, ok := findKTFJavaMethod(class, "startApp", "([Ljava/lang/String;)V")
	if !ok {
		return fmt.Errorf("KTF MClass %q has no startApp(String[])", class.Name)
	}
	if r.DeferThreads {
		task, err := r.NewTask(
			start.Body,
			[]uint32{0, instance, args},
			len(r.Tasks),
		)
		if err != nil {
			return fmt.Errorf("queue KTF MClass %q startApp: %w", class.Name, err)
		}
		if layout, ok := findKTFJavaMethod(class, "layout", "()V"); ok &&
			layout.Body != 0 {
			task.layoutOnReturn = instance
		}
		r.Tasks = append(r.Tasks, task)
		r.tracef(
			"java_task_queue:%s.startApp([Ljava/lang/String;)V:"+
				"instance=0x%08x:procedure=0x%08x",
			class.Name,
			instance,
			start.Body,
		)
		return nil
	}
	if result, _, err := r.call(
		ctx,
		start.Body,
		[]uint32{0, instance, args},
		ktfBootstrapInstructionMax,
	); err != nil {
		return fmt.Errorf(
			"start KTF MClass %q at PC 0x%08x after %d instructions: %w",
			class.Name,
			result.PC,
			result.Instructions,
			err,
		)
	}
	return nil
}
