package raptor

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// World Janggi Chess's text-resource parser stores each parsed long through
// module 100#253 (r2 = high word, r3 = low word), then reads it through #91
// (r0 = low word, r1 = high word). These are eight-byte primitive array slots,
// not the four-byte reference stores handled by #250.
func TestRaptorJavaLongArrayImports(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32)}
	array, err := runtime.newRaptorJavaArray('J', 3)
	check(t, err)
	body, err := public.ReadU32(array + 8)
	check(t, err)
	initial := make([]byte, 24)
	for i := range initial {
		initial[i] = 0xa5
	}
	check(t, public.CPU.WriteMemory(body+4, initial))
	const value = uint64(0xfedcba9876543210)
	for i, word := range []uint32{array, 1, uint32(value >> 32), uint32(value & 0xffffffff)} {
		check(t, public.CPU.WriteRegister(uint32(i), word))
	}
	check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: 100, Ordinal: 253}))
	stored := make([]byte, 24)
	check(t, public.CPU.ReadMemory(body+4, stored))
	if got := binary.LittleEndian.Uint64(stored[8:16]); got != value {
		t.Fatalf("stored long = %016x, want %016x", got, value)
	}
	for _, i := range []int{0, 7, 16, 23} {
		if stored[i] != 0xa5 {
			t.Fatalf("neighbor byte %d changed to %02x", i, stored[i])
		}
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)
	mirrorBody, _, _, _, ok := java.Host.ArrayShape(java.lgtToKTF[array])
	if !ok {
		t.Fatal("missing primitive mirror")
	}
	var mirrored [8]byte
	check(t, public.CPU.ReadMemory(mirrorBody+8, mirrored[:]))
	if got := binary.LittleEndian.Uint64(mirrored[:]); got != value {
		t.Fatalf("mirror long = %016x, want %016x", got, value)
	}
	check(t, public.CPU.WriteRegister(cpu.RegisterR0, array))
	check(t, public.CPU.WriteRegister(cpu.RegisterR1, 1))
	check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: 100, Ordinal: 91}))
	low, err := public.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	high, err := public.CPU.ReadRegister(cpu.RegisterR1)
	check(t, err)
	if got := uint64(high)<<32 | uint64(low); got != value {
		t.Fatalf("loaded long = %016x, want %016x", got, value)
	}
	if public.Stats.UnimplementedCalls != 0 {
		t.Fatalf("unimplemented calls = %d", public.Stats.UnimplementedCalls)
	}
}

func TestRaptorJavaLongArrayImportsRejectInvalidAccess(t *testing.T) {
	for _, ordinal := range []uint32{91, 253} {
		for _, index := range []uint32{2, ^uint32(0)} {
			public := newPublicRuntime(t)
			runtime := &Runtime{CPU: public.CPU, Public: public,
				resolvedImports: make(map[raptorImportKey]uint64),
				importSlotByKey: make(map[raptorImportKey]uint32)}
			array, err := runtime.newRaptorJavaArray('J', 2)
			check(t, err)
			body, err := public.ReadU32(array + 8)
			check(t, err)
			check(t, public.WriteU32(body+4, 0x12345678))
			for i, word := range []uint32{array, index, 0xffffffff, 0xffffffff} {
				check(t, public.CPU.WriteRegister(uint32(i), word))
			}
			check(t, runtime.dispatchImport(context.Background(), raptorImportKey{Module: 100, Ordinal: ordinal}))
			if _, thrown := runtime.TakeUndeliveredJavaThrow(); !thrown {
				t.Fatalf("ordinal %d accepted index %d", ordinal, index)
			}
			word, err := public.ReadU32(body + 4)
			check(t, err)
			if word != 0x12345678 {
				t.Fatalf("invalid access changed body to %08x", word)
			}
		}
	}
}
