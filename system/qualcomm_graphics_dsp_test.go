package system

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

type graphicsDSPInterruptProbe struct {
	pulses [][2]uint32
}

func (p *graphicsDSPInterruptProbe) SetGroupedSource(statusOffset, mask uint32, asserted bool) error {
	if asserted {
		p.pulses = append(p.pulses, [2]uint32{statusOffset, mask})
	}
	return nil
}

func testGraphicsDSPProfile() QualcommGraphicsDSPProfile {
	return QualcommGraphicsDSPProfile{
		MemoryID:      "dsp",
		ResetOffset:   0x1000,
		TriggerOffset: 0x1008, TriggerValue: 0x88000000,
		CommandOffset:       0x0120,
		ModuleVersionOffset: 0x0300, ModuleVersion: 0x12000315,
		StateOffset:        0x0200,
		ReadyOffset:        0x0202,
		ArraySizeOffset:    0x0204,
		ArrayAddressOffset: 0x0208,
		StampOffset:        0x020c,
		EventRingOffset:    0x0100, EventRingLengthOffset: 0x0210,
		EventRingLength:            4,
		PrivateStateOffset:         0x8000,
		InterruptGroupStatusOffset: 0x84, InterruptGroupMask: 0x01,
	}
}

type graphicsDSPHarness struct {
	t         *testing.T
	bus       *Bus
	interrupt *graphicsDSPInterruptProbe
}

func newGraphicsDSPHarness(t *testing.T) *graphicsDSPHarness {
	t.Helper()
	board := BoardProfile{
		ID: "test.board",
		Memory: []MemoryRegionProfile{
			{ID: "ram", Kind: MemoryRAM, Address: 0x00000000, Size: 0x00100000},
			{ID: "dsp", Kind: MemorySparseRAM, Address: 0x70000000, Size: 0x00200000},
		},
	}
	profile := testGraphicsDSPProfile()
	check(t, profile.validate(board.Memory))
	board.GraphicsDSP = &profile
	bus := NewBus()
	check(t, bus.MapRAM("ram", 0x00000000, 0x00100000))
	check(t, bus.MapSparseRAM("dsp", 0x70000000, 0x00200000))
	interrupt := &graphicsDSPInterruptProbe{}
	check(t, board.AttachGraphicsDSP(bus, interrupt))
	return &graphicsDSPHarness{t: t, bus: bus, interrupt: interrupt}
}

func (h *graphicsDSPHarness) write(address uint32, width Width, value uint32) {
	h.t.Helper()
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	check(h.t, h.bus.Write(address, raw[:int(width)], cpu.PermissionWrite))
}

func (h *graphicsDSPHarness) read(address uint32, width Width) uint32 {
	h.t.Helper()
	var raw [4]byte
	check(h.t, h.bus.Read(address, raw[:int(width)], cpu.PermissionRead))
	return binary.LittleEndian.Uint32(raw[:])
}

func (h *graphicsDSPHarness) load(address uint32, data []byte) {
	h.t.Helper()
	for offset := 0; offset < len(data); offset += 4 {
		check(h.t, h.bus.Write(address+uint32(offset), data[offset:offset+4], cpu.PermissionWrite))
	}
}

// submit hands one command array to the module the way qgldsp_command does.
func (h *graphicsDSPHarness) submit(address uint32, array []byte) {
	h.t.Helper()
	h.load(address, array)
	h.write(0x70000202, Width16, 0)
	h.write(0x70000204, Width16, uint32(len(array)))
	h.write(0x70000208, Width32, address)
	h.write(0x70000120, Width16, qualcommGraphicsDSPCommandArray)
	h.write(0x70001008, Width32, 0x88000000)
}

