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
