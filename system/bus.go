// Package system contains guest-neutral whole-system machine contracts.
package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/mirusu400/aram-core/cpu"
)

var (
	ErrInvalidRegion   = errors.New("invalid physical region")
	ErrRegionOverlap   = errors.New("physical region overlap")
	ErrInvalidWidth    = errors.New("invalid physical access width")
	ErrUnalignedAccess = errors.New("unaligned physical access")
	ErrRegionBoundary  = errors.New("physical access crosses a region boundary")
	ErrInvalidState    = errors.New("invalid physical bus state")
)

type Width uint8

const (
	Width8  Width = 1
	Width16 Width = 2
	Width32 Width = 4
)

func widthForSize(size int) (Width, bool) {
	switch size {
	case 1:
		return Width8, true
	case 2:
		return Width16, true
	case 4:
		return Width32, true
	default:
		return 0, false
	}
}

type Device interface {
	Reset() error
	Read(offset uint32, width Width) (uint32, error)
	Write(offset uint32, width Width, value uint32) error
}

type StatefulDevice interface {
	Device
	SaveState() ([]byte, error)
	LoadState([]byte) error
}

// SubsetStatefulDevice may accept a state from an older, compatible device
// profile when the caller explicitly requests a diagnostic subset restore.
// LoadState must retain the device's exact-profile contract.
type SubsetStatefulDevice interface {
	StatefulDevice
	LoadStateSubset([]byte) error
}

// ClockedDevices returns MMIO devices that advance from retired guest
// instructions, in physical-address order. A board can therefore add a
// profile-created timed peripheral without also exposing a title-specific
// construction hook to the execution loop.
func (b *Bus) ClockedDevices() []ClockedDevice {
	b.mu.Lock()
	defer b.mu.Unlock()
	devices := make([]ClockedDevice, 0)
	for index := range b.regions {
		if b.regions[index].kind != regionMMIO {
			continue
		}
		if device, ok := b.regions[index].device.(ClockedDevice); ok {
			devices = append(devices, device)
		}
	}
	return devices
}

type Fault struct {
	Region     string
	Address    uint32
	Width      Width
	Permission cpu.Permissions
	Err        error
}

// MMIOAccess is one completed device access as observed at the physical bus.
// Context identifies the guest instruction responsible for the access when
// the CPU backend uses the optional cpu.ContextMemoryBus contract.
type MMIOAccess struct {
	Context    cpu.MemoryAccessContext
	Region     string
	Address    uint32
	Offset     uint32
	Width      Width
	Permission cpu.Permissions
	Value      uint32
	Write      bool
	Err        error
}

type MMIOObserver func(MMIOAccess)

// MemoryAccess is one completed RAM, ROM, or MMIO access inside an explicitly
// observed physical range. It is intended for bounded watchpoints; normal bus
// execution does not allocate or call an observer.
type MemoryAccess struct {
	Context    cpu.MemoryAccessContext
	Region     string
	Address    uint32
	Offset     uint32
	Width      Width
	Permission cpu.Permissions
	Value      uint32
	Write      bool
	MMIO       bool
	Err        error
}

type MemoryObserver func(MemoryAccess)

func (f *Fault) Error() string {
	region := f.Region
	if region == "" {
		region = "unmapped"
	}
	return fmt.Sprintf(
		"physical %s access at 0x%08x width %d in %s: %v",
		permissionName(f.Permission),
		f.Address,
		f.Width,
		region,
		f.Err,
	)
}

func (f *Fault) Unwrap() error {
	return f.Err
}

// ExternalAbort reports only a genuinely unmapped physical address. MMIO
// register/width errors remain visible emulator faults so missing devices are
// not silently converted into guest behavior.
func (f *Fault) ExternalAbort() bool {
	return f.Region == "" && errors.Is(f.Err, cpu.ErrInvalidAddress)
}

type regionKind uint8

const (
	regionRAM regionKind = iota + 1
	regionROM
	regionMMIO
	regionSparseRAM
)

type region struct {
	name        string
	address     uint32
	size        uint32
	permissions cpu.Permissions
	kind        regionKind
	data        []byte
	initial     []byte
	device      Device
	sparse      *sparseRAM
}

func (r *region) end() uint64 {
	return uint64(r.address) + uint64(r.size)
}

