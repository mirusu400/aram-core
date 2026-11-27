package system

import (
	"encoding/binary"
	"fmt"
)

// QualcommGraphicsDSPProfile locates the ARM/DSP shared variables of the
// Q3Dimension graphics module inside an application-DSP address-space region.
// The module is a screen-space rasteriser: the ARM-side OpenGL ES library
// transforms, lights and clips, packs the result into a command array, and
// hands the array's ARM address to the DSP. All offsets are relative to the
// region named by MemoryID and come from the DSP image's variable table.
type QualcommGraphicsDSPProfile struct {
	MemoryID string

	// ResetOffset is the DSP run-control word; storing zero starts the image.
	ResetOffset uint32
	// TriggerOffset receives TriggerValue when the ARM raises the graphics
	// module's command interrupt.
	TriggerOffset uint32
	TriggerValue  uint32
	// CommandOffset is the 16-bit interrupt-command word the ARM fills before
	// the trigger and the DSP clears once it has taken the command.
	CommandOffset uint32

	// ModuleVersionOffset holds the graphics module's version word in the
	// downloaded image. Other DSP images use the same address space for other
	// things, so the module only starts when the image carries ModuleVersion.
	ModuleVersionOffset uint32
	ModuleVersion       uint32

	// StateOffset reads 0x8888 once the module is initialised.
	StateOffset uint32
	// ReadyOffset reads 0xffff while the module can accept another array.
	ReadyOffset uint32
	// ArrayAddressOffset and ArraySizeOffset describe the submitted array.
	ArrayAddressOffset uint32
	ArraySizeOffset    uint32
	// StampOffset holds the identifier of the last array the DSP completed.
	StampOffset uint32
	// EventRingOffset is a ring of 16-bit event codes, 0xffff when empty, and
	// EventRingLengthOffset publishes its length to the ARM.
	EventRingOffset       uint32
	EventRingLengthOffset uint32
	EventRingLength       uint16

	// PrivateStateOffset is DSP-internal memory the ARM never touches. The
	// model keeps the module's registers there, as the real module does, so a
	// machine snapshot carries them with the rest of the DSP address space.
	PrivateStateOffset uint32

	// InterruptGroupStatusOffset and InterruptGroupMask name the second-level
	// interrupt child the module pulses after posting an event.
	InterruptGroupStatusOffset uint32
	InterruptGroupMask         uint32
}

const (
	qualcommGraphicsDSPInitialised   = 0x8888
	qualcommGraphicsDSPReady         = 0xffff
	qualcommGraphicsDSPNoEvent       = 0xffff
	qualcommGraphicsDSPCommandArray  = 0x0800
	qualcommGraphicsDSPArrayHeader   = 64
	qualcommGraphicsDSPMaximumArray  = 0x10000
	qualcommGraphicsDSPMaximumEvents = 64
)

func (p QualcommGraphicsDSPProfile) validate(memory []MemoryRegionProfile) error {
	var region *MemoryRegionProfile
	for index := range memory {
		if memory[index].ID == p.MemoryID {
			region = &memory[index]
			break
		}
	}
	if region == nil || region.Kind != MemorySparseRAM && region.Kind != MemoryRAM {
		return fmt.Errorf("Qualcomm graphics DSP memory region %q is not ordinary memory", p.MemoryID)
	}
	inside := func(offset, size, alignment uint32) bool {
		return offset%alignment == 0 && uint64(offset)+uint64(size) <= uint64(region.Size)
	}
	if !inside(p.ResetOffset, 4, 4) || !inside(p.TriggerOffset, 4, 4) ||
		!inside(p.CommandOffset, 2, 2) || !inside(p.StateOffset, 2, 2) ||
		!inside(p.ModuleVersionOffset, 4, 4) ||
		!inside(p.ReadyOffset, 2, 2) || !inside(p.ArrayAddressOffset, 4, 4) ||
		!inside(p.ArraySizeOffset, 2, 2) || !inside(p.StampOffset, 4, 4) ||
		!inside(p.EventRingLengthOffset, 2, 2) ||
		p.EventRingLength == 0 || p.EventRingLength > qualcommGraphicsDSPMaximumEvents ||
		!inside(p.EventRingOffset, uint32(p.EventRingLength)*2, 2) ||
		!inside(p.PrivateStateOffset, qualcommGraphicsDSPPrivateSize, 4) ||
		p.ResetOffset == p.TriggerOffset || p.InterruptGroupMask == 0 {
		return fmt.Errorf("invalid Qualcomm graphics DSP profile for %q", p.MemoryID)
	}
	return nil
}

// QualcommGraphicsDSPInterrupt is the second-level interrupt input the module
// pulses. The compact VIC implements it.
type QualcommGraphicsDSPInterrupt interface {
	SetGroupedSource(statusOffset uint32, mask uint32, asserted bool) error
}

// QualcommGraphicsDSP models the Q3Dimension graphics module at the level of
// its ARM interface: the start-up handshake, command-array submission, the
// completion stamp, the event ring and the completion interrupt. It executes
// arrays synchronously with the store that submits them, which keeps a run
// deterministic and needs no scheduling.
type QualcommGraphicsDSP struct {
	profile   QualcommGraphicsDSPProfile
	base      uint32
	interrupt QualcommGraphicsDSPInterrupt
	scratch   qualcommGraphicsScratch
}

