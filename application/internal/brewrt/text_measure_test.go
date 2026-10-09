package brewrt

import (
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestDisplayMeasureTextExUsesDrawnGlyphWidths(t *testing.T) {
	r := newSyntheticRuntime(t)
	textAt, stackAt, fitsAt := heapBase+0x200, heapBase+0x300, heapBase+0x400
	// ASCII advances seven pixels; the handset Hangul glyph advances thirteen.
	for _, units := range [][]uint16{{'A', 0xac00, 'B', 0}, {'A', 0xa1b0, 'B', 0}} {
		encoded := make([]byte, len(units)*2)
		for i, unit := range units {
			binary.LittleEndian.PutUint16(encoded[i*2:], unit)
		}
		if err := r.cpu.WriteMemory(textAt, encoded); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			limit       int32
			width, fits uint32
		}{{-1, 27, 3}, {0, 0, 0}, {6, 0, 0}, {7, 7, 1}, {19, 7, 1}, {20, 20, 2}, {26, 20, 2}, {27, 27, 3}} {
			var args [8]byte
			binary.LittleEndian.PutUint32(args[:], uint32(test.limit))
			binary.LittleEndian.PutUint32(args[4:], fitsAt)
			if err := r.cpu.WriteMemory(stackAt, args[:]); err != nil {
				t.Fatal(err)
			}
			for reg, value := range map[uint32]uint32{cpu.RegisterR2: textAt, cpu.RegisterR3: ^uint32(0), cpu.RegisterSP: stackAt} {
				if err := r.cpu.WriteRegister(reg, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.measureDisplayText(); err != nil {
				t.Fatal(err)
			}
			width, err := r.cpu.ReadRegister(cpu.RegisterR0)
			if err != nil {
				t.Fatal(err)
			}
			var fits [4]byte
			if err := r.cpu.ReadMemory(fitsAt, fits[:]); err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(fits[:]); width != test.width || got != test.fits {
				t.Fatalf("units=%x limit=%d: width=%d fits=%d, want %d/%d", units, test.limit, width, got, test.width, test.fits)
			}
		}
	}
}

func TestDisplayMeasureTextExRejectsInvalidCounts(t *testing.T) {
	r := newSyntheticRuntime(t)
	for _, count := range []uint32{4097, ^uint32(1)} {
		if err := r.cpu.WriteRegister(cpu.RegisterR2, heapBase); err != nil {
			t.Fatal(err)
		}
		if err := r.cpu.WriteRegister(cpu.RegisterR3, count); err != nil {
			t.Fatal(err)
		}
		if err := r.measureDisplayText(); err == nil {
			t.Fatalf("MeasureTextEx accepted count %d", int32(count))
		}
	}
}