type Bus struct {
	mu      sync.Mutex
	regions []region
	// lastRegion is the region the previous access resolved to. Guest memory
	// traffic has strong locality, so this answers almost every access and
	// keeps the binary search off the path a CPU takes for each load and store.
	lastRegion *region
	// observed is true while any observer is armed. Every access used to
	// evaluate three observer predicates and materialize the transferred value
	// for them; a machine that is not being watched now tests one bool.
	observed             bool
	mmioObserver         MMIOObserver
	memoryObserver       MemoryObserver
	memoryObserverStart  uint32
	memoryObserverEnd    uint64
	contextObserver      MemoryObserver
	contextObserverStart uint32
	contextObserverEnd   uint64
}

func NewBus() *Bus {
	return &Bus{}
}

// SetMMIOObserver replaces the optional diagnostic observer. The observer is
// called after each MMIO access and may safely inspect the bus, but it must not
// mutate the byte slices supplied to ReadContext or WriteContext.
func (b *Bus) SetMMIOObserver(observer MMIOObserver) {
	b.mu.Lock()
	b.mmioObserver = observer
	b.refreshObserved()
	b.mu.Unlock()
}

// SetMemoryObserver replaces the optional bounded physical-memory observer.
// A nil observer disables it. The callback is invoked after matching accesses
// and may safely inspect the bus.
func (b *Bus) SetMemoryObserver(address, size uint32, observer MemoryObserver) error {
	end := uint64(address) + uint64(size)
	if observer != nil && (size == 0 || end > 1<<32) {
		return fmt.Errorf("memory-observer range 0x%08x+0x%x: %w", address, size, ErrInvalidRegion)
	}
	b.mu.Lock()
	b.memoryObserver = observer
	b.memoryObserverStart = address
	b.memoryObserverEnd = end
	b.refreshObserved()
	b.mu.Unlock()
	return nil
}

// refreshObserved recomputes the armed-observer summary. Every writer of an
// observer has to call it, so the fast path can trust one field.
func (b *Bus) refreshObserved() {
	b.observed = b.mmioObserver != nil || b.memoryObserver != nil || b.contextObserver != nil
}

func (b *Bus) observesMemory(address uint32, size int) bool {
	return b.memoryObserver != nil &&
		uint64(address) < b.memoryObserverEnd &&
		uint64(b.memoryObserverStart) < uint64(address)+uint64(size)
}

// SetInstructionMemoryObserver replaces the optional observer for accesses
// attributed to a bounded guest instruction-address range. Unlike a physical
// watchpoint, it reports every RAM, ROM, or MMIO access made by matching
// instructions. A nil observer disables it.
func (b *Bus) SetInstructionMemoryObserver(
	address, size uint32,
	observer MemoryObserver,
) error {
	end := uint64(address) + uint64(size)
	if observer != nil && (size == 0 || end > 1<<32) {
		return fmt.Errorf("instruction-memory-observer range 0x%08x+0x%x: %w", address, size, ErrInvalidRegion)
	}
	b.mu.Lock()
	b.contextObserver = observer
	b.contextObserverStart = address
	b.contextObserverEnd = end
	b.refreshObserved()
	b.mu.Unlock()
	return nil
}

func (b *Bus) observesInstruction(context cpu.MemoryAccessContext) bool {
	return b.contextObserver != nil && context.Attributed &&
		uint64(context.InstructionAddress) >= uint64(b.contextObserverStart) &&
		uint64(context.InstructionAddress) < b.contextObserverEnd
}

func (b *Bus) MapRAM(name string, address, size uint32) error {
	return b.MapRAMImage(name, address, size, nil)
}

// MapRAMImage creates writable/executable RAM with deterministic reset bytes.
// It is used for images copied by a modeled or HLE boot stage; guest writes do
// not alter the reset image.
func (b *Bus) MapRAMImage(name string, address, size uint32, initial []byte) error {
	if uint64(size) > uint64(int(^uint(0)>>1)) {
		return fmt.Errorf("RAM region %q is too large: %w", name, ErrInvalidRegion)
	}
	if len(initial) > int(size) {
		return fmt.Errorf("RAM region %q initial image is too large: %w", name, ErrInvalidRegion)
	}
	data := make([]byte, int(size))
	copy(data, initial)
	return b.mapRegion(region{
		name: name, address: address, size: size,
		permissions: cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute,
		kind:        regionRAM, data: data, initial: append([]byte(nil), data...),
	})
}

