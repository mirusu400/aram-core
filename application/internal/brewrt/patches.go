package brewrt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const (
	swordMasterModuleSHA256     = "40344eff584388c6c78187e95f5d16bfbbb1971e59de57b481aeaf673e477645"
	kashanModuleSHA256          = "ca382383e4654072f6ec7e65225a8270e67d4b10b5f6a4e2bc0687a0f40b2f2e"
	yeolhyeolganghoModuleSHA256 = "5435150afb92668f8a670cfcc6f7ad18ec1e8ed17d220cbc6a834f24e80d7a86"
	legendOfEllosModuleSHA256   = "2d4a9519fc7d2df92075a6d1af76990d46844d6462cfb931ff312268389c87bb"
	blackComicsModuleSHA256     = "529c9ebf20a5ddb019b89b08b4b810cfb67efe5cabbb1d4c3ab56089122791d9"
	ragnarokKafraModuleSHA256   = "2836e3b795ebe05933e654e736fa81d70b2103b018b31f78bc3fd598530544e1"
	mudaeriOmokModuleSHA256     = "30855c99061b1dd3515be32e6dbac518f23dd79a8174a29bc0c314cc877a8173"
	musicMatgoModuleSHA256      = "ab315ca8ccedf2af13b63b4564c2c4dfe283a37a89b3d320d5c9b2f339ac68b2"
	rummikubModuleSHA256        = "a05cab8903da96c29864e0a5af5ff49793274dd497cdfbfe2d88b40906fae442"
)

func coalesceDuplicateTimerModule(module []byte) bool {
	// #396 posts the same timer deadline recursively and otherwise floods the queue.
	digest := sha256.Sum256(module)
	return hex.EncodeToString(digest[:]) == yeolhyeolganghoModuleSHA256
}

func deliverCanceledDueTimerModule(module []byte) bool {
	// #313 expects an expired callback to run after another callback cancels it.
	digest := sha256.Sum256(module)
	return hex.EncodeToString(digest[:]) == legendOfEllosModuleSHA256
}

// patchBREWModule applies narrowly identified handset compatibility fixes to
// the loaded copy, never to archive files. Both the complete module digest and
// original instructions must match; other builds retain their original code.
func patchBREWModule(module, imageData []byte) ([]byte, error) {
	digest := sha256.Sum256(module)
	return patchBREWModuleForDigest(hex.EncodeToString(digest[:]), module, imageData)
}