func newQualcommGraphicsDSP(
	profile QualcommGraphicsDSPProfile,
	base uint32,
	interrupt QualcommGraphicsDSPInterrupt,
) *QualcommGraphicsDSP {
	return &QualcommGraphicsDSP{profile: profile, base: base, interrupt: interrupt}
}

// AttachGraphicsDSP connects the profiled graphics module, if the board has
// one, to its DSP address space and interrupt child.
func (p BoardProfile) AttachGraphicsDSP(bus *Bus, interrupt QualcommGraphicsDSPInterrupt) error {
	if p.GraphicsDSP == nil {
		return nil
	}
	if bus == nil || interrupt == nil {
		return fmt.Errorf("attach board profile %q graphics DSP: missing bus or interrupt controller", p.ID)
	}
	for _, region := range p.Memory {
		if region.ID == p.GraphicsDSP.MemoryID {
			device := newQualcommGraphicsDSP(*p.GraphicsDSP, region.Address, interrupt)
			if err := bus.AttachMemoryCoprocessor(region.ID, device); err != nil {
				return fmt.Errorf("attach board profile %q graphics DSP: %w", p.ID, err)
			}
			return nil
		}
	}
	return fmt.Errorf("attach board profile %q graphics DSP: no memory region %q", p.ID, p.GraphicsDSP.MemoryID)
}

type qualcommGraphicsDSPMemory struct {
	memory CoprocessorMemory
	base   uint32
}

func (m qualcommGraphicsDSPMemory) read16(offset uint32) uint16 {
	var raw [2]byte
	m.memory.ReadMemory(m.base+offset, raw[:])
	return binary.LittleEndian.Uint16(raw[:])
}

func (m qualcommGraphicsDSPMemory) read32(offset uint32) uint32 {
	var raw [4]byte
	m.memory.ReadMemory(m.base+offset, raw[:])
	return binary.LittleEndian.Uint32(raw[:])
}

func (m qualcommGraphicsDSPMemory) write16(offset uint32, value uint16) {
	var raw [2]byte
	binary.LittleEndian.PutUint16(raw[:], value)
	m.memory.WriteMemory(m.base+offset, raw[:])
}

func (m qualcommGraphicsDSPMemory) write32(offset uint32, value uint32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	m.memory.WriteMemory(m.base+offset, raw[:])
}

// MemoryWritten implements MemoryCoprocessor.
func (d *QualcommGraphicsDSP) MemoryWritten(memory CoprocessorMemory, offset uint32, width Width, value uint32) {
	if width != Width32 {
		return
	}
	dsp := qualcommGraphicsDSPMemory{memory: memory, base: d.base}
	switch offset {
	case d.profile.ResetOffset:
		if value == 0 && dsp.read32(d.profile.ModuleVersionOffset) == d.profile.ModuleVersion {
			d.start(dsp)
		}
	case d.profile.TriggerOffset:
		if value == d.profile.TriggerValue && dsp.read16(d.profile.StateOffset) == qualcommGraphicsDSPInitialised {
			d.command(dsp)
		}
	}
}

// start is the module's reset entry: it clears its registers, empties the
// event ring and announces itself.
func (d *QualcommGraphicsDSP) start(dsp qualcommGraphicsDSPMemory) {
	var state qualcommGraphicsState
	state.store(dsp, d.profile.PrivateStateOffset)
	for index := uint32(0); index < uint32(d.profile.EventRingLength); index++ {
		dsp.write16(d.profile.EventRingOffset+index*2, qualcommGraphicsDSPNoEvent)
	}
	dsp.write16(d.profile.EventRingLengthOffset, d.profile.EventRingLength)
	dsp.write32(d.profile.StampOffset, 0)
	dsp.write16(d.profile.ReadyOffset, 0)
	dsp.write16(d.profile.StateOffset, qualcommGraphicsDSPInitialised)
}

func (d *QualcommGraphicsDSP) command(dsp qualcommGraphicsDSPMemory) {
	command := dsp.read16(d.profile.CommandOffset)
	dsp.write16(d.profile.CommandOffset, 0)
	if command != qualcommGraphicsDSPCommandArray {
		// Any other interrupt command leaves the module idle and ready.
		dsp.write16(d.profile.ReadyOffset, qualcommGraphicsDSPReady)
		return
	}
	address := dsp.read32(d.profile.ArrayAddressOffset)
	size := int(dsp.read16(d.profile.ArraySizeOffset))
	state := qualcommGraphicsState{scratch: &d.scratch}
	state.load(dsp, d.profile.PrivateStateOffset)
	var events []uint16
	array := make([]byte, size)
	if size < qualcommGraphicsDSPArrayHeader || !dsp.memory.ReadMemory(address, array) {
		events = []uint16{qualcommGraphicsDSPEventError}
	} else {
		events = state.execute(dsp, d.profile.PrivateStateOffset, array)
		dsp.write32(d.profile.StampOffset, binary.LittleEndian.Uint32(array[0:4]))
	}
	for _, event := range events {
		dsp.write16(d.profile.EventRingOffset+uint32(state.eventIndex)*2, event)
		state.eventIndex = (state.eventIndex + 1) % d.profile.EventRingLength
	}
	state.store(dsp, d.profile.PrivateStateOffset)
	dsp.write16(d.profile.ReadyOffset, qualcommGraphicsDSPReady)
	// Both a posted event and the freed command slot are reported through the
	// module's interrupt; the ARM service routine then pumps the ring and
	// submits the next queued array.
	_ = d.interrupt.SetGroupedSource(
		d.profile.InterruptGroupStatusOffset, d.profile.InterruptGroupMask, true,
	)
}
