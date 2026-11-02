package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

// gvmEntryArchive builds a v2 image with the given header entries (0x1c start,
// 0x1e end, 0x20 event, 0x22 input) placed back to back from offset 54, a
// number of one-word RAM symbols, and one file-backed NUL-terminated media
// record.
func gvmEntryArchive(t *testing.T, entries map[int][]byte, symbols int, media string) []byte {
	t.Helper()
	sgs := make([]byte, 54)
	sgs[0], sgs[2], sgs[5], sgs[10] = 2, 12, 1, 'G'
	for _, header := range []int{0x1c, 0x1e, 0x20, 0x22} {
		code, ok := entries[header]
		if !ok {
			continue
		}
		binary.LittleEndian.PutUint16(sgs[header:], uint16(len(sgs)))
		sgs = append(sgs, code...)
	}
	ds := len(sgs)
	for range symbols {
		sgs = append(sgs, 2, 1, 1, 0)
	}
	ps := len(sgs)
	sgs = append(sgs, make([]byte, 2*symbols)...)
	dm := len(sgs)
	payload := append([]byte(media), 0)
	sgs = append(sgs, 0, 0, byte(len(payload)), byte(len(payload)>>8))
	pm := len(sgs)
	sgs = append(sgs, payload...)
	for offset, value := range map[int]int{0x2c: ds, 0x2e: ps, 0x30: dm, 0x32: pm} {
		binary.LittleEndian.PutUint16(sgs[offset:], uint16(value))
	}
	return testZIP(t, map[string][]byte{"game.sgs": sgs})
}

func gvmSymbolWord(t *testing.T, m *gvmMachine, index uint8) int16 {
	t.Helper()
	value, err := m.vm.SymbolWord(index)
	if err != nil {
		t.Fatal(err)
	}
	return int16(value)
}

// The event entry is GVM2X's timer callback: it never runs on a frame of its
// own, only when one of the three timer slots fires.
func TestGVMEventEntryRunsOnlyOnTimerTicks(t *testing.T) {
	data := gvmEntryArchive(t, map[int][]byte{
		0x1c: {0xff},
		0x20: {0x05, 0, 0x57, 0x78, 0xff},
	}, 16, "unused")
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for frame := range 30 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
		if presents := machine.GVMPresentCount(); presents != 0 {
			t.Fatalf("frame %d: event entry ran without a timer (%d presents)", frame, presents)
		}
	}
}

// Slot 0 advances the frame counter in symbol 3 and its remainders in symbols
// 4..6, and the palette follows symbol 6 (sub_41E450, sub_40ED50).
func TestGVMSlotZeroTickAdvancesCountersAndPalettePhase(t *testing.T) {
	data := gvmEntryArchive(t, map[int][]byte{
		// 50 ms repeating timer on slot 0.
		0x1c: {0x06, 0, 50, 0x05, 1, 0x9a, 0xff},
		0x20: {0x05, 0, 0x57, 0x78, 0xff},
	}, 16, "unused")
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if got := gvmSymbolWord(t, machine, 0); got != 1 {
		t.Fatalf("symbol 0 before start = %d, want 1", got)
	}
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for tick := 1; tick <= 7; tick++ {
		for range 3 {
			if err := machine.StepFrame(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if presents := machine.GVMPresentCount(); presents != uint64(tick) {
			t.Fatalf("tick %d: %d presents", tick, presents)
		}
		want := [...]int16{0, int16(tick), int16(tick % 2), int16(tick % 3), int16(tick % 6)}
		for index, value := range want {
			symbol := uint8(index)
			if index > 0 {
				symbol = uint8(index + 2)
			}
			if got := gvmSymbolWord(t, machine, symbol); got != value {
				t.Fatalf("tick %d: symbol %d = %d, want %d", tick, symbol, got, value)
			}
		}
		if phase := machine.display.Snapshot().Phase; phase != uint8(tick%6) {
			t.Fatalf("tick %d: palette phase %d", tick, phase)
		}
	}
}

func TestGVMOtherSlotsLeaveTheFrameCounter(t *testing.T) {
	data := gvmEntryArchive(t, map[int][]byte{
		0x1c: {0x06, 0, 50, 0x05, 1, 0x9c, 0xff},
		0x20: {0x05, 0, 0x57, 0x78, 0xff},
	}, 16, "unused")
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if machine.GVMPresentCount() != 1 || gvmSymbolWord(t, machine, 0) != 2 || gvmSymbolWord(t, machine, 3) != 0 {
		t.Fatalf("presents=%d symbol0=%d symbol3=%d", machine.GVMPresentCount(),
			gvmSymbolWord(t, machine, 0), gvmSymbolWord(t, machine, 3))
	}
}

// Opcode95 kills the vibration timer, never the guest's slot 0.
func TestGVMVibrationStopKeepsSlotZeroRunning(t *testing.T) {
	data := gvmEntryArchive(t, map[int][]byte{
		0x1c: {0x06, 0, 50, 0x05, 1, 0x9a, 0x05, 3, 0x94, 0x95, 0xff},
		0x20: {0x05, 0, 0x57, 0x78, 0xff},
	}, 16, "unused")
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if presents := machine.GVMPresentCount(); presents != 2 {
		t.Fatalf("slot 0 fired %d times in 100 ms, want 2", presents)
	}
}

func TestGVMSendKeyIsGuestCode14(t *testing.T) {
	data := gvmEntryArchive(t, map[int][]byte{0x1c: {0xff}, 0x22: {0xff}}, 16, "unused")
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "send", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	if machine.lastInputCode != 14 || gvmSymbolWord(t, machine, 0) != 14 {
		t.Fatalf("send delivered code %d", machine.lastInputCode)
	}
}

// END runs the script's end entry, never the input entry, then closes it.
func TestGVMEndKeyRunsEndEntryAndCloses(t *testing.T) {
	const target = "http://example.invalid/end"
	data := gvmEntryArchive(t, map[int][]byte{
		0x1c: {0xff},
		0x1e: {0x05, 0, 0xc4, 0xff},
		0x22: {0x05, 0, 0x57, 0x78, 0xff},
	}, 16, target)
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "end", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped || machine.GVMPresentCount() != 0 {
		t.Fatalf("state=%s presents=%d", machine.State(), machine.GVMPresentCount())
	}
	if requests := machine.ExternalRequests(); len(requests) != 1 || requests[0].Target != target {
		t.Fatalf("end entry requests = %+v", requests)
	}
}

func TestGVMEndKeyWithoutEndEntryCloses(t *testing.T) {
	data := gvmEntryArchive(t, map[int][]byte{0x1c: {0xff}, 0x22: {0xff}}, 16, "unused")
	machine := newOperationalFixture(t, data)
	defer machine.Close()
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "end", Pressed: false}); err != nil || machine.State() != machinecore.StateRunning {
		t.Fatalf("release: %v state=%s", err, machine.State())
	}
	if err := machine.QueueInput(machinecore.InputEvent{Control: "end", Pressed: true}); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StateStopped || machine.inputDispatches != 0 {
		t.Fatalf("state=%s input dispatches=%d", machine.State(), machine.inputDispatches)
	}
}