func patchBREWModuleForDigest(digest string, module, imageData []byte) ([]byte, error) {
	switch digest {
	case swordMasterModuleSHA256:
		// Ranking-screen teardown cancels its UI callback (0x16559), but
		// leaves its network poller (0x166d1) queued with the same context.
		// Cancel both context-owned timers before the released INetMgr can
		// be used again: PFNNOTIFY=NULL, pUser unchanged.
		if err := replaceBREWInstructions(module, imageData, 0x6ac6,
			[]byte{0x0c, 0x49, 0x79, 0x44}, // ldr r1, literal; add r1, pc
			[]byte{0x00, 0x21, 0xc0, 0x46}, // movs r1, #0; nop
		); err != nil {
			return nil, err
		}
	case kashanModuleSHA256:
		// The key-press dispatcher checks for an absent screen before the
		// old-key notification, but omits the check before the new-key
		// notification. Guard the two vtable loads, retaining the original
		// successful no-screen epilogue and the live-screen dispatch path.
		padding := (4 - len(imageData)%4) % 4
		guardAddress := moduleBase - 8 + uint32(len(imageData)+padding)
		branch, err := brewThumbBL(moduleBase+0x16a84, guardAddress)
		if err != nil {
			return nil, err
		}
		if err := replaceBREWInstructions(module, imageData, 0x16a84,
			[]byte{0x01, 0x68, 0x0a, 0x69}, // ldr r1, [r0]; ldr r2, [r1, #16]
			branch,
		); err != nil {
			return nil, err
		}
		imageData = append(imageData, make([]byte, padding)...)
		imageData = append(imageData, kashanKeyGuard()...)
	case blackComicsModuleSHA256:
		// The title's PRNG leaves a zero seed at zero forever. A later scene
		// repeatedly requests a nonzero result, so initialize only the zero
		// state before the original PRNG update.
		padding := (4 - len(imageData)%4) % 4
		guardAddress := moduleBase - 8 + uint32(len(imageData)+padding)
		branch, err := brewThumbBL(moduleBase+0x2008, guardAddress)
		if err != nil {
			return nil, err
		}
		if err := replaceBREWInstructions(module, imageData, 0x2008,
			[]byte{0x00, 0x6c, 0x11, 0x4b}, // ldr r0, [r0, #64]; ldr r3, multiplier
			branch,
		); err != nil {
			return nil, err
		}
		imageData = append(imageData, make([]byte, padding)...)
		imageData = append(imageData, blackComicsSeedGuard()...)
	case ragnarokKafraModuleSHA256:
		// The back-key teardown clears object+0x3c8, then immediately follows
		// that same pointer and stores through its child. Skip the stale store
		// only when either pointer is null; preserve the live-object path.
		padding := (4 - len(imageData)%4) % 4
		guardAddress := moduleBase - 8 + uint32(len(imageData)+padding)
		branch, err := brewARMBranch(moduleBase+0x1f0c0, guardAddress)
		if err != nil {
			return nil, err
		}
		if err := replaceBREWInstructions(module, imageData, 0x1f0c0,
			[]byte{0x10, 0x00, 0x90, 0xe5}, // ldr r0, [r0, #16]
			branch,
		); err != nil {
			return nil, err
		}
		guard, err := ragnarokKafraNullStoreGuard(guardAddress)
		if err != nil {
			return nil, err
		}
		imageData = append(imageData, make([]byte, padding)...)
		imageData = append(imageData, guard...)
	case mudaeriOmokModuleSHA256:
		// Teardown writes 0xffffffff as an empty image marker. The draw
		// helper already skips null images, so include this marker in its
		// empty-image check before it dereferences the image pointer.
		padding := (4 - len(imageData)%4) % 4
		guardAddress := moduleBase - 8 + uint32(len(imageData)+padding)
		branch, err := brewThumbBL(moduleBase+0xb22e, guardAddress)
		if err != nil {
			return nil, err
		}
		if err := replaceBREWInstructions(module, imageData, 0xb22e,
			[]byte{0x00, 0x29, 0x0c, 0x9f}, // cmp r1, #0; ldr r7, [sp, #0x30]
			branch,
		); err != nil {
			return nil, err
		}
		imageData = append(imageData, make([]byte, padding)...)
		imageData = append(imageData, mudaeriOmokImageGuard()...)
	case musicMatgoModuleSHA256:
		// The menu's module-context branch calls two applet methods with the
		// module object. For this authenticated build, the 0x28-byte module
		// allocation is immediately followed by its applet allocation. Pass
		// the applet object to both methods so their image and network fields
		// refer to the applet rather than the module's inline vtable.
		if err := replaceBREWInstructions(module, imageData, 0x1d9bc,
			[]byte{0x04, 0x00, 0xa0, 0xe1}, // mov r0, r4
			[]byte{0x28, 0x00, 0x84, 0xe2}, // add r0, r4, #0x28
		); err != nil {
			return nil, err
		}
		if err := replaceBREWInstructions(module, imageData, 0x1d9c4,
			[]byte{0x04, 0x00, 0xa0, 0xe1}, // mov r0, r4
			[]byte{0x28, 0x00, 0x84, 0xe2}, // add r0, r4, #0x28
		); err != nil {
			return nil, err
		}
	case rummikubModuleSHA256:
		// #518 redraws a cleared selection whose tile code is zero. Subtracting
		// 'a' produces -97, which the draw helper uses as an image-table index.
		// Skip that absent tile while preserving the original nonnegative index
		// comparison and switch, including the existing joker handling.
		padding := (4 - len(imageData)%4) % 4
		guardAddress := moduleBase - 8 + uint32(len(imageData)+padding)
		branch, err := brewThumbBL(moduleBase+0x1eaa, guardAddress)
		if err != nil {
			return nil, err
		}
		if err := replaceBREWInstructions(module, imageData, 0x1eaa,
			[]byte{0x1d, 0x1c, 0x10, 0x2d}, // adds r5, r3, #0; cmp r5, #16
			branch,
		); err != nil {
			return nil, err
		}
		imageData = append(imageData, make([]byte, padding)...)
		imageData = append(imageData, rummikubTileGuard()...)
	}
	return imageData, nil
}

