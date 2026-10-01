package brewrt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const (
	swordMasterModuleSHA256 = "40344eff584388c6c78187e95f5d16bfbbb1971e59de57b481aeaf673e477645"
	kashanModuleSHA256      = "ca382383e4654072f6ec7e65225a8270e67d4b10b5f6a4e2bc0687a0f40b2f2e"
)

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
	}
	return imageData, nil
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
