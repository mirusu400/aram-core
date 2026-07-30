package wipi

import (
	"encoding/binary"
	"fmt"
)

const (
	SystemBase           = uint32(0x01000000)
	SystemSize           = uint32(0x00010000)
	ImportPointerAddress = uint32(0x01001000)
	ProcessHolderAddress = uint32(0x01002000)
	ImportRootAddress    = uint32(0x01003000)
	PackageTableBase     = uint32(0x01004000)
	PackageTableStride   = uint32(0x00000100)
	TrampolineBase       = uint32(0x01100000)
	TrampolineSize       = uint32(0x00010000)
)

var rootFields = map[string]uint32{
	"MC_KNL":  0x00,
	"MC_GRP":  0x04,
	"MC_FS":   0x0c,
	"MC_NET":  0x14,
	"MC_UTIL": 0x18,
	"MC_HTTP": 0x2c,
	"CSTDLIB": 0x38,
	"MC_UIC":  0x44,
	"MC_MDA":  0x4c,
}

// Layout is a deterministic image of the process-global WIPI import
// indirection recovered from the reference runtime.
type Layout struct {
	System          []byte
	Trampolines     []byte
	PackageByFamily map[string]uint32
	StubByName      map[string]uint32
	APIByStub       map[uint32]API
}

// NewLayout builds the import pointer, process holder, root fields, package
// slot tables, and one host-intercepted Thumb trampoline per public API.
func NewLayout() (Layout, error) {
	layout := Layout{
		System:          make([]byte, SystemSize),
		Trampolines:     make([]byte, TrampolineSize),
		PackageByFamily: make(map[string]uint32),
		StubByName:      make(map[string]uint32, len(generatedAPIs)),
		APIByStub:       make(map[uint32]API, len(generatedAPIs)),
	}
	for offset := 0; offset < len(layout.Trampolines); offset += 4 {
		// BKPT #0; NOP. The portable CPU returns to the host at the same
		// boundary at which the reference Unicorn code hook dispatches an API.
		copy(layout.Trampolines[offset:], []byte{0x00, 0xbe, 0x00, 0xbf})
	}
	if err := layout.writeSystem(ImportPointerAddress, ProcessHolderAddress); err != nil {
		return Layout{}, err
	}
	if err := layout.writeSystem(ProcessHolderAddress, ImportRootAddress); err != nil {
		return Layout{}, err
	}

	families := Families()
	for index, family := range families {
		table := PackageTableBase + uint32(index)*PackageTableStride
		if table+PackageTableStride > SystemBase+SystemSize {
			return Layout{}, fmt.Errorf("WIPI package %q exceeds system page", family)
		}
		layout.PackageByFamily[family] = table
	}
	for index, api := range generatedAPIs {
		stub := TrampolineBase + uint32(index)*4
		if stub+4 > TrampolineBase+TrampolineSize {
			return Layout{}, fmt.Errorf("WIPI API %q exceeds trampoline page", api.Name)
		}
		table, ok := layout.PackageByFamily[api.Family]
		if !ok {
			return Layout{}, fmt.Errorf("WIPI API %q has no package table", api.Name)
		}
		if err := layout.writeSystem(table+api.Slot, stub|1); err != nil {
			return Layout{}, fmt.Errorf("bind %s +0x%x: %w", api.Family, api.Slot, err)
		}
		layout.StubByName[api.Name] = stub | 1
		layout.APIByStub[stub] = api
	}
	for family, field := range rootFields {
		table, ok := layout.PackageByFamily[family]
		if !ok {
			return Layout{}, fmt.Errorf("WIPI root family %q is absent", family)
		}
		if err := layout.writeSystem(ImportRootAddress+field, table); err != nil {
			return Layout{}, err
		}
	}
	return layout, nil
}

// RootFields returns a copy of the standard import-root bindings. Provider-only
// packages are cataloged but intentionally are not invented as root fields.
func RootFields() map[string]uint32 {
	result := make(map[string]uint32, len(rootFields))
	for family, field := range rootFields {
		result[family] = field
	}
	return result
}

func (l Layout) writeSystem(address, value uint32) error {
	if address < SystemBase || uint64(address)+4 > uint64(SystemBase)+uint64(len(l.System)) {
		return fmt.Errorf("system write 0x%08x is outside the layout", address)
	}
	binary.LittleEndian.PutUint32(l.System[address-SystemBase:], value)
	return nil
}