// MapSparseRAM creates a zero-filled writable/executable address window whose
// storage is allocated one page at a time. It is intended for large coprocessor
// and banked-memory windows where allocating the entire physical span would be
// wasteful. Reset discards all allocated pages.
func (b *Bus) MapSparseRAM(name string, address, size uint32) error {
	return b.mapRegion(region{
		name: name, address: address, size: size,
		permissions: cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute,
		kind:        regionSparseRAM, sparse: newSparseRAM(),
	})
}

func (b *Bus) MapROM(name string, address uint32, data []byte) error {
	if uint64(len(data)) > uint64(^uint32(0)) {
		return ErrInvalidRegion
	}
	return b.mapRegion(region{
		name: name, address: address, size: uint32(len(data)),
		permissions: cpu.PermissionRead | cpu.PermissionExecute,
		kind:        regionROM, data: append([]byte(nil), data...),
	})
}

func (b *Bus) MapMMIO(name string, address, size uint32, device Device) error {
	if device == nil {
		return fmt.Errorf("MMIO region %q has no device: %w", name, ErrInvalidRegion)
	}
	return b.mapRegion(region{
		name: name, address: address, size: size,
		permissions: cpu.PermissionRead | cpu.PermissionWrite,
		kind:        regionMMIO, device: device,
	})
}

// mapRegion appends to the region slice, which can move its backing array, so
// the remembered pointer has to be dropped.
func (b *Bus) mapRegion(mapped region) error {
	b.lastRegion = nil
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(mapped.name) == "" || len(mapped.name) > 255 ||
		strings.IndexByte(mapped.name, 0) >= 0 || mapped.size == 0 ||
		uint64(mapped.address)+uint64(mapped.size) > 1<<32 {
		return fmt.Errorf("region %q: %w", mapped.name, ErrInvalidRegion)
	}
	for _, existing := range b.regions {
		if existing.name == mapped.name {
			return fmt.Errorf("duplicate region %q: %w", mapped.name, ErrInvalidRegion)
		}
		if uint64(mapped.address) < existing.end() && uint64(existing.address) < mapped.end() {
			return fmt.Errorf("region %q overlaps %q: %w", mapped.name, existing.name, ErrRegionOverlap)
		}
	}
	b.regions = append(b.regions, mapped)
	sort.Slice(b.regions, func(i, j int) bool { return b.regions[i].address < b.regions[j].address })
	return nil
}

func (b *Bus) Read(address uint32, destination []byte, permission cpu.Permissions) error {
	return b.ReadContext(cpu.MemoryAccessContext{}, address, destination, permission)
}

func (b *Bus) ReadContext(
	context cpu.MemoryAccessContext,
	address uint32,
	destination []byte,
	permission cpu.Permissions,
) error {
	b.mu.Lock()
	width, mapped, offset, err := b.resolve(address, len(destination), permission)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	if mapped.kind != regionMMIO {
		if mapped.kind == regionSparseRAM {
			mapped.sparse.read(offset, destination)
		} else {
			start := int(offset)
			copy(destination, mapped.data[start:start+len(destination)])
		}
		if !b.observed {
			b.mu.Unlock()
			return nil
		}
		observer := b.memoryObserver
		observed := b.observesMemory(address, len(destination))
		contextObserver := b.contextObserver
		contextObserved := b.observesInstruction(context)
		regionName := mapped.name
		regionOffset := offset
		value := valueOf(destination)
		b.mu.Unlock()
		access := MemoryAccess{
			Context: context, Region: regionName, Address: address, Offset: regionOffset,
			Width: width, Permission: permission, Value: value,
		}
		if observed {
			observer(access)
		}
		if contextObserved {
			contextObserver(access)
		}
		return nil
	}
	regionName := mapped.name
	deviceOffset := offset
	value, err := mapped.device.Read(deviceOffset, width)
	mmioObserver := b.mmioObserver
	memoryObserver := b.memoryObserver
	observed := b.observesMemory(address, len(destination))
	contextObserver := b.contextObserver
	contextObserved := b.observesInstruction(context)
	b.mu.Unlock()
	if err != nil {
		err = &Fault{Region: regionName, Address: address, Width: width, Permission: permission, Err: err}
	} else {
		putValue(destination, value)
	}
	if mmioObserver != nil {
		mmioObserver(MMIOAccess{
			Context: context, Region: regionName, Address: address, Offset: deviceOffset,
			Width: width, Permission: permission, Value: value, Err: err,
		})
	}
	access := MemoryAccess{
		Context: context, Region: regionName, Address: address, Offset: deviceOffset,
		Width: width, Permission: permission, Value: value, MMIO: true, Err: err,
	}
	if observed {
		memoryObserver(access)
	}
	if contextObserved {
		contextObserver(access)
	}
	return err
}