func rummikubTileGuard() []byte {
	guard := make([]byte, 20)
	for index, instruction := range []uint16{
		0x1c1d, // adds r5, r3, #0
		0x2d00, // cmp r5, #0
		0xda01, // bge original comparison
		0x4b02, // ldr r3, epilogue
		0x4718, // bx r3
		0x2d10, // cmp r5, #16
		0x4770, // bx lr
		0x46c0, // alignment
	} {
		binary.LittleEndian.PutUint16(guard[index*2:], instruction)
	}
	binary.LittleEndian.PutUint32(guard[16:], moduleBase+0x1f34|1)
	return guard
}

func replaceBREWInstructions(module, imageData []byte, offset uint32, original, replacement []byte) error {
	end := uint64(offset) + uint64(len(original))
	if len(original) != len(replacement) || end > uint64(len(module)) || end+8 > uint64(len(imageData)) ||
		!bytes.Equal(module[offset:end], original) || !bytes.Equal(imageData[8+offset:8+end], original) {
		return fmt.Errorf("BREW compatibility instruction mismatch at module offset 0x%x", offset)
	}
	copy(imageData[8+offset:8+end], replacement)
	return nil
}

func brewThumbBL(source, target uint32) ([]byte, error) {
	delta := int64(target) - int64(source) - 4
	if source&1 != 0 || target&1 != 0 || delta < -(1<<22) || delta >= 1<<22 {
		return nil, fmt.Errorf("BREW compatibility Thumb branch is out of range")
	}
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint16(encoded, 0xf000|uint16((delta>>12)&0x7ff))
	binary.LittleEndian.PutUint16(encoded[2:], 0xf800|uint16((delta>>1)&0x7ff))
	return encoded, nil
}

func kashanKeyGuard() []byte {
	guard := make([]byte, 20)
	for index, instruction := range []uint16{
		0x2800, // cmp r0, #0
		0xd002, // beq absent
		0x6801, // ldr r1, [r0]
		0x690a, // ldr r2, [r1, #16]
		0x4770, // bx lr
		0x4b01, // absent: ldr r3, epilogue
		0x4718, // bx r3
		0x46c0, // alignment
	} {
		binary.LittleEndian.PutUint16(guard[index*2:], instruction)
	}
	binary.LittleEndian.PutUint32(guard[16:], moduleBase+0x16afe|1)
	return guard
}

func blackComicsSeedGuard() []byte {
	guard := make([]byte, 20)
	for index, instruction := range []uint16{
		0x6c20, // ldr r0, [r4, #64]
		0x2800, // cmp r0, #0
		0xd100, // bne load multiplier
		0x4801, // ldr r0, seed
		0x4b01, // ldr r3, multiplier
		0x4770, // bx lr
	} {
		binary.LittleEndian.PutUint16(guard[index*2:], instruction)
	}
	binary.LittleEndian.PutUint32(guard[12:], 0x13579bdf)
	binary.LittleEndian.PutUint32(guard[16:], 0x00018c94)
	return guard
}

func brewARMBranch(source, target uint32) ([]byte, error) {
	delta := int64(target) - int64(source) - 8
	if source&3 != 0 || target&3 != 0 || delta < -(1<<25) || delta >= 1<<25 || delta&3 != 0 {
		return nil, fmt.Errorf("BREW compatibility ARM branch is out of range")
	}
	branch := make([]byte, 4)
	binary.LittleEndian.PutUint32(branch, 0xea000000|uint32(delta>>2)&0x00ffffff)
	return branch, nil
}

func ragnarokKafraNullStoreGuard(address uint32) ([]byte, error) {
	guard := make([]byte, 20)
	for index, instruction := range []uint32{
		0xe3500000, // cmp r0, #0
		0x15900010, // ldrne r0, [r0, #16]
		0x13500000, // cmpne r0, #0
		0x15805530, // strne r5, [r0, #0x530]
	} {
		binary.LittleEndian.PutUint32(guard[index*4:], instruction)
	}
	branch, err := brewARMBranch(address+16, moduleBase+0x1f0c8)
	if err != nil {
		return nil, err
	}
	copy(guard[16:], branch)
	return guard, nil
}

func mudaeriOmokImageGuard() []byte {
	guard := make([]byte, 14)
	for index, instruction := range []uint16{
		0x2900, // cmp r1, #0
		0xd002, // beq preserve the empty flag
		0xb401, // push {r0}
		0x1c48, // adds r0, r1, #1: also flag 0xffffffff as empty
		0xbc01, // pop {r0}, preserving the flags
		0x9f0c, // ldr r7, [sp, #0x30]
		0x4770, // bx lr
	} {
		binary.LittleEndian.PutUint16(guard[index*2:], instruction)
	}
	return guard
}
