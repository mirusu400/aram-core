package application

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/wipi"
)

const (
	// cletImportRoot is the table of WIPI-C package tables the image's import
	// pointer names. It sits in the unused upper half of the WIPI system page.
	cletImportRoot      = wipi.SystemBase + 0x8000
	cletImportTableBase = wipi.SystemBase + 0x9000
	cletImportPackages  = 24
	cletImportSlots     = 64

	cletKernelPackage   = 0
	cletGraphicsPackage = 1
	cletMiscPackage     = 4
	// cletMediaPackage is the root index of MC_MDA. Its table is not the
	// recovered public one: slots are inserted after MC_mdaClipGetType and
	// after MC_mdaClipPutData, so every later selector sits further down.
	cletMediaPackage = 7
)

// cletMediaSlots binds the media slots whose selector is established: the
// ones a title was observed calling with that selector's arguments
// (ClipCreate, ClipPutData, ClipSetVolume, Play, Vibrator), Stop and
// ClipClearData from the stop-then-clear sequence that precedes each new clip,
// and the selectors the recovered order places between two of those.
var cletMediaSlots = map[uint32]string{
	0x00: "MC_mdaClipCreate",
	0x04: "MC_mdaClipFree",
	0x08: "MC_mdaClipGetType",
	0x10: "MC_mdaClipPutData",
	0x28: "MC_mdaClipClearData",
	0x2c: "MC_mdaClipSetPosition",
	0x30: "MC_mdaClipGetVolume",
	0x34: "MC_mdaClipSetVolume",
	0x38: "MC_mdaPlay",
	0x3c: "MC_mdaPause",
	0x40: "MC_mdaResume",
	0x44: "MC_mdaStop",
	0x48: "MC_mdaRecord",
	0x4c: "MC_mdaGetVolume",
	0x50: "MC_mdaSetVolume",
	0x54: "MC_mdaVibrator",
}

// cletImportFamilies maps a root index to the public WIPI-C package served
// there. The indices are the recovered import-root fields, MC_MISC is where a
// title was observed calling MC_miscBackLight, and MC_MDA is bound slot by
// slot instead.
func cletImportFamilies() map[int]string {
	families := map[int]string{cletMiscPackage: "MC_MISC"}
	for family, field := range wipi.RootFields() {
		if family != "MC_MDA" {
			families[int(field/4)] = family
		}
	}
	return families
}

func cletFirstUnboundStub() uint32 {
	return wipi.TrampolineBase + uint32(len(wipi.APIs()))*4
}

// installImports writes the import root and one table per package. A slot
// whose selector is known resolves to the shared runtime's trampoline for
// that API; any other slot gets a trampoline of its own, so a call through it
// is answered and reported by package and slot instead of branching through a
// null pointer.
func (c *cletRuntime) installImports(backend cpu.Backend, runtime *wipirt.Runtime) error {
	firstUnbound := cletFirstUnboundStub()
	families := cletImportFamilies()
	root := make([]byte, cletImportPackages*4)
	tables := make([]byte, cletImportPackages*cletImportSlots*4)
	for pkg := range cletImportPackages {
		table := cletImportTableBase + uint32(pkg*cletImportSlots*4)
		binary.LittleEndian.PutUint32(root[pkg*4:], table)
		for slot := range cletImportSlots {
			index := pkg*cletImportSlots + slot
			stub := (firstUnbound + uint32(index)*4) | 1
			name := ""
			if api, ok := wipi.Resolve(families[pkg], uint32(slot*4)); ok {
				name = api.Name
			}
			if pkg == cletMediaPackage {
				name = cletMediaSlots[uint32(slot*4)]
			}
			if bound, ok := runtime.Layout.StubByName[name]; ok {
				stub = bound
			}
			binary.LittleEndian.PutUint32(tables[index*4:], stub)
		}
	}
	if err := backend.WriteMemory(cletImportRoot, root); err != nil {
		return fmt.Errorf("install Clet import root: %w", err)
	}
	if err := backend.WriteMemory(cletImportTableBase, tables); err != nil {
		return fmt.Errorf("install Clet import tables: %w", err)
	}
	return nil
}

// dispatchUnboundTrap answers a call through a slot the public catalog has no
// selector for.
func (c *cletRuntime) dispatchUnboundTrap(runtime *wipirt.Runtime, trap uint32) (bool, error) {
	firstUnbound := cletFirstUnboundStub()
	if trap < firstUnbound || (trap-firstUnbound)/4 >= cletImportPackages*cletImportSlots {
		return false, nil
	}
	index := int(trap-firstUnbound) / 4
	pkg, slot := index/cletImportSlots, uint32(index%cletImportSlots)*4
	var args [4]uint32
	for register := range args {
		value, err := runtime.CPU.ReadRegister(uint32(register))
		if err != nil {
			return true, err
		}
		args[register] = value
	}
	name := fmt.Sprintf("SKT.package%d+0x%02x", pkg, slot)
	result := guest.WIPIReturn{}
	switch {
	case pkg == cletKernelPackage && slot == 0xb4:
		// A timer arm that passes MC_knlSetTimer's 64-bit timeout in r1:r2
		// and its parameter in r3. A title calls it right after
		// MC_knlDefTimer on the same timer.
		armed, err := runtime.SetTimer(args[0], uint64(args[1])|uint64(args[2])<<32, args[3], true)
		if err != nil {
			return true, fmt.Errorf("%s: %w", name, err)
		}
		runtime.Observed[name]++
		return true, runtime.ReturnFromTrap(armed)
	case pkg == cletGraphicsPackage && slot == 0xb4:
		// Reads a named display property ("HEIGHT") into a caller buffer and
		// returns zero on success. No property is provided, so the title
		// keeps its own default instead of parsing an unwritten buffer.
		result.Low = guest.WIPIReturnCode(guest.WIPIInvalid)
	}
	runtime.Unimplemented[name]++
	runtime.Stats.UnimplementedCalls++
	runtime.Stats.LastUnimplemented = name
	return true, runtime.ReturnFromTrap(result)
}
