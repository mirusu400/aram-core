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
		{"zero PRNG seed", blackComicsModuleSHA256, 0x2008, []byte{0x00, 0x6c, 0x11, 0x4b}},
		{"null teardown store", ragnarokKafraModuleSHA256, 0x1f0c0, []byte{0x10, 0x00, 0x90, 0xe5}},
		{"empty image marker", mudaeriOmokModuleSHA256, 0xb22e, []byte{0x00, 0x29, 0x0c, 0x9f}},
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

func TestMusicMatgoAppletCallsRequireBothOriginalInstructions(t *testing.T) {
	module := make([]byte, 0x1d9c8)
	original := []byte{0x04, 0x00, 0xa0, 0xe1} // mov r0, r4
	for _, offset := range []int{0x1d9bc, 0x1d9c4} {
		copy(module[offset:], original)
	}
	imageData := make([]byte, len(module)+8+int(moduleBSSSlack))
	copy(imageData[8:], module)
	untouched := bytes.Clone(imageData)
	if got, err := patchBREWModule(module, imageData); err != nil || !bytes.Equal(got, untouched) {
		t.Fatalf("unmatched module was patched: %v", err)
	}
	patched, err := patchBREWModuleForDigest(musicMatgoModuleSHA256, module, bytes.Clone(imageData))
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{0x1d9bc, 0x1d9c4} {
		if got := patched[8+offset : 8+offset+4]; !bytes.Equal(got, []byte{0x28, 0x00, 0x84, 0xe2}) {
			t.Fatalf("call at offset 0x%x = %x", offset, got)
		}
		if got := module[offset : offset+4]; !bytes.Equal(got, original) {
			t.Fatalf("archive code at offset 0x%x changed to %x", offset, got)
		}
		bad := bytes.Clone(module)
		bad[offset] ^= 1
		if _, err := patchBREWModuleForDigest(musicMatgoModuleSHA256, bad, bytes.Clone(imageData)); err == nil {
			t.Fatalf("changed call at offset 0x%x was accepted", offset)
		}
	}
}

func TestBlackComicsSeedGuardPreservesNonzeroState(t *testing.T) {
	for _, test := range []struct {
		name          string
		initial, want uint32
	}{
		{"zero", 0, 0x13579bdf},
		{"existing", 0x2468ace0, 0x2468ace0},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := interpreter.New()
			defer backend.Close()
			for _, mapping := range []struct {
				address, size uint32
				permissions   cpu.Permissions
			}{
				{moduleBase, 32, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
				{heapBase, 256, cpu.PermissionRead | cpu.PermissionWrite},
				{returnTrap, 2, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
			} {
				if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
					t.Fatal(err)
				}
			}
			if err := backend.WriteMemory(moduleBase, blackComicsSeedGuard()); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(returnTrap, []byte{0x00, 0xbe}); err != nil {
				t.Fatal(err)
			}
			var word [4]byte
			binary.LittleEndian.PutUint32(word[:], test.initial)
			if err := backend.WriteMemory(heapBase+0x40, word[:]); err != nil {
				t.Fatal(err)
			}
			for register, value := range map[uint32]uint32{
				cpu.RegisterR4: heapBase,
				cpu.RegisterLR: returnTrap | 1,
			} {
				if err := backend.WriteRegister(register, value); err != nil {
					t.Fatal(err)
				}
			}
			result := backend.Run(context.Background(), moduleBase, cpu.ModeThumb, 32)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != returnTrap+2 {
				t.Fatalf("seed guard execution=%+v", result)
			}
			if got, err := backend.ReadRegister(cpu.RegisterR0); err != nil || got != test.want {
				t.Fatalf("seed=%08x, want %08x: %v", got, test.want, err)
			}
			if got, err := backend.ReadRegister(cpu.RegisterR3); err != nil || got != 0x18c94 {
				t.Fatalf("multiplier=%08x, want 00018c94: %v", got, err)
			}
			if err := backend.ReadMemory(heapBase+0x40, word[:]); err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(word[:]); got != test.initial {
				t.Fatalf("stored state=%08x, want %08x", got, test.initial)
			}
		})
	}
}