func (b *Bus) Write(address uint32, source []byte, permission cpu.Permissions) error {
	return b.WriteContext(cpu.MemoryAccessContext{}, address, source, permission)
}

func (b *Bus) WriteContext(
	context cpu.MemoryAccessContext,
	address uint32,
	source []byte,
	permission cpu.Permissions,
) error {
	b.mu.Lock()
	width, mapped, offset, err := b.resolve(address, len(source), permission)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	if mapped.kind != regionMMIO {
		if mapped.kind == regionSparseRAM {
			mapped.sparse.write(offset, source)
		} else {
			start := int(offset)
			copy(mapped.data[start:start+len(source)], source)
		}
		if !b.observed {
			b.mu.Unlock()
			return nil
		}
		observer := b.memoryObserver
		observed := b.observesMemory(address, len(source))
		contextObserver := b.contextObserver
		contextObserved := b.observesInstruction(context)
		regionName := mapped.name
		regionOffset := offset
		value := valueOf(source)
		b.mu.Unlock()
		access := MemoryAccess{
			Context: context, Region: regionName, Address: address, Offset: regionOffset,
			Width: width, Permission: permission, Value: value, Write: true,
		}
		if observed {
			observer(access)
		}
		if contextObserved {
			contextObserver(access)
		}
		return nil
	}
	regionName := mapped.name
	deviceOffset := offset
	value := valueOf(source)
	err = mapped.device.Write(deviceOffset, width, value)
	mmioObserver := b.mmioObserver
	memoryObserver := b.memoryObserver
	observed := b.observesMemory(address, len(source))
	contextObserver := b.contextObserver
	contextObserved := b.observesInstruction(context)
	b.mu.Unlock()
	if err != nil {
		err = &Fault{Region: regionName, Address: address, Width: width, Permission: permission, Err: err}
	}
	if mmioObserver != nil {
		mmioObserver(MMIOAccess{
			Context: context, Region: regionName, Address: address, Offset: deviceOffset,
			Width: width, Permission: permission, Value: value, Write: true, Err: err,
		})
	}
	access := MemoryAccess{
		Context: context, Region: regionName, Address: address, Offset: deviceOffset,
		Width: width, Permission: permission, Value: value, Write: true, MMIO: true, Err: err,
	}
	if observed {
		memoryObserver(access)
	}
	if contextObserved {
		contextObserver(access)
	}
	return err
}

func (b *Bus) resolve(
	address uint32,
	size int,
	permission cpu.Permissions,
) (Width, *region, uint32, error) {
	width, ok := widthForSize(size)
	if !ok {
		return 0, nil, 0, &Fault{Address: address, Permission: permission, Err: ErrInvalidWidth}
	}
	if address%uint32(width) != 0 {
		return width, nil, 0, &Fault{Address: address, Width: width, Permission: permission, Err: ErrUnalignedAccess}
	}
	mapped := b.lastRegion
	if mapped == nil || address < mapped.address || uint64(address) >= mapped.end() {
		index := sort.Search(len(b.regions), func(index int) bool { return uint64(address) < b.regions[index].end() })
		if index >= len(b.regions) || address < b.regions[index].address {
			return width, nil, 0, &Fault{Address: address, Width: width, Permission: permission, Err: cpu.ErrInvalidAddress}
		}
		mapped = &b.regions[index]
		b.lastRegion = mapped
	}
	if uint64(address)+uint64(size) > mapped.end() {
		return width, nil, 0, &Fault{Region: mapped.name, Address: address, Width: width, Permission: permission, Err: ErrRegionBoundary}
	}
	if mapped.permissions&permission != permission {
		return width, nil, 0, &Fault{Region: mapped.name, Address: address, Width: width, Permission: permission, Err: cpu.ErrPermissionDenied}
	}
	return width, mapped, address - mapped.address, nil
}

