package system

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestQualcommMDPScriptEngineTransfersRGB565ToDCSPanel(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("script-and-frame", 0, 0x10000); err != nil {
		t.Fatal(err)
	}
	panel := newTestMDPPanel(t, 2, 2)
	const (
		rootAddress   = uint32(0x1000)
		imageAddress  = uint32(0x1100)
		outputAddress = uint32(0x1200)
		sourceAddress = uint32(0x4000)
	)
	writeTestMDPWords(t, bus, sourceAddress, []uint32{0x07e0f800, 0xffff001f})
	writeTestMDPWords(t, bus, rootAddress, []uint32{
		0x00000000,
		0x05000000, imageAddress,
		0x06000000,
	})
	writeTestMDPWords(t, bus, imageAddress, []uint32{
		0x10000000, sourceAddress,
		0x11000004,
		0x12000020,
		0x13000040,
		0x0f000008,
		0x3c000001,
		0x3d000001,
		0x03000000, outputAddress,
		0x01000000,
	})
	writeTestMDPWords(t, bus, outputAddress, testMDPOutputScript(1, 1))

	engine, err := NewQualcommMDPScriptEngine(bus, panel, QualcommMDPProfile{
		CompletionStartOffset: 0x0e04,
		ScriptPointerOffset:   0x0e08,
		RGB565SourceFormat:    0x20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.QueueScript(rootAddress); err != nil {
		t.Fatal(err)
	}
	if err := engine.Advance(1); err != nil {
		t.Fatal(err)
	}
	if want := []uint16{0xf800, 0x07e0, 0x001f, 0xffff}; !reflect.DeepEqual(panel.FrameRGB565(), want) {
		t.Fatalf("MDP frame = %#v, want %#v", panel.FrameRGB565(), want)
	}
	if pixels, updates := panel.WriteCounts(); pixels != 4 || updates != 1 {
		t.Fatalf("panel writes = %d pixels, %d updates", pixels, updates)
	}
}

func TestQualcommMDPScriptEngineRejectsInvalidTransfers(t *testing.T) {
	for _, test := range []struct {
		name        string
		format      uint32
		source      uint32
		wantErrorIs error
	}{
		{name: "unsupported format", format: 0x21, source: 0x4000, wantErrorIs: ErrQualcommMDP},
		{name: "unmapped source", format: 0x20, source: 0x10000, wantErrorIs: cpu.ErrInvalidAddress},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := NewBus()
			if err := bus.MapRAM("script-and-frame", 0, 0x10000); err != nil {
				t.Fatal(err)
			}
			panel := newTestMDPPanel(t, 2, 2)
			writeTestMDPWords(t, bus, 0x1000, []uint32{
				0x10000000, test.source,
				0x11000004,
				0x12000000 | test.format,
				0x03000000, 0x1200,
				0x01000000,
			})
			writeTestMDPWords(t, bus, 0x1200, testMDPOutputScript(1, 1))
			engine, err := NewQualcommMDPScriptEngine(bus, panel, QualcommMDPProfile{
				CompletionStartOffset: 0x0e04,
				ScriptPointerOffset:   0x0e08,
				RGB565SourceFormat:    0x20,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.QueueScript(0x1000); err != nil {
				t.Fatal(err)
			}
			if err := engine.Advance(1); !errors.Is(err, test.wantErrorIs) {
				t.Fatalf("Advance error = %v, want %v", err, test.wantErrorIs)
			}
		})
	}
}

func TestQualcommMDPScriptEngineAcceptsNonImageConfiguration(t *testing.T) {
	bus := NewBus()
	if err := bus.MapRAM("script", 0, 0x2000); err != nil {
		t.Fatal(err)
	}
	writeTestMDPWords(t, bus, 0x1000, []uint32{
		0x00000000, 0x3effffff,
		0x1f000254, 0x1f010000, 0x1f020331,
		0x06000000,
	})
	engine, err := NewQualcommMDPScriptEngine(bus, newTestMDPPanel(t, 2, 2), QualcommMDPProfile{
		CompletionStartOffset: 0x0e04,
		ScriptPointerOffset:   0x0e08,
		RGB565SourceFormat:    0x20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.QueueScript(0x1000); err != nil {
		t.Fatal(err)
	}
	if err := engine.Advance(1); err != nil {
		t.Fatal(err)
	}
}

func newTestMDPPanel(t *testing.T, width, height uint16) *DCSPanelController {
	t.Helper()
	panel, err := NewDCSPanelController(DCSPanelConfig{Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []uint16{dcsExitSleepMode, dcsSetDisplayOn, dcsSetPixelFormat} {
		if err := panel.WriteCommand(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := panel.WriteData(dcsPixelFormatRGB565); err != nil {
		t.Fatal(err)
	}
	return panel
}

func writeTestMDPWords(t *testing.T, bus *Bus, address uint32, words []uint32) {
	t.Helper()
	var encoded [4]byte
	for index, word := range words {
		binary.LittleEndian.PutUint32(encoded[:], word)
		if err := bus.Write(address+uint32(index*4), encoded[:], cpu.PermissionWrite); err != nil {
			t.Fatal(err)
		}
	}
}

func testMDPOutputScript(columnEnd, pageEnd uint16) []uint32 {
	return []uint32{
		0x0a000000, 0x20000000,
		0x0c000000, 0x20100000,
		0x0b00002a,
		0x0e000000,
		0x0e000000,
		0x0e000000 | uint32(columnEnd>>8),
		0x0e000000 | uint32(columnEnd&0xff),
		0x0b00002b,
		0x0e000000,
		0x0e000000,
		0x0e000000 | uint32(pageEnd>>8),
		0x0e000000 | uint32(pageEnd&0xff),
		0x0b00002c,
		0x04000000,
	}
}