func gvmTickedMachine(t *testing.T) *gvmMachine {
	t.Helper()
	data := gvmEntryArchive(t, map[int][]byte{
		0x1c: {0x06, 0, 50, 0x05, 1, 0x9a, 0xff},
		0x20: {0x05, 0, 0x57, 0x78, 0xff},
	}, 16, "unused")
	machine := newOperationalFixture(t, data)
	digest := sha256.Sum256(data)
	machine.source.SHA256 = fmt.Sprintf("%x", digest)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	return machine
}

// encodeGVMStateForTest frames a payload the way SaveState does.
func encodeGVMStateForTest(t *testing.T, m *gvmMachine, version uint32, state any) []byte {
	t.Helper()
	payload, err := shared.MarshalStateComponent(state)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := hex.DecodeString(m.source.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	encoded.WriteString(gvmMachineStateMagic)
	_ = binary.Write(&encoded, binary.LittleEndian, version)
	encoded.Write(digest)
	_ = binary.Write(&encoded, binary.LittleEndian, uint64(len(payload)))
	encoded.Write(payload)
	checksum := sha256.Sum256(encoded.Bytes())
	encoded.Write(checksum[:])
	return encoded.Bytes()
}

func TestGVMStateKeepsPalettePhase(t *testing.T) {
	machine := gvmTickedMachine(t)
	defer machine.Close()
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	if err := machine.display.SetGVMPalettePhase(4); err != nil {
		t.Fatal(err)
	}
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if phase := machine.display.Snapshot().Phase; phase != 1 {
		t.Fatalf("restored palette phase %d, want 1", phase)
	}
}

// Version 1 states, written before opcode94 was known to be vibration, still
// load; their palette starts at the steady phase.
func TestGVMStateLoadsVersion1(t *testing.T) {
	machine := gvmTickedMachine(t)
	defer machine.Close()
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	current, err := machine.parseGVMState(saved.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var legacy gvmVolatileStateV1
	legacy.Lifecycle, legacy.VM, legacy.Clock, legacy.Random = current.Lifecycle, current.VM, current.Clock, current.Random
	legacy.Timer.Mode, legacy.Timer.Interval, legacy.Timer.Selector = current.Timer.Mode, current.Timer.Interval, current.Timer.Selector
	legacy.Timer.Reached, legacy.Timer.Frame, legacy.Timer.Slots = current.Timer.Reached, current.Timer.Frame, current.Timer.Slots
	legacy.Timer.Fixed = true
	legacy.Display.Width, legacy.Display.Height = current.Display.Width, current.Display.Height
	legacy.Display.Drawing, legacy.Display.Auxiliary = current.Display.Drawing, current.Display.Auxiliary
	legacy.Display.Mapping, legacy.Display.Selector = current.Display.Mapping, current.Display.Selector
	legacy.Media, legacy.Frame, legacy.AudioGeneration = current.Media, current.Frame, current.AudioGeneration
	legacy.InputDispatches, legacy.LastInputCode = current.InputDispatches, current.LastInputCode
	if err := machine.LoadState(bytes.NewReader(encodeGVMStateForTest(t, machine, 1, legacy))); err != nil {
		t.Fatal(err)
	}
	if machine.State() != machinecore.StatePaused || machine.GVMPresentCount() != 1 || machine.display.Snapshot().Phase != 0 {
		t.Fatalf("state=%s presents=%d phase=%d", machine.State(), machine.GVMPresentCount(), machine.display.Snapshot().Phase)
	}
	if err := machine.Resume(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if machine.GVMPresentCount() != 2 || machine.display.Snapshot().Phase != 2 {
		t.Fatalf("upgraded timer: presents=%d phase=%d", machine.GVMPresentCount(), machine.display.Snapshot().Phase)
	}
	if err := machine.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := machine.LoadState(bytes.NewReader(encodeGVMStateForTest(t, machine, 3, current))); err == nil {
		t.Fatal("unknown state version loaded")
	}
}
