package gvmhost

import "testing"

func TestSKTGammaColorNormalRange(t *testing.T) {
	seen := make(map[byte]struct{}, 124)
	for index := uint8(16); index <= 139; index++ {
		packed, ok := SKTGammaColor(3, index)
		if !ok {
			t.Fatalf("index %d unsupported", index)
		}
		if _, duplicate := seen[packed]; duplicate {
			t.Fatalf("index %d produced duplicate packed byte %02x", index, packed)
		}
		seen[packed] = struct{}{}
	}
	if len(seen) != 124 {
		t.Fatalf("unique colors = %d, want 124", len(seen))
	}

	want := map[uint8]byte{
		16:  1,
		46:  31,
		47:  64,
		55:  72,
		56:  74,
		77:  95,
		78:  128,
		95:  145,
		96:  147,
		108: 159,
		109: 224,
		139: 254,
	}
	for index, expected := range want {
		got, ok := SKTGammaColor(3, index)
		if !ok || got != expected {
			t.Fatalf("index %d = (%02x,%v), want (%02x,true)", index, got, ok, expected)
		}
	}
}

func TestSKTGammaColorFixedColorsAndGamma(t *testing.T) {
	fixed := []byte{0xff, 0x92, 0x49, 0x00, 0x00}
	for index, expected := range fixed {
		got, ok := SKTGammaColor(3, uint8(index))
		if !ok || got != expected {
			t.Fatalf("gamma3 index %d = (%02x,%v), want (%02x,true)", index, got, ok, expected)
		}
	}

	for _, index := range []uint8{0, 1, 2, 3, 4, 16, 47, 109, 139} {
		bright, ok := SKTGammaColor(0, index)
		if !ok {
			t.Fatalf("gamma0 index %d unsupported", index)
		}
		dark, ok := SKTGammaColor(6, index)
		if !ok || dark != 0 {
			t.Fatalf("gamma6 index %d = (%02x,%v), want (00,true)", index, dark, ok)
		}
		if index != 3 && index != 4 && bright == 0 {
			t.Fatalf("gamma0 index %d unexpectedly black", index)
		}
	}
}

func TestSKTGammaColorRejectsUnmodeledRanges(t *testing.T) {
	for _, index := range []uint8{5, 15, 140, 181, 182, 255} {
		if got, ok := SKTGammaColor(3, index); ok {
			t.Fatalf("index %d unexpectedly mapped to %02x", index, got)
		}
	}
	if got, ok := SKTGammaColor(7, 16); ok {
		t.Fatalf("gamma 7 unexpectedly mapped to %02x", got)
	}
}
