package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// TestJITSelfModifyingCodeInvalidatesTranslation proves the SMC range-check
// still invalidates a translated block when the guest code it covers is
// overwritten. The optimization narrows invalidation to writes that overlap
// translated code (so blitter writes elsewhere in the same RWX region are
// cheap); it must never keep a stale translation for code that actually
// changed.
func TestJITSelfModifyingCodeInvalidatesTranslation(t *testing.T) {
	backend := NewJIT()
	mapCodeAndStack(t, backend) // 0x1000 is read-write-execute

	// movs r0, #1 ; bkpt
	if err := backend.WriteMemory(0x1000, []byte{0x01, 0x20, 0x00, 0xbe}); err != nil {
		t.Fatal(err)
	}
	if r := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8); r.Err != nil ||
		r.Reason != cpu.StopBreakpoint {
		t.Fatalf("first run = %+v", r)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("r0 after first run = %d, want 1", got)
	}

	// Overwrite the same code in place: movs r0, #2 ; bkpt. This write lands
	// inside the translated block's span, so it must invalidate the cache.
	if err := backend.WriteMemory(0x1000, []byte{0x02, 0x20, 0x00, 0xbe}); err != nil {
		t.Fatal(err)
	}
	if r := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8); r.Err != nil ||
		r.Reason != cpu.StopBreakpoint {
		t.Fatalf("second run = %+v", r)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 2 {
		t.Fatalf("r0 after self-modifying rewrite = %d, want 2 (stale translation not invalidated)", got)
	}
}

func TestARMJITSelfModifyingCodeInvalidatesTranslation(t *testing.T) {
	backend := NewJIT()
	mapCodeAndStack(t, backend)

	code := make([]byte, 8)
	binary.LittleEndian.PutUint32(code[0:4], 0xe3a00001) // mov r0, #1
	binary.LittleEndian.PutUint32(code[4:8], 0xe1200070) // bkpt
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8); result.Err != nil ||
		result.Reason != cpu.StopBreakpoint {
		t.Fatalf("first run = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("r0 after first run = %d, want 1", got)
	}
	if len(backend.armJITBlocks) == 0 {
		t.Fatal("ARM run did not retain a translated block")
	}

	binary.LittleEndian.PutUint32(code[0:4], 0xe3a00002) // mov r0, #2
	if err := backend.WriteMemory(0x1000, code[:4]); err != nil {
		t.Fatal(err)
	}
	if result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8); result.Err != nil ||
		result.Reason != cpu.StopBreakpoint {
		t.Fatalf("second run = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 2 {
		t.Fatalf("r0 after ARM rewrite = %d, want 2 (stale translation not invalidated)", got)
	}
}

// TestJITWriteOutsideCodeSpanKeepsExecuting proves a write elsewhere in the same
// executable region (the common case: a framebuffer/heap store) does not corrupt
// execution — the block at 0x1000 keeps running correctly.
func TestJITWriteOutsideCodeSpanKeepsExecuting(t *testing.T) {
	backend := NewJIT()
	mapCodeAndStack(t, backend)

	if err := backend.WriteMemory(0x1000, []byte{0x07, 0x20, 0x00, 0xbe}); err != nil { // movs r0,#7; bkpt
		t.Fatal(err)
	}
	if r := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8); r.Err != nil {
		t.Fatalf("first run = %+v", r)
	}
	// Store far from the translated code but still in the RWX region.
	if err := backend.WriteMemory(0x1800, []byte{0xff, 0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if r := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8); r.Err != nil {
		t.Fatalf("second run = %+v", r)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 7 {
		t.Fatalf("r0 = %d, want 7", got)
	}
}

func TestJITInstructionCacheInvalidationDropsTranslations(t *testing.T) {
	backend := NewJIT()
	backend.jitBlocks[0x1000] = &jitBlock{start: 0x1000, end: 0x1002}
	backend.jitCodeLo, backend.jitCodeHi = 0x1000, 0x1002
	oldGeneration := backend.jitGen

	if err := backend.writeCP15(7, 5, 0, 0); err != nil {
		t.Fatal(err)
	}
	if len(backend.jitBlocks) != 0 {
		t.Fatalf("translated block count = %d, want 0", len(backend.jitBlocks))
	}
	if backend.jitGen == oldGeneration {
		t.Fatal("translation generation did not advance")
	}
	if backend.jitCodeLo != ^uint32(0) || backend.jitCodeHi != 0 {
		t.Fatalf("translated code span = [%#x,%#x), want empty", backend.jitCodeLo, backend.jitCodeHi)
	}
}

// TestPerPermissionDataCacheBlit exercises the per-permission data cache with a
// software-blitter pattern: read region A, write region B, alternating. Both
// regions must stay cached and the copy must be exact. Runs on the precise and
// JIT backends because they share the memory path (conformance cannot catch a
// bug there, since both would have it).
func TestPerPermissionDataCacheBlit(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() *Backend
	}{
		{"precise", func() *Backend { return New() }},
		{"jit", func() *Backend { return NewJIT() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.make()
			mustMap(t, b, 0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute)
			mustMap(t, b, 0x3000, 0x100, cpu.PermissionRead|cpu.PermissionWrite) // source A
			mustMap(t, b, 0x4000, 0x100, cpu.PermissionRead|cpu.PermissionWrite) // dest B
			// loop: ldrh r2,[r0]; strh r2,[r1]; adds r0,#2; adds r1,#2; subs r3,#1; bne loop; bkpt
			code := []byte{
				0x02, 0x88, 0x0a, 0x80, 0x02, 0x30, 0x02, 0x31,
				0x01, 0x3b, 0xf9, 0xd1, 0x00, 0xbe,
			}
			if err := b.WriteMemory(0x1000, code); err != nil {
				t.Fatal(err)
			}
			const n = 8
			src := make([]byte, n*2)
			for i := 0; i < n; i++ {
				binaryPutUint16(src[i*2:], uint16(0x1100+i*7))
			}
			if err := b.WriteMemory(0x3000, src); err != nil {
				t.Fatal(err)
			}
			for reg, val := range map[uint32]uint32{
				cpu.RegisterR0: 0x3000, cpu.RegisterR1: 0x4000, cpu.RegisterR3: n,
			} {
				if err := b.WriteRegister(reg, val); err != nil {
					t.Fatal(err)
				}
			}
			r := b.Run(context.Background(), 0x1000, cpu.ModeThumb, 200)
			if r.Err != nil || r.Reason != cpu.StopBreakpoint {
				t.Fatalf("run = %+v", r)
			}
			dst := make([]byte, n*2)
			if err := b.ReadMemory(0x4000, dst); err != nil {
				t.Fatal(err)
			}
			for i := range src {
				if dst[i] != src[i] {
					t.Fatalf("blit byte %d = %#x, want %#x (dst=%x)", i, dst[i], src[i], dst)
				}
			}
		})
	}
}

func mustMap(t *testing.T, b *Backend, addr, size uint32, perm cpu.Permissions) {
	t.Helper()
	if err := b.Map(addr, size, perm); err != nil {
		t.Fatal(err)
	}
}

func binaryPutUint16(dst []byte, v uint16) {
	dst[0] = byte(v)
	dst[1] = byte(v >> 8)
}
