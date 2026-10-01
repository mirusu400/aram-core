package brewrt

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

func TestBREWCompatibilityPatchesRequireDigestAndOriginalInstructions(t *testing.T) {
	for _, test := range []struct {
		name, digest string
		offset       uint32
		original     []byte
	}{
		{"ranking cancellation", swordMasterModuleSHA256, 0x6ac6, []byte{0x0c, 0x49, 0x79, 0x44}},
		{"key dispatch", kashanModuleSHA256, 0x16a84, []byte{0x01, 0x68, 0x0a, 0x69}},
	} {
		t.Run(test.name, func(t *testing.T) {
			module := make([]byte, int(test.offset)+4)
			copy(module[test.offset:], test.original)
			originalModule := bytes.Clone(module)
			imageData := make([]byte, len(module)+8+int(moduleBSSSlack))
			copy(imageData[8:], module)
			originalImage := bytes.Clone(imageData)

			// Recognizable instructions alone must not enable a title patch.
			unmatched, err := patchBREWModule(module, imageData)
			if err != nil || !bytes.Equal(unmatched, originalImage) {
				t.Fatalf("unqualified module was patched: err=%v", err)
			}
			lookalike, err := patchBREWModuleForDigest("0"+test.digest[1:], module, imageData)
			if err != nil || !bytes.Equal(lookalike, originalImage) {
				t.Fatalf("lookalike digest enabled patch: err=%v", err)
			}
			patched, err := patchBREWModuleForDigest(test.digest, module, imageData)
			if err != nil || bytes.Equal(patched[8+test.offset:8+test.offset+4], test.original) {
				t.Fatalf("qualified loaded-copy patch missing: err=%v", err)
			}
			if !bytes.Equal(module, originalModule) {
				t.Fatal("patch modified archive module bytes")
			}
			badModule := bytes.Clone(module)
			badModule[test.offset] ^= 1
			if _, err := patchBREWModuleForDigest(test.digest, badModule, bytes.Clone(originalImage)); err == nil {
				t.Fatal("changed original instructions accepted")
			}
			badImage := bytes.Clone(originalImage)
			badImage[8+test.offset] ^= 1
			if _, err := patchBREWModuleForDigest(test.digest, module, badImage); err == nil {
				t.Fatal("changed loaded instructions accepted")
			}
			if _, err := patchBREWModuleForDigest(test.digest, module[:test.offset], originalImage); err == nil {
				t.Fatal("truncated module accepted")
			}
		})
	}
}

func TestKashanKeyGuardPreservesLiveScreenAndSkipsAbsentScreen(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "present"}[present], func(t *testing.T) {
			module := make([]byte, 0x16b10)
			for index, instruction := range []uint16{
				0xb500, // push {lr}
				0x46c0, // nop
				0x6801, // the two expected original vtable loads
				0x690a,
				0x6022, // str r2, [r4]
				0xbd00, // pop {pc}
			} {
				binary.LittleEndian.PutUint16(module[0x16a80+index*2:], instruction)
			}
			for index, instruction := range []uint16{
				0x2209, // absent-screen epilogue: marker 9
				0x6022, // str r2, [r4]
				0xbd00, // pop {pc}
			} {
				binary.LittleEndian.PutUint16(module[0x16afe+index*2:], instruction)
			}
			imageData := make([]byte, len(module)+8+int(moduleBSSSlack))
			copy(imageData[8:], module)
			imageData, err := patchBREWModuleForDigest(kashanModuleSHA256, module, imageData)
			if err != nil {
				t.Fatal(err)
			}
			backend := interpreter.New()
			defer backend.Close()
			for _, mapping := range []struct {
				address, size uint32
				permissions   cpu.Permissions
			}{
				{moduleBase - 8, uint32(len(imageData)), cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
				{heapBase, 4096, cpu.PermissionRead | cpu.PermissionWrite},
				{stackBase, 4096, cpu.PermissionRead | cpu.PermissionWrite},
				{returnTrap, 2, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
			} {
				if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
					t.Fatal(err)
				}
			}
			if err := backend.WriteMemory(moduleBase-8, imageData); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(returnTrap, []byte{0x00, 0xbe}); err != nil {
				t.Fatal(err)
			}
			object, vtable := heapBase+0x100, heapBase+0x200
			var pointer [4]byte
			binary.LittleEndian.PutUint32(pointer[:], vtable)
			if err := backend.WriteMemory(object, pointer[:]); err != nil {
				t.Fatal(err)
			}
			binary.LittleEndian.PutUint32(pointer[:], 0x12345678)
			if err := backend.WriteMemory(vtable+16, pointer[:]); err != nil {
				t.Fatal(err)
			}
			if !present {
				object = 0
			}
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: object, cpu.RegisterR4: heapBase,
				cpu.RegisterSP: stackBase + 0x800, cpu.RegisterLR: returnTrap | 1,
			} {
				if err := backend.WriteRegister(register, value); err != nil {
					t.Fatal(err)
				}
			}
			result := backend.Run(context.Background(), moduleBase+0x16a80, cpu.ModeThumb, 128)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != returnTrap+2 {
				t.Fatalf("guard execution=%+v", result)
			}
			if err := backend.ReadMemory(heapBase, pointer[:]); err != nil {
				t.Fatal(err)
			}
			want := uint32(9)
			if present {
				want = 0x12345678
			}
			if got := binary.LittleEndian.Uint32(pointer[:]); got != want {
				t.Fatalf("guard path marker=%08x, want %08x", got, want)
			}
		})
	}
}
