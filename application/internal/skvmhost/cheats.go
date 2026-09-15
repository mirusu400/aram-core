package skvmhost

import (
	"fmt"
	"sort"

	"github.com/mirusu400/aram-core/cheat"
)

const (
	// SKVMClassAddressBase is the start of the deterministic virtual address
	// space used for Java class-file cheats. Classes are sorted by internal
	// name and page-aligned. The loaded class image hash changes whenever that
	// layout could change, so a catalog can never silently target a different
	// build.
	SKVMClassAddressBase = uint32(0x20000000)
	skvmClassAlignment   = uint64(0x1000)
)

type classCheatRegion struct {
	name  string
	start uint32
	data  []byte
}

func buildClassCheatRegions(classData map[string][]byte) ([]classCheatRegion, error) {
	names := make([]string, 0, len(classData))
	for name := range classData {
		names = append(names, name)
	}
	sort.Strings(names)

	cursor := uint64(SKVMClassAddressBase)
	regions := make([]classCheatRegion, 0, len(names))
	for _, name := range names {
		cursor = (cursor + skvmClassAlignment - 1) &^ (skvmClassAlignment - 1)
		data := classData[name]
		end := cursor + uint64(len(data))
		if end > uint64(1)<<32 {
			return nil, fmt.Errorf("map SKVM class %q: virtual address space exhausted", name)
		}
		regions = append(regions, classCheatRegion{
			name:  name,
			start: uint32(cursor),
			data:  data,
		})
		cursor = end
	}
	return regions, nil
}

// CheatRegions exposes the original class files as writable, non-scannable
// executable regions. Method Code attributes retain slices of these bytes,
// so a guarded write changes the instruction stream fetched by the VM.
func (m *Machine) CheatRegions() []cheat.Region {
	m.mu.Lock()
	defer m.mu.Unlock()
	regions := make([]cheat.Region, 0, len(m.cheatRegions))
	for _, region := range m.cheatRegions {
		regions = append(regions, cheat.Region{
			Name:      "skvm.class." + region.name,
			Start:     region.start,
			Size:      uint32(len(region.data)),
			Writable:  true,
			Scannable: false,
		})
	}
	return regions
}

// ReadMemory implements cheat.Memory over the deterministic class address
// space without exposing VM internals to frontends.
func (m *Machine) ReadMemory(address uint32, destination []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("SKVM machine is closed")
	}
	region, offset, err := m.classCheatSlice(address, len(destination))
	if err != nil {
		return err
	}
	copy(destination, region.data[offset:offset+len(destination)])
	return nil
}

// WriteMemory implements cheat.Memory over the deterministic class address
// space. The cheat engine performs expected-original validation before this
// method is called.
func (m *Machine) WriteMemory(address uint32, source []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("SKVM machine is closed")
	}
	region, offset, err := m.classCheatSlice(address, len(source))
	if err != nil {
		return err
	}
	original := append([]byte(nil), region.data[offset:offset+len(source)]...)
	copy(region.data[offset:offset+len(source)], source)
	if err := m.vm.ReplaceClassData(region.name, region.data); err != nil {
		copy(region.data[offset:offset+len(source)], original)
		return fmt.Errorf("patch SKVM class %q: %w", region.name, err)
	}
	return nil
}

func (m *Machine) classCheatSlice(
	address uint32,
	size int,
) (classCheatRegion, int, error) {
	if size <= 0 {
		return classCheatRegion{}, 0, fmt.Errorf("SKVM class memory size %d is invalid", size)
	}
	start := uint64(address)
	end := start + uint64(size)
	for _, region := range m.cheatRegions {
		regionStart := uint64(region.start)
		regionEnd := regionStart + uint64(len(region.data))
		if start >= regionStart && end <= regionEnd {
			return region, int(start - regionStart), nil
		}
	}
	return classCheatRegion{}, 0, fmt.Errorf(
		"SKVM class memory 0x%08x..0x%08x is outside mapped classes",
		address,
		end,
	)
}

// CheatTargetSHA256 is the container identity used as a fallback for catalog
// lookup when only an older file-keyed document is available.
func (m *Machine) CheatTargetSHA256() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.source.SHA256
}

// CheatImageSHA256 is the stable identity of the original loaded Java class
// image, independent of ZIP/JAR repackaging.
func (m *Machine) CheatImageSHA256() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.imageSHA256
}

// CheatProfileID reports the selected SKVM compatibility profile to product
// adapters that cannot import this internal package directly.
func (m *Machine) CheatProfileID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.services.Config.Device.ProfileID
}
