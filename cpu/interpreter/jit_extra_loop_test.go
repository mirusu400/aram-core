package interpreter

import "testing"

func TestThumbExtraLoopTranslation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend func(*testing.T) *Backend
		kind    jitExtraLoopKind
		length  uint32
	}{
		{
			name:    "object lookup",
			backend: func(t *testing.T) *Backend { return newThumbObjectLookupTestBackend(t, NewJIT()) },
			kind:    jitObjectLookupLoop,
			length:  thumbObjectLookupInstructions,
		},
		{
			name:    "indexed palette",
			backend: func(t *testing.T) *Backend { return newThumbIndexedPaletteTestBackend(t, NewJIT(), 0x5000) },
			kind:    jitIndexedPaletteLoop,
			length:  thumbIndexedPaletteLoopInstructions,
		},
		{
			name:    "transparent palette",
			backend: func(t *testing.T) *Backend { return newThumbTransparentPaletteTestBackend(t, NewJIT(), false, 0x5000) },
			kind:    jitTransparentPaletteLoop,
			length:  thumbTransparentPaletteLoopInstructions,
		},
		{
			name:    "stack palette",
			backend: func(t *testing.T) *Backend { return newThumbStackPaletteTestBackend(t, NewJIT(), 0x5000) },
			kind:    jitStackPaletteLoop,
			length:  thumbStackPaletteLoopInstructions,
		},
		{
			name:    "halfword fill",
			backend: func(t *testing.T) *Backend { return newThumbHalfwordFillTestBackend(t, NewJIT(), 0x5000) },
			kind:    jitHalfwordFillLoop,
			length:  thumbHalfwordFillLoopInstructions,
		},
		{
			name:    "color",
			backend: func(t *testing.T) *Backend { return newThumbColorTestBackend(t, NewJIT(), 0) },
			kind:    jitColorLoop,
			length:  thumbColorLoopInstructions,
		},
		{
			name:    "fill",
			backend: func(t *testing.T) *Backend { return newThumbFillTestBackend(t, NewJIT(), fillLoopTestCases()[0], 0) },
			kind:    jitFillLoop,
			length:  thumbFillLoopInstructions,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := tc.backend(t).translateThumbBlock(0x1000)
			if block == nil || block.extraLoop == nil {
				t.Fatal("exact loop did not select the extra dispatch slot")
			}
			if block.extraLoop.kind != tc.kind || block.end != 0x1000+tc.length*2 {
				t.Fatalf("translated loop kind=%d end=%08x", block.extraLoop.kind, block.end)
			}
		})
	}

	plain := newThumbIndexedPaletteTestBackend(t, NewJIT(), 0x5000)
	check(t, plain.WriteMemory(0x1000, []byte{0xc0, 0x46})) // Thumb NOP.
	block := plain.translateThumbBlock(0x1000)
	if block == nil || block.extraLoop != nil {
		t.Fatal("ordinary Thumb block selected an extra loop")
	}
}

func TestThumbExtraLoopFirstWordsDoNotOverlap(t *testing.T) {
	words := []uint16{
		thumbObjectLookupWords[0], thumbIndexedPaletteLoopWords[0],
		thumbTransparentPaletteLoopWords[0], thumbStackPaletteLoopWords[0],
		thumbHalfwordFillLoopWords[0], 0x8832, 0x4640, 0x1843,
	}
	for i, word := range words {
		if word&0xffc0 == 0x7800 {
			t.Fatalf("exact loop %d overlaps palette prefix", i)
		}
		for j := 0; j < i; j++ {
			if word == words[j] {
				t.Fatalf("exact loop %d overlaps %d", i, j)
			}
		}
	}
}
