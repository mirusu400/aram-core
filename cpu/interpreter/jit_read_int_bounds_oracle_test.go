package interpreter

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// These are literal copies of the previous guards, independent of the new
// endpoint arithmetic. Include even wrapped/invalid cache entries so the
// refactor does not silently change the helpers' uint32 subtraction semantics.
func literalJITWordBeforeIntBounds(entry *dataRegionCache, address uint32) (uint32, bool) {
	offset := uint64(address - entry.address)
	if address&3 != 0 || entry.perms&cpu.PermissionRead == 0 || offset+4 > uint64(len(entry.data)) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(entry.data[int(offset) : int(offset)+4]), true
}

func literalJITRead8BeforeIntBounds(entry *dataRegionCache, address uint32) (byte, bool) {
	offset := uint64(address - entry.address)
	if entry.perms&cpu.PermissionRead == 0 || offset+1 > uint64(len(entry.data)) {
		return 0, false
	}
	return entry.data[int(offset)], true
}

func literalJITRead16BeforeIntBounds(entry *dataRegionCache, address uint32) (uint16, bool) {
	offset := uint64(address - entry.address)
	if entry.perms&cpu.PermissionRead == 0 || offset+2 > uint64(len(entry.data)) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(entry.data[int(offset) : int(offset)+2]), true
}

func TestJITReadIntBoundsLiteralOracle(t *testing.T) {
	for _, base := range []uint32{0, 1, 3, 4, 0x1000, 0x7ffffff0, 0x80000000, 0xfffffff0, 0xffffffff} {
		for _, size := range []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 32, 64} {
			for permission := cpu.Permissions(0); permission < 8; permission++ {
				t.Run(fmt.Sprintf("base-%08x-size-%d-perms-%d", base, size, permission), func(t *testing.T) {
					data := make([]byte, size)
					for i := range data {
						data[i] = byte(i*37 + 11)
					}
					entry := dataRegionCache{address: base, data: data, perms: permission}
					addresses := []uint32{0, 1, 3, 4, 0x7ffffffc, 0x7fffffff, 0x80000000, 0x80000001, 0xfffffffc, 0xffffffff}
					for delta := int64(-8); delta <= int64(size)+8; delta++ {
						addresses = append(addresses, base+uint32(delta))
					}
					rng := rand.New(rand.NewSource(20261017))
					for range 256 {
						addresses = append(addresses, rng.Uint32())
					}
					for _, address := range addresses {
						want8, want8Hit := literalJITRead8BeforeIntBounds(&entry, address)
						got8, got8Hit := cachedJITRead8(&entry, address)
						want16, want16Hit := literalJITRead16BeforeIntBounds(&entry, address)
						got16, got16Hit := cachedJITRead16(&entry, address)
						want32, want32Hit := literalJITWordBeforeIntBounds(&entry, address)
						got32, got32Hit := cachedJITWord(&entry, address)
						if got8 != want8 || got8Hit != want8Hit || got16 != want16 || got16Hit != want16Hit || got32 != want32 || got32Hit != want32Hit {
							t.Fatalf("address=%08x byte=%02x/%t want=%02x/%t half=%04x/%t want=%04x/%t word=%08x/%t want=%08x/%t", address, got8, got8Hit, want8, want8Hit, got16, got16Hit, want16, want16Hit, got32, got32Hit, want32, want32Hit)
						}
					}
				})
			}
		}
	}
}

// Arithmetic-only boundary cases cover enormous valid slice lengths without
// allocating fake or multi-gigabyte backing memory. Actual reads above execute
// the production helpers on both native and js/wasm.
func TestJITReadIntBoundsEndArithmetic32(t *testing.T) {
	for _, width := range []int32{1, 2, 4} {
		for _, length := range []int32{0, 1, 2, 3, 4, 7, 0x7ffffffb, 0x7ffffffc, 0x7ffffffe, 0x7fffffff} {
			deltas := []uint32{0, 1, 2, 3, 4, 7, 0x7ffffffb, 0x7ffffffc, 0x7ffffffe, 0x7fffffff, 0x80000000, 0x80000001, 0xfffffffc, 0xfffffffe, 0xffffffff}
			rng := rand.New(rand.NewSource(41021))
			for range 1024 {
				deltas = append(deltas, rng.Uint32())
			}
			for _, delta := range deltas {
				want := uint64(delta)+uint64(width) <= uint64(length)
				end := int32(delta) + width
				got := end >= width && end <= length
				if width == 1 {
					got = uint32(int32(delta)) < uint32(length)
				}
				if got != want {
					t.Fatalf("width=%d length=%d delta=%08x got=%t want=%t", width, length, delta, got, want)
				}
			}
		}
	}
}

func TestJITReadIntBoundsEndArithmetic64(t *testing.T) {
	for _, width := range []int64{1, 2, 4} {
		for _, length := range []int64{0, 1, 2, 3, 4, 7, 1<<31 - 1, 1 << 31, 1<<32 - 1, 1 << 32, 1<<63 - 1} {
			for _, delta := range []uint32{0, 1, 2, 3, 4, 7, 0x7fffffff, 0x80000000, 0xfffffffc, 0xfffffffe, 0xffffffff} {
				want := uint64(delta)+uint64(width) <= uint64(length)
				end := int64(delta) + width
				got := end >= width && end <= length
				if width == 1 {
					got = uint64(int64(delta)) < uint64(length)
				}
				if got != want {
					t.Fatalf("width=%d length=%d delta=%08x got=%t want=%t", width, length, delta, got, want)
				}
			}
		}
	}
}
