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

type regionKind uint8

const (
	regionRAM regionKind = iota + 1
	regionROM
	regionMMIO
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
}

func (r *region) end() uint64 {
	return uint64(r.address) + uint64(r.size)
}

type Bus struct {
	mu           sync.Mutex
	regions      []region
	mmioObserver MMIOObserver
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
	b.mu.Unlock()
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

func (b *Bus) mapRegion(mapped region) error {
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
		copy(destination, mapped.data[offset:offset+len(destination)])
		b.mu.Unlock()
		return nil
	}
	regionName := mapped.name
	deviceOffset := uint32(offset)
	value, err := mapped.device.Read(deviceOffset, width)
	observer := b.mmioObserver
	b.mu.Unlock()
	if err != nil {
		err = &Fault{Region: regionName, Address: address, Width: width, Permission: permission, Err: err}
	} else {
		putValue(destination, value)
	}
	if observer != nil {
		observer(MMIOAccess{
			Context: context, Region: regionName, Address: address, Offset: deviceOffset,
			Width: width, Permission: permission, Value: value, Err: err,
		})
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
		copy(mapped.data[offset:offset+len(source)], source)
		b.mu.Unlock()
		return nil
	}
	regionName := mapped.name
	deviceOffset := uint32(offset)
	value := valueOf(source)
	err = mapped.device.Write(deviceOffset, width, value)
	observer := b.mmioObserver
	b.mu.Unlock()
	if err != nil {
		err = &Fault{Region: regionName, Address: address, Width: width, Permission: permission, Err: err}
	}
	if observer != nil {
		observer(MMIOAccess{
			Context: context, Region: regionName, Address: address, Offset: deviceOffset,
			Width: width, Permission: permission, Value: value, Write: true, Err: err,
		})
	}
	return err
}

func (b *Bus) resolve(
	address uint32,
	size int,
	permission cpu.Permissions,
) (Width, *region, int, error) {
	width, ok := widthForSize(size)
	if !ok {
		return 0, nil, 0, &Fault{Address: address, Permission: permission, Err: ErrInvalidWidth}
	}
	if address%uint32(width) != 0 {
		return width, nil, 0, &Fault{Address: address, Width: width, Permission: permission, Err: ErrUnalignedAccess}
	}
	index := sort.Search(len(b.regions), func(index int) bool { return uint64(address) < b.regions[index].end() })
	if index >= len(b.regions) || address < b.regions[index].address {
		return width, nil, 0, &Fault{Address: address, Width: width, Permission: permission, Err: cpu.ErrInvalidAddress}
	}
	mapped := &b.regions[index]
	if uint64(address)+uint64(size) > mapped.end() {
		return width, nil, 0, &Fault{Region: mapped.name, Address: address, Width: width, Permission: permission, Err: ErrRegionBoundary}
	}
	if mapped.permissions&permission != permission {
		return width, nil, 0, &Fault{Region: mapped.name, Address: address, Width: width, Permission: permission, Err: cpu.ErrPermissionDenied}
	}
	return width, mapped, int(address - mapped.address), nil
}

func (b *Bus) Reset() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index := range b.regions {
		mapped := &b.regions[index]
		switch mapped.kind {
		case regionRAM:
			copy(mapped.data, mapped.initial)
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
		if b.regions[index].kind == regionRAM || b.regions[index].kind == regionMMIO {
			expectedCount++
		}
	}
	if count != expectedCount {
		return ErrInvalidState
	}
	type restored struct {
		region *region
		state  []byte
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
		if mapped.kind == regionMMIO {
			if _, ok := mapped.device.(StatefulDevice); !ok {
				return ErrInvalidState
			}
		}
		items = append(items, restored{region: mapped, state: componentState})
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
		stateful := item.region.device.(StatefulDevice)
		before, err := stateful.SaveState()
		if err != nil {
			return fmt.Errorf("save device %q before restore: %w", item.region.name, err)
		}
		previous = append(previous, restored{region: item.region, state: before})
	}
	rollback := func() {
		for _, item := range previous {
			if item.region.kind == regionRAM {
				copy(item.region.data, item.state)
			} else {
				_ = item.region.device.(StatefulDevice).LoadState(item.state)
			}
		}
	}
	for _, item := range items {
		if item.region.kind == regionRAM {
			copy(item.region.data, item.state)
			continue
		}
		stateful, ok := item.region.device.(StatefulDevice)
		if !ok {
			rollback()
			return ErrInvalidState
		}
		if err := stateful.LoadState(item.state); err != nil {
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
