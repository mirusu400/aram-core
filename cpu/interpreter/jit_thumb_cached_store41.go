package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

// Cached-store helpers have no Backend or observer access.
// Callers must establish foldPrivateScalarBounds && privateDataRoute first and
// preserve the scalar writer's exact post-write SMC guard. Map-owned regions
// satisfy uint64(address)+uint64(len(data)) <= 1<<32; cap(data) is not a bound.
// The compile-time guard disables every call on non-WASM targets, rather than
// changing the existing native scalar writers or their unusual size contracts.
func cachedStore8(entry *dataRegionCache, address uint32, value byte) (cpu.Permissions, bool) {
	offset := uint64(address - entry.address)
	if entry.perms&cpu.PermissionWrite == 0 || offset+1 > uint64(len(entry.data)) {
		return 0, false
	}
	entry.data[int(offset)] = value
	return entry.perms, true
}

func cachedStore16(entry *dataRegionCache, address uint32, value uint16) (cpu.Permissions, bool) {
	offset := uint64(address - entry.address)
	if entry.perms&cpu.PermissionWrite == 0 || offset+2 > uint64(len(entry.data)) {
		return 0, false
	}
	binary.LittleEndian.PutUint16(entry.data[int(offset):int(offset)+2], value)
	return entry.perms, true
}

func cachedStore32(entry *dataRegionCache, address, value uint32) (cpu.Permissions, bool) {
	offset := uint64(address - entry.address)
	// An unaligned word MUST use write32, including its CP15.A-dependent address.
	if address&3 != 0 || entry.perms&cpu.PermissionWrite == 0 || offset+4 > uint64(len(entry.data)) {
		return 0, false
	}
	binary.LittleEndian.PutUint32(entry.data[offset:], value)
	return entry.perms, true
}

// cachedStore41SMC keeps the scalar writer's exact post-write invalidation guard.
// It has no locks, statistics, flags, or extra state.
func (b *Backend) cachedStore41SMC(address, size uint32, perms cpu.Permissions) {
	if b.jitBlocks != nil || b.nativeBlocks != nil {
		b.smcInvalidate(address, size, perms)
	}
}
