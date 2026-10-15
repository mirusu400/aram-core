package interpreter

import "github.com/mirusu400/aram-core/cpu"

// armPrivateBlockTransferSpan proves an ordinary LDM/STM infallible before its
// first register or memory update. A cold cache, split mapping, observer or
// possible code write retains the scalar instruction's ordered partial faults.
// The caller has already applied blockTransferAddress; writeback must continue
// to use the original, possibly unaligned base rather than this access address.
func (b *Backend) armPrivateBlockTransferSpan(address uint32, count int, load bool) ([]byte, int, bool) {
	if b.systemBus != nil || b.physicalAccess || b.tlb != nil || b.tracing() || len(b.executionTraps) != 0 || address&3 != 0 || count <= 0 || count > 16 {
		return nil, 0, false
	}
	size := count * 4
	if uint64(address)+uint64(size) > 1<<32 {
		return nil, 0, false
	}
	permission := cpu.PermissionWrite
	if load {
		permission = cpu.PermissionRead
	}
	data, offset, _, hit := b.privateDataHit(address, size, permission)
	if !hit {
		return nil, 0, false
	}
	if !load && (b.hasJITCodePages(address, uint32(size)) ||
		(b.nativeBlocks != nil || b.nativeARMBlocks != nil) && b.hasCodePages(address, uint32(size))) {
		return nil, 0, false
	}
	return data, offset, true
}