func (b *Bus) Reset() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index := range b.regions {
		mapped := &b.regions[index]
		switch mapped.kind {
		case regionRAM:
			copy(mapped.data, mapped.initial)
		case regionSparseRAM:
			mapped.sparse.reset()
		case regionMMIO:
			if err := mapped.device.Reset(); err != nil {
				return fmt.Errorf("reset device %q: %w", mapped.name, err)
			}
		}
	}
	return nil
}

func (b *Bus) SaveState() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	type component struct {
		region *region
		state  []byte
	}
	var components []component
	for index := range b.regions {
		mapped := &b.regions[index]
		switch mapped.kind {
		case regionRAM:
			components = append(components, component{region: mapped, state: append([]byte(nil), mapped.data...)})
		case regionSparseRAM:
			state, err := mapped.sparse.saveState(mapped.size)
			if err != nil {
				return nil, fmt.Errorf("save sparse RAM %q: %w", mapped.name, err)
			}
			components = append(components, component{region: mapped, state: state})
		case regionMMIO:
			stateful, ok := mapped.device.(StatefulDevice)
			if !ok {
				return nil, fmt.Errorf("device %q does not support state", mapped.name)
			}
			state, err := stateful.SaveState()
			if err != nil {
				return nil, fmt.Errorf("save device %q: %w", mapped.name, err)
			}
			components = append(components, component{region: mapped, state: state})
		}
	}
	var output bytes.Buffer
	output.WriteString("ARBS")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(components)))
	for _, component := range components {
		if uint64(len(component.state)) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("state component %q is too large", component.region.name)
		}
		_ = binary.Write(&output, binary.LittleEndian, uint16(len(component.region.name)))
		output.WriteString(component.region.name)
		output.WriteByte(byte(component.region.kind))
		output.WriteByte(0)
		_ = binary.Write(&output, binary.LittleEndian, component.region.address)
		_ = binary.Write(&output, binary.LittleEndian, component.region.size)
		_ = binary.Write(&output, binary.LittleEndian, uint32(len(component.state)))
		output.Write(component.state)
	}
	return output.Bytes(), nil
}

func (b *Bus) LoadState(state []byte) error {
	return b.loadState(state, false)
}

// LoadStateSubset restores a state whose regions are a strict subset of the
// current bus topology. Every serialized component must still match by name,
// kind, address, size, and device-specific state contract. Newly added regions
// remain in their current state. This is intended for explicitly versioned
// diagnostic snapshots; normal machine snapshots should use LoadState.
func (b *Bus) LoadStateSubset(state []byte) error {
	return b.loadState(state, true)
}

