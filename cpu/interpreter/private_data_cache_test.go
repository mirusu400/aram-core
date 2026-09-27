package interpreter

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestPrivateDataCacheRetainsAlternatingRegions(t *testing.T) {
	b := New()
	defer b.Close()
	addresses := []uint32{0x1000, 0x3000, 0x5000}
	for _, address := range addresses {
		check(t, b.Map(address, 16, cpu.PermissionRead|cpu.PermissionWrite))
	}
	for i, address := range addresses {
		check(t, b.write32(address, uint32(i+1), cpu.PermissionWrite))
		got, err := b.read32(address, cpu.PermissionRead)
		check(t, err)
		if got != uint32(i+1) {
			t.Fatalf("value at %#x = %d", address, got)
		}
	}
	for _, address := range addresses {
		for _, permission := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionWrite} {
			if _, _, _, ok := b.privateDataHit(address, 4, permission); !ok {
				t.Fatalf("alternating access evicted %#x permission %d", address, permission)
			}
		}
	}
}

func TestPrivateDataCacheCollisionBoundsAndPermissions(t *testing.T) {
	b := New()
	defer b.Close()
	const first = uint32(0x1000)
	second := first + 0x1000
	for privateDataCacheIndex(second, cpu.PermissionRead) != privateDataCacheIndex(first, cpu.PermissionRead) {
		second += 0x1000
	}
	check(t, b.Map(first, 8, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.Map(second, 8, cpu.PermissionRead))
	check(t, b.write32(first, 0x12345678, cpu.PermissionWrite))
	for i := 0; i < 100; i++ {
		value, err := b.read32(first, cpu.PermissionRead)
		check(t, err)
		if value != 0x12345678 {
			t.Fatalf("colliding read = %#x", value)
		}
		value, err = b.read32(second, cpu.PermissionRead)
		check(t, err)
		if value != 0 {
			t.Fatalf("wrong cached region: %#x", value)
		}
	}
	if err := b.write32(second, 1, cpu.PermissionWrite); !errors.Is(err, cpu.ErrPermissionDenied) {
		t.Fatalf("read-only write = %v", err)
	}
	for _, address := range []uint32{second - 1, second + 5, second + 8, second + 0x1000} {
		if _, _, _, ok := b.privateDataHit(address, 4, cpu.PermissionRead); ok {
			t.Fatalf("out-of-region cache hit at %#x", address)
		}
	}
	check(t, b.Map(0, 4, cpu.PermissionRead|cpu.PermissionWrite))
	if _, _, _, ok := b.privateDataHit(second, 4, cpu.PermissionRead); ok {
		t.Fatal("mapping change retained a cache entry")
	}
	_, err := b.read32(second, cpu.PermissionRead)
	check(t, err)
	saved, err := b.SaveContext()
	check(t, err)
	check(t, b.RestoreContext(saved))
	if _, _, _, ok := b.privateDataHit(second, 4, cpu.PermissionRead); ok {
		t.Fatal("serialized restore retained a cache entry")
	}
}

func TestPrivateDataCacheMatchesColdScalarAccess(t *testing.T) {
	fast, cold := New(), New()
	defer fast.Close()
	defer cold.Close()
	for _, b := range []*Backend{fast, cold} {
		for _, mapping := range []struct {
			address, size uint32
			permissions   cpu.Permissions
		}{
			{0x1000, 5, cpu.PermissionRead | cpu.PermissionWrite},
			{0x1005, 7, cpu.PermissionRead | cpu.PermissionWrite},
			{0x2000, 32, cpu.PermissionRead},
			{0x41000, 32, cpu.PermissionRead | cpu.PermissionWrite},
			{0x50000, 32, cpu.PermissionWrite},
			{0xfffffff0, 16, cpu.PermissionRead | cpu.PermissionWrite},
		} {
			check(t, b.Map(mapping.address, mapping.size, mapping.permissions))
		}
	}
	rng := rand.New(rand.NewSource(346))
	bases := []uint32{0x1000, 0x1005, 0x2000, 0x41000, 0x50000, 0xfffffff0}
	for operation := 0; operation < 10000; operation++ {
		address := bases[rng.Intn(len(bases))] + uint32(rng.Intn(40)) - 4
		width := []int{1, 2, 4}[rng.Intn(3)]
		write := rng.Intn(2) == 0
		value := rng.Uint32()
		cold.clearDataCaches()
		access := func(b *Backend) (uint32, error) {
			if write {
				switch width {
				case 1:
					return 0, b.write8(address, byte(value), cpu.PermissionWrite)
				case 2:
					return 0, b.write16(address, uint16(value), cpu.PermissionWrite)
				default:
					return 0, b.write32(address, value, cpu.PermissionWrite)
				}
			}
			switch width {
			case 1:
				v, e := b.read8(address, cpu.PermissionRead)
				return uint32(v), e
			case 2:
				v, e := b.read16(address, cpu.PermissionRead)
				return uint32(v), e
			default:
				return b.read32(address, cpu.PermissionRead)
			}
		}
		got, gotErr := access(fast)
		want, wantErr := access(cold)
		if got != want || (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr.Error() != wantErr.Error()) {
			t.Fatalf("operation %d write=%v width=%d address=%#x: cached (%#x,%v) cold (%#x,%v)", operation, write, width, address, got, gotErr, want, wantErr)
		}
		for i := range fast.regions {
			if !bytes.Equal(fast.regions[i].data, cold.regions[i].data) {
				t.Fatalf("operation %d changed partial-write semantics", operation)
			}
		}
	}
}
