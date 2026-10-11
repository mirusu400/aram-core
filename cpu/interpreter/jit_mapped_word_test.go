package interpreter

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestJITMappedWordMatchesMappingOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(41011))
	for _, mapping := range []struct{ address, size uint32 }{
		{0, 16}, {1, 16}, {0x1000, 32}, {0xfffffff0, 16}, {0xfffffff7, 9}, {0xffffffff, 1},
	} {
		for _, permission := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionRead | cpu.PermissionWrite, cpu.PermissionWrite, cpu.PermissionExecute} {
			backend := New()
			check(t, backend.Map(mapping.address, mapping.size, permission))
			mapped := &backend.regions[0]
			for i := range mapped.data {
				mapped.data[i] = byte(3*i + 11)
			}
			addresses := []uint32{0, 1, 0x7fffffff, 0x80000000, 0xfffffffc, 0xffffffff}
			for delta := int64(-8); delta <= int64(mapping.size)+8; delta++ {
				addresses = append(addresses, uint32(int64(mapping.address)+delta))
			}
			for range 256 {
				addresses = append(addresses, rng.Uint32())
			}
			for _, address := range addresses {
				// Force colliding cache tags; full mapping bounds and permissions,
				// rather than the tag alone, must decide whether a word can be read.
				backend.cacheData(mapped, address, cpu.PermissionRead)
				got, hit := cachedJITWord(&backend.privateDataCache[privateDataCacheIndex(address, cpu.PermissionRead)], address)
				wantHit := address&3 == 0 && permission&cpu.PermissionRead != 0 &&
					uint64(address) >= uint64(mapping.address) && uint64(address)+4 <= uint64(mapping.address)+uint64(mapping.size)
				if hit != wantHit {
					t.Fatalf("mapping %#x/%d permission %d address %#x hit=%v want=%v", mapping.address, mapping.size, permission, address, hit, wantHit)
				}
				if hit {
					offset := int(address - mapping.address)
					want := binary.LittleEndian.Uint32(mapped.data[offset : offset+4])
					if got != want {
						t.Fatalf("word %#x = %#x want %#x", address, got, want)
					}
				}
			}
			check(t, backend.Close())
		}
	}
}

func TestJITMappedWordRetainsScalarFallbackAndInvalidation(t *testing.T) {
	b := New()
	defer b.Close()
	check(t, b.Map(0x1000, 8, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.WriteMemory(0x1000, []byte{1, 2, 3, 4, 5, 6, 7, 8}))
	if _, hit := cachedJITWord(&b.privateDataCache[privateDataCacheIndex(0x1000, cpu.PermissionRead)], 0x1000); hit {
		t.Fatal("cold cache hit")
	}
	value, err := b.read32(0x1000, cpu.PermissionRead)
	check(t, err)
	if got, hit := cachedJITWord(&b.privateDataCache[privateDataCacheIndex(0x1000, cpu.PermissionRead)], 0x1000); !hit || got != value {
		t.Fatal("warm aligned load missed")
	}
	if _, hit := cachedJITWord(&b.privateDataCache[privateDataCacheIndex(0x1001, cpu.PermissionRead)], 0x1001); hit {
		t.Fatal("unaligned load bypasses ARM rotation")
	}
	check(t, b.Map(0x2000, 4, cpu.PermissionRead))
	if _, hit := cachedJITWord(&b.privateDataCache[privateDataCacheIndex(0x1000, cpu.PermissionRead)], 0x1000); hit {
		t.Fatal("mapping change retained a hit")
	}
	_, err = b.read32(0x1000, cpu.PermissionRead)
	check(t, err)
	state, err := b.SaveContext()
	check(t, err)
	check(t, b.RestoreContext(state))
	if _, hit := cachedJITWord(&b.privateDataCache[privateDataCacheIndex(0x1000, cpu.PermissionRead)], 0x1000); hit {
		t.Fatal("context restore retained a hit")
	}
}

func TestJITWordLoadUsesPhysicalBusAndNativeTLB(t *testing.T) {
	b := NewJIT()
	defer b.Close()
	check(t, b.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.WriteMemory(0x2000, []byte{1, 2, 3, 4}))
	_, err := b.read32(0x2000, cpu.PermissionRead)
	check(t, err)
	b.regs[cpu.RegisterR1] = 0x2000
	block := &jitBlock{thumb: []thumbMicroInstr{{pc: 0x1000, raw: 0x6808, op: thumbImmediateTransfer}}}
	bus := &testSystemBus{memory: map[uint32]byte{0x2000: 9, 0x2001: 8, 0x2002: 7, 0x2003: 6}}
	b.systemBus, b.physicalAccess = bus, true
	retired, _, _, err := b.executeThumbMicroBlock(block, 1, true, false, false)
	check(t, err)
	if retired != 1 || b.regs[cpu.RegisterR0] != 0x06070809 || bus.dataReads != 1 {
		t.Fatal("word load used private cache instead of the physical bus")
	}
	b.systemBus, b.physicalAccess = nil, false
	b.tlb = make([]tlbEntry, nativeTLBEntries*2)
	b.tlbClear()
	retired, _, _, err = b.executeThumbMicroBlock(block, 1, false, false, false)
	check(t, err)
	if retired != 1 || b.regs[cpu.RegisterR0] != 0x04030201 || !b.tlbHit(0x2000, cpu.PermissionRead) {
		t.Fatal("warm cached load skipped native TLB bookkeeping")
	}
}