func (b *Bus) loadState(state []byte, allowMissingRegions bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	reader := bytes.NewReader(state)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "ARBS" {
		return ErrInvalidState
	}
	var version, count uint32
	if binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &count) != nil {
		return ErrInvalidState
	}
	var expectedCount uint32
	for index := range b.regions {
		if b.regions[index].kind == regionRAM ||
			b.regions[index].kind == regionSparseRAM ||
			b.regions[index].kind == regionMMIO {
			expectedCount++
		}
	}
	if count > expectedCount || !allowMissingRegions && count != expectedCount {
		return ErrInvalidState
	}
	type restored struct {
		region *region
		state  []byte
		sparse *sparseRAM
	}
	items := make([]restored, 0, count)
	seen := make(map[string]struct{}, count)
	for component := uint32(0); component < count; component++ {
		var nameLength uint16
		if binary.Read(reader, binary.LittleEndian, &nameLength) != nil || nameLength == 0 {
			return ErrInvalidState
		}
		name := make([]byte, nameLength)
		if _, err := io.ReadFull(reader, name); err != nil {
			return ErrInvalidState
		}
		kind, err := reader.ReadByte()
		if err != nil {
			return ErrInvalidState
		}
		if _, err := reader.ReadByte(); err != nil {
			return ErrInvalidState
		}
		var address, size, stateLength uint32
		if binary.Read(reader, binary.LittleEndian, &address) != nil ||
			binary.Read(reader, binary.LittleEndian, &size) != nil ||
			binary.Read(reader, binary.LittleEndian, &stateLength) != nil ||
			uint64(stateLength) > uint64(reader.Len()) {
			return ErrInvalidState
		}
		mapped := b.regionByIdentity(string(name), regionKind(kind), address, size)
		if mapped == nil {
			return ErrInvalidState
		}
		if _, duplicate := seen[mapped.name]; duplicate {
			return ErrInvalidState
		}
		seen[mapped.name] = struct{}{}
		componentState := make([]byte, stateLength)
		if _, err := io.ReadFull(reader, componentState); err != nil {
			return ErrInvalidState
		}
		if mapped.kind == regionRAM && len(componentState) != len(mapped.data) {
			return ErrInvalidState
		}
		var sparse *sparseRAM
		if mapped.kind == regionSparseRAM {
			decoded, decodeErr := decodeSparseRAMState(mapped.size, componentState)
			if decodeErr != nil {
				return ErrInvalidState
			}
			sparse = decoded
		}
		if mapped.kind == regionMMIO {
			if _, ok := mapped.device.(StatefulDevice); !ok {
				return ErrInvalidState
			}
		}
		items = append(items, restored{region: mapped, state: componentState, sparse: sparse})
	}
	if reader.Len() != 0 {
		return ErrInvalidState
	}
	previous := make([]restored, 0, len(items))
	for _, item := range items {
		if item.region.kind == regionRAM {
			previous = append(previous, restored{
				region: item.region,
				state:  append([]byte(nil), item.region.data...),
			})
			continue
		}
		if item.region.kind == regionSparseRAM {
			before, err := item.region.sparse.saveState(item.region.size)
			if err != nil {
				return fmt.Errorf("save sparse RAM %q before restore: %w", item.region.name, err)
			}
			previous = append(previous, restored{region: item.region, state: before})
			continue
		}
		stateful := item.region.device.(StatefulDevice)
		before, err := stateful.SaveState()
		if err != nil {
			return fmt.Errorf("save device %q before restore: %w", item.region.name, err)
		}
		previous = append(previous, restored{region: item.region, state: before})
	}
	rollback := func() {
		for _, item := range previous {
			switch item.region.kind {
			case regionRAM:
				copy(item.region.data, item.state)
			case regionSparseRAM:
				restored, err := decodeSparseRAMState(item.region.size, item.state)
				if err == nil {
					item.region.sparse = restored
				}
			default:
				_ = item.region.device.(StatefulDevice).LoadState(item.state)
			}
		}
	}
	for _, item := range items {
		if item.region.kind == regionRAM {
			copy(item.region.data, item.state)
			continue
		}
		if item.region.kind == regionSparseRAM {
			item.region.sparse = item.sparse
			continue
		}
		stateful, ok := item.region.device.(StatefulDevice)
		if !ok {
			rollback()
			return ErrInvalidState
		}
		var err error
		if allowMissingRegions {
			if subset, ok := item.region.device.(SubsetStatefulDevice); ok {
				err = subset.LoadStateSubset(item.state)
			} else {
				err = stateful.LoadState(item.state)
			}
		} else {
			err = stateful.LoadState(item.state)
		}
		if err != nil {
			rollback()
			return fmt.Errorf("load device %q: %w", item.region.name, err)
		}
	}
	return nil
}

func (b *Bus) regionByIdentity(name string, kind regionKind, address, size uint32) *region {
	for index := range b.regions {
		mapped := &b.regions[index]
		if mapped.name == name && mapped.kind == kind && mapped.address == address && mapped.size == size {
			return mapped
		}
	}
	return nil
}

func valueOf(data []byte) uint32 {
	switch len(data) {
	case 1:
		return uint32(data[0])
	case 2:
		return uint32(binary.LittleEndian.Uint16(data))
	default:
		return binary.LittleEndian.Uint32(data)
	}
}

func putValue(data []byte, value uint32) {
	switch len(data) {
	case 1:
		data[0] = byte(value)
	case 2:
		binary.LittleEndian.PutUint16(data, uint16(value))
	default:
		binary.LittleEndian.PutUint32(data, value)
	}
}

func permissionName(permission cpu.Permissions) string {
	switch permission {
	case cpu.PermissionRead:
		return "read"
	case cpu.PermissionWrite:
		return "write"
	case cpu.PermissionExecute:
		return "execute"
	default:
		return fmt.Sprintf("permission-0x%x", permission)
	}
}

var _ cpu.MemoryBus = (*Bus)(nil)