func TestRagnarokKafraNullStoreGuard(t *testing.T) {
	for _, test := range []struct {
		name          string
		parent, child uint32
		want          uint32
	}{
		{"null parent", 0, 0, 0},
		{"null child", heapBase + 0x100, 0, 0},
		{"live child", heapBase + 0x100, heapBase + 0x200, 0x12345678},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := interpreter.New()
			defer backend.Close()
			for _, mapping := range []struct {
				address, size uint32
				permissions   cpu.Permissions
			}{
				{moduleBase, 0x20000, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
				{heapBase, 4096, cpu.PermissionRead | cpu.PermissionWrite},
				{returnTrap, 2, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
			} {
				if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
					t.Fatal(err)
				}
			}
			guardAddress := moduleBase + 0x3000
			branch, err := brewARMBranch(moduleBase+0x1f0c0, guardAddress)
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(moduleBase+0x1f0c0, branch); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(moduleBase+0x1f0c8, []byte{0x1e, 0xff, 0x2f, 0xe1}); err != nil { // bx lr
				t.Fatal(err)
			}
			guard, err := ragnarokKafraNullStoreGuard(guardAddress)
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(guardAddress, guard); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(returnTrap, []byte{0x00, 0xbe}); err != nil {
				t.Fatal(err)
			}
			var word [4]byte
			binary.LittleEndian.PutUint32(word[:], test.child)
			if err := backend.WriteMemory(heapBase+0x110, word[:]); err != nil {
				t.Fatal(err)
			}
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: test.parent,
				cpu.RegisterR5: 0x12345678,
				cpu.RegisterLR: returnTrap | 1,
			} {
				if err := backend.WriteRegister(register, value); err != nil {
					t.Fatal(err)
				}
			}
			result := backend.Run(context.Background(), moduleBase+0x1f0c0, cpu.ModeARM, 64)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != returnTrap+2 {
				t.Fatalf("null-store guard execution=%+v", result)
			}
			if err := backend.ReadMemory(heapBase+0x200+0x530, word[:]); err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(word[:]); got != test.want {
				t.Fatalf("child field=%08x, want %08x", got, test.want)
			}
		})
	}
}

func TestMudaeriOmokImageGuard(t *testing.T) {
	for _, test := range []struct {
		name  string
		image uint32
		empty bool
	}{
		{"null", 0, true},
		{"removed", ^uint32(0), true},
		{"live", heapBase + 0x100, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := interpreter.New()
			defer backend.Close()
			for _, mapping := range []struct {
				address, size uint32
				permissions   cpu.Permissions
			}{
				{moduleBase, 32, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
				{stackBase, 512, cpu.PermissionRead | cpu.PermissionWrite},
				{returnTrap, 2, cpu.PermissionRead | cpu.PermissionWrite | cpu.PermissionExecute},
			} {
				if err := backend.Map(mapping.address, mapping.size, mapping.permissions); err != nil {
					t.Fatal(err)
				}
			}
			if err := backend.WriteMemory(moduleBase, mudaeriOmokImageGuard()); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(returnTrap, []byte{0x00, 0xbe}); err != nil {
				t.Fatal(err)
			}
			var word [4]byte
			binary.LittleEndian.PutUint32(word[:], 0x11223344)
			if err := backend.WriteMemory(stackBase+0x130, word[:]); err != nil {
				t.Fatal(err)
			}
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: 0x12345678,
				cpu.RegisterR1: test.image,
				cpu.RegisterR3: 0x87654321,
				cpu.RegisterSP: stackBase + 0x100,
				cpu.RegisterLR: returnTrap | 1,
			} {
				if err := backend.WriteRegister(register, value); err != nil {
					t.Fatal(err)
				}
			}
			result := backend.Run(context.Background(), moduleBase, cpu.ModeThumb, 32)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != returnTrap+2 {
				t.Fatalf("image guard execution=%+v", result)
			}
			for register, want := range map[uint32]uint32{
				cpu.RegisterR0: 0x12345678,
				cpu.RegisterR1: test.image,
				cpu.RegisterR3: 0x87654321,
				cpu.RegisterR7: 0x11223344,
				cpu.RegisterSP: stackBase + 0x100,
			} {
				got, err := backend.ReadRegister(register)
				if err != nil || got != want {
					t.Fatalf("r%d=%08x, want %08x: %v", register, got, want, err)
				}
			}
			cpsr, err := backend.ReadRegister(cpu.RegisterCPSR)
			if err != nil || (cpsr&(1<<30) != 0) != test.empty {
				t.Fatalf("zero flag in CPSR=%08x, want empty=%t: %v", cpsr, test.empty, err)
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