func TestQualcommGraphicsDSPStartsAndCompletesCommandArrays(t *testing.T) {
	h := newGraphicsDSPHarness(t)
	// Nothing answers before the image runs.
	h.write(0x70001008, Width32, 0x88000000)
	if state := h.read(0x70000200, Width16); state != 0 {
		t.Fatalf("module state before start = %#x", state)
	}
	h.write(0x70001000, Width32, 1)
	if state := h.read(0x70000200, Width16); state != 0 {
		t.Fatalf("module state while held in reset = %#x", state)
	}

	// An image without the graphics module starts without touching the
	// addresses this module would own.
	h.write(0x70001000, Width32, 0)
	if state := h.read(0x70000200, Width16); state != 0 {
		t.Fatalf("module state in an image without the module = %#x", state)
	}
	h.write(0x70000300, Width32, 0x12000315)
	h.write(0x70001000, Width32, 0)
	if state := h.read(0x70000200, Width16); state != qualcommGraphicsDSPInitialised {
		t.Fatalf("module state after start = %#x", state)
	}
	if length := h.read(0x70000210, Width16); length != 4 {
		t.Fatalf("published event ring length = %d", length)
	}
	for slot := uint32(0); slot < 4; slot++ {
		if event := h.read(0x70000100+slot*2, Width16); event != qualcommGraphicsDSPNoEvent {
			t.Fatalf("event slot %d after start = %#x", slot, event)
		}
	}
	if ready := h.read(0x70000202, Width16); ready != 0 {
		t.Fatalf("module ready before the first command interrupt = %#x", ready)
	}

	// The buffer-setup interrupt carries no command; it leaves the module ready.
	h.write(0x70001008, Width32, 0x88000000)
	if ready := h.read(0x70000202, Width16); ready != qualcommGraphicsDSPReady {
		t.Fatalf("module ready after setup interrupt = %#x", ready)
	}
	if len(h.interrupt.pulses) != 0 {
		t.Fatalf("setup interrupt pulsed the ARM: %v", h.interrupt.pulses)
	}

	array := make([]byte, qualcommGraphicsDSPArrayHeader)
	binary.LittleEndian.PutUint32(array[0:4], 7)
	binary.LittleEndian.PutUint16(array[12:14], uint16(len(array)))
	h.submit(0x00020000, array)
	if stamp := h.read(0x7000020c, Width32); stamp != 7 {
		t.Fatalf("completion stamp = %d, want 7", stamp)
	}
	if command := h.read(0x70000120, Width16); command != 0 {
		t.Fatalf("command word after execution = %#x", command)
	}
	if ready := h.read(0x70000202, Width16); ready != qualcommGraphicsDSPReady {
		t.Fatalf("module ready after execution = %#x", ready)
	}
	if want := [][2]uint32{{0x84, 0x01}}; len(h.interrupt.pulses) != 1 || h.interrupt.pulses[0] != want[0] {
		t.Fatalf("completion interrupt = %v, want %v", h.interrupt.pulses, want)
	}

	// An array the module cannot read is reported, not silently completed.
	h.write(0x70000202, Width16, 0)
	h.write(0x70000204, Width16, 64)
	h.write(0x70000208, Width32, 0x50000000)
	h.write(0x70000120, Width16, qualcommGraphicsDSPCommandArray)
	h.write(0x70001008, Width32, 0x88000000)
	if stamp := h.read(0x7000020c, Width32); stamp != 7 {
		t.Fatalf("unreadable array advanced the stamp to %d", stamp)
	}
	errorSeen := false
	for slot := uint32(0); slot < 4; slot++ {
		errorSeen = errorSeen || h.read(0x70000100+slot*2, Width16) == qualcommGraphicsDSPEventError
	}
	if !errorSeen {
		t.Fatal("unreadable array posted no error event")
	}
}

func TestQualcommGraphicsDSPProfileRejectsMisplacedVariables(t *testing.T) {
	memory := []MemoryRegionProfile{
		{ID: "dsp", Kind: MemorySparseRAM, Address: 0x70000000, Size: 0x00200000},
	}
	check(t, testGraphicsDSPProfile().validate(memory))
	for name, mutate := range map[string]func(*QualcommGraphicsDSPProfile){
		"unknown region":      func(p *QualcommGraphicsDSPProfile) { p.MemoryID = "other" },
		"odd state":           func(p *QualcommGraphicsDSPProfile) { p.StateOffset = 0x0201 },
		"array past region":   func(p *QualcommGraphicsDSPProfile) { p.ArrayAddressOffset = 0x200000 },
		"empty ring":          func(p *QualcommGraphicsDSPProfile) { p.EventRingLength = 0 },
		"ring past region":    func(p *QualcommGraphicsDSPProfile) { p.EventRingOffset = 0x1ffffc },
		"private past region": func(p *QualcommGraphicsDSPProfile) { p.PrivateStateOffset = 0x1ff000 },
		"no interrupt child":  func(p *QualcommGraphicsDSPProfile) { p.InterruptGroupMask = 0 },
		"reset is trigger":    func(p *QualcommGraphicsDSPProfile) { p.TriggerOffset = p.ResetOffset },
	} {
		profile := testGraphicsDSPProfile()
		mutate(&profile)
		if err := profile.validate(memory); err == nil {
			t.Errorf("%s: invalid profile accepted", name)
		}
	}
}
