package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

// accessAttribution names the instruction responsible for a physical access.
// It is built here rather than in the run loops because a guest retires far
// more instructions than it makes bus accesses: filling the whole struct per
// instruction cost more than the accesses it described.
func (b *Backend) accessAttribution() cpu.MemoryAccessContext {
	return cpu.MemoryAccessContext{
		InstructionAddress: b.instructionAddress,
		LinkAddress:        b.regs[cpu.RegisterLR],
		StackAddress:       b.regs[cpu.RegisterSP],
		Mode:               b.mode,
		Attributed:         true,
	}
}

func (b *Backend) readSystemBus(address uint32, destination []byte, permission cpu.Permissions) error {
	if b.contextBus != nil {
		return b.contextBus.ReadContext(b.accessAttribution(), address, destination, permission)
	}
	return b.systemBus.Read(address, destination, permission)
}

func (b *Backend) writeSystemBus(address uint32, source []byte, permission cpu.Permissions) error {
	if b.contextBus != nil {
		return b.contextBus.WriteContext(b.accessAttribution(), address, source, permission)
	}
	return b.systemBus.Write(address, source, permission)
}

// dataHit returns the cached data-region slice and the offset of a size-byte
// access within it when the cache fully covers the access with the required
// permission. It lets repeated data reads/writes with locality skip the sorted
// findRegion lookup, mirroring the executeData fetch cache.
func (b *Backend) dataHit(address uint32, size int, permission cpu.Permissions) ([]byte, int, cpu.Permissions, bool) {
	slot := int(permission)
	if slot < 0 || slot >= len(b.dataCache) {
		return nil, 0, 0, false
	}
	entry := &b.dataCache[slot]
	if entry.data == nil ||
		entry.perms&permission != permission ||
		address < entry.address {
		return nil, 0, 0, false
	}
	offset := uint64(address - entry.address)
	if offset+uint64(size) > uint64(len(entry.data)) {
		return nil, 0, 0, false
	}
	return entry.data, int(offset), entry.perms, true
}

// cacheData records a region as the most recently accessed for one access
// permission (Read/Write/Execute), so a read region and a write region can both
// stay cached. It stores value copies of the region's address/permissions/slice,
// which stay valid across region re-sorts because regions never overlap and own
// stable backing arrays; the cache is invalidated wherever executeData is.
func (b *Backend) cacheData(mapped *region, access cpu.Permissions) {
	slot := int(access)
	if slot < 0 || slot >= len(b.dataCache) {
		return
	}
	b.dataCache[slot] = dataRegionCache{
		address: mapped.address,
		perms:   mapped.permissions,
		data:    mapped.data,
	}
}

func (b *Backend) read16(address uint32, permission cpu.Permissions) (uint16, error) {
	if b.physicalAccess {
		if b.mmuEnabled() {
			var data [2]byte
			if err := b.readVirtual(address, data[:], permission); err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint16(data[:]), nil
		}
		if b.systemBus != nil {
			var data [2]byte
			if err := b.readSystemBus(address, data[:], permission); err != nil {
				return 0, b.recordExternalAbort(address, permission, err)
			}
			return binary.LittleEndian.Uint16(data[:]), nil
		}
	}
	if permission == cpu.PermissionExecute {
		return b.fetch16(address)
	}
	if data, offset, perms, ok := b.dataHit(address, 2, permission); ok {
		if b.tlb != nil {
			b.tlbNote(address, address-uint32(offset), data, perms)
		}
		return binary.LittleEndian.Uint16(data[offset : offset+2]), nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset >= 2 {
		b.cacheData(mapped, permission)
		if b.tlb != nil {
			b.tlbNote(address, mapped.address, mapped.data, mapped.permissions)
		}
		return binary.LittleEndian.Uint16(mapped.data[offset : offset+2]), nil
	}
	var data [2]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data[:]), nil
}

func (b *Backend) read32(address uint32, permission cpu.Permissions) (uint32, error) {
	if b.physicalAccess {
		if b.mmuEnabled() {
			var data [4]byte
			if err := b.readVirtual(address, data[:], permission); err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint32(data[:]), nil
		}
		if b.systemBus != nil {
			var data [4]byte
			if err := b.readSystemBus(address, data[:], permission); err != nil {
				return 0, b.recordExternalAbort(address, permission, err)
			}
			return binary.LittleEndian.Uint32(data[:]), nil
		}
	}
	if permission == cpu.PermissionExecute {
		return b.fetch32(address)
	}
	if data, offset, perms, ok := b.dataHit(address, 4, permission); ok {
		if b.tlb != nil {
			b.tlbNote(address, address-uint32(offset), data, perms)
		}
		return binary.LittleEndian.Uint32(data[offset : offset+4]), nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset >= 4 {
		b.cacheData(mapped, permission)
		if b.tlb != nil {
			b.tlbNote(address, mapped.address, mapped.data, mapped.permissions)
		}
		return binary.LittleEndian.Uint32(mapped.data[offset : offset+4]), nil
	}
	var data [4]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (b *Backend) fetch16(address uint32) (uint16, error) {
	if b.physicalAccess {
		if b.instructionCacheEnabled() {
			data, err := b.fetchInstructionCache(address, 2)
			if err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint16(data), nil
		}
		if b.mmuEnabled() {
			var data [2]byte
			if err := b.readVirtual(address, data[:], cpu.PermissionExecute); err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint16(data[:]), nil
		}
		if b.systemBus != nil {
			var data [2]byte
			if err := b.readSystemBus(address, data[:], cpu.PermissionExecute); err != nil {
				return 0, b.recordExternalAbort(address, cpu.PermissionExecute, err)
			}
			return binary.LittleEndian.Uint16(data[:]), nil
		}
	}
	if address >= b.executeAddress {
		offset := uint64(address - b.executeAddress)
		if offset+2 <= uint64(len(b.executeData)) {
			index := int(offset)
			return uint16(b.executeData[index]) |
				uint16(b.executeData[index+1])<<8, nil
		}
	}
	mapped, offset, err := b.findRegion(address, cpu.PermissionExecute)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset < 2 {
		var data [2]byte
		if err := b.copyOut(address, data[:], cpu.PermissionExecute); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint16(data[:]), nil
	}
	b.executeAddress = mapped.address
	b.executeData = mapped.data
	return uint16(mapped.data[offset]) |
		uint16(mapped.data[offset+1])<<8, nil
}

func (b *Backend) fetch32(address uint32) (uint32, error) {
	if b.physicalAccess {
		if b.instructionCacheEnabled() {
			data, err := b.fetchInstructionCache(address, 4)
			if err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint32(data), nil
		}
		if b.mmuEnabled() {
			var data [4]byte
			if err := b.readVirtual(address, data[:], cpu.PermissionExecute); err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint32(data[:]), nil
		}
		if b.systemBus != nil {
			var data [4]byte
			if err := b.readSystemBus(address, data[:], cpu.PermissionExecute); err != nil {
				return 0, b.recordExternalAbort(address, cpu.PermissionExecute, err)
			}
			return binary.LittleEndian.Uint32(data[:]), nil
		}
	}
	if address >= b.executeAddress {
		offset := uint64(address - b.executeAddress)
		if offset+4 <= uint64(len(b.executeData)) {
			index := int(offset)
			return uint32(b.executeData[index]) |
				uint32(b.executeData[index+1])<<8 |
				uint32(b.executeData[index+2])<<16 |
				uint32(b.executeData[index+3])<<24, nil
		}
	}
	mapped, offset, err := b.findRegion(address, cpu.PermissionExecute)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset < 4 {
		var data [4]byte
		if err := b.copyOut(address, data[:], cpu.PermissionExecute); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(data[:]), nil
	}
	b.executeAddress = mapped.address
	b.executeData = mapped.data
	return uint32(mapped.data[offset]) |
		uint32(mapped.data[offset+1])<<8 |
		uint32(mapped.data[offset+2])<<16 |
		uint32(mapped.data[offset+3])<<24, nil
}

func (b *Backend) write16(address uint32, value uint16, permission cpu.Permissions) error {
	if b.physicalAccess {
		if b.mmuEnabled() {
			var data [2]byte
			binary.LittleEndian.PutUint16(data[:], value)
			return b.writeVirtual(address, data[:], permission)
		}
		if b.systemBus != nil {
			var data [2]byte
			binary.LittleEndian.PutUint16(data[:], value)
			err := b.writeSystemBus(address, data[:], permission)
			return b.recordExternalAbort(address, permission, err)
		}
	}
	if data, offset, perms, ok := b.dataHit(address, 2, permission); ok {
		binary.LittleEndian.PutUint16(data[offset:offset+2], value)
		if b.jitBlocks != nil || b.nativeBlocks != nil {
			b.smcInvalidate(address, 2, perms)
		}
		if b.tlb != nil {
			b.tlbNote(address, address-uint32(offset), data, perms)
		}
		return nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	if len(mapped.data)-offset >= 2 {
		b.cacheData(mapped, permission)
		binary.LittleEndian.PutUint16(mapped.data[offset:offset+2], value)
		if b.jitBlocks != nil || b.nativeBlocks != nil {
			b.smcInvalidate(address, 2, mapped.permissions)
		}
		if b.tlb != nil {
			b.tlbNote(address, mapped.address, mapped.data, mapped.permissions)
		}
		return nil
	}
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) write32(address, value uint32, permission cpu.Permissions) error {
	if b.physicalAccess {
		if b.mmuEnabled() {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], value)
			return b.writeVirtual(address, data[:], permission)
		}
		if b.systemBus != nil {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], value)
			err := b.writeSystemBus(address, data[:], permission)
			return b.recordExternalAbort(address, permission, err)
		}
	}
	if data, offset, perms, ok := b.dataHit(address, 4, permission); ok {
		binary.LittleEndian.PutUint32(data[offset:offset+4], value)
		if b.jitBlocks != nil || b.nativeBlocks != nil {
			b.smcInvalidate(address, 4, perms)
		}
		if b.tlb != nil {
			b.tlbNote(address, address-uint32(offset), data, perms)
		}
		return nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	if len(mapped.data)-offset >= 4 {
		b.cacheData(mapped, permission)
		binary.LittleEndian.PutUint32(mapped.data[offset:offset+4], value)
		if b.jitBlocks != nil || b.nativeBlocks != nil {
			b.smcInvalidate(address, 4, mapped.permissions)
		}
		if b.tlb != nil {
			b.tlbNote(address, mapped.address, mapped.data, mapped.permissions)
		}
		return nil
	}
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) read8(address uint32, permission cpu.Permissions) (byte, error) {
	if b.physicalAccess {
		if b.mmuEnabled() {
			var data [1]byte
			if err := b.readVirtual(address, data[:], permission); err != nil {
				return 0, err
			}
			return data[0], nil
		}
		if b.systemBus != nil {
			var data [1]byte
			if err := b.readSystemBus(address, data[:], permission); err != nil {
				return 0, b.recordExternalAbort(address, permission, err)
			}
			return data[0], nil
		}
	}
	if data, offset, perms, ok := b.dataHit(address, 1, permission); ok {
		if b.tlb != nil {
			b.tlbNote(address, address-uint32(offset), data, perms)
		}
		return data[offset], nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	b.cacheData(mapped, permission)
	if b.tlb != nil {
		b.tlbNote(address, mapped.address, mapped.data, mapped.permissions)
	}
	return mapped.data[offset], nil
}

func (b *Backend) write8(address uint32, value byte, permission cpu.Permissions) error {
	if b.physicalAccess {
		if b.mmuEnabled() {
			return b.writeVirtual(address, []byte{value}, permission)
		}
		if b.systemBus != nil {
			err := b.writeSystemBus(address, []byte{value}, permission)
			return b.recordExternalAbort(address, permission, err)
		}
	}
	if data, offset, perms, ok := b.dataHit(address, 1, permission); ok {
		data[offset] = value
		if b.jitBlocks != nil || b.nativeBlocks != nil {
			b.smcInvalidate(address, 1, perms)
		}
		if b.tlb != nil {
			b.tlbNote(address, address-uint32(offset), data, perms)
		}
		return nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	b.cacheData(mapped, permission)
	mapped.data[offset] = value
	if b.jitBlocks != nil || b.nativeBlocks != nil {
		b.smcInvalidate(address, 1, mapped.permissions)
	}
	if b.tlb != nil {
		b.tlbNote(address, mapped.address, mapped.data, mapped.permissions)
	}
	return nil
}

func (b *Backend) copyOut(address uint32, destination []byte, permission cpu.Permissions) error {
	if len(destination) == 0 {
		return nil
	}
	if b.systemBus != nil {
		if b.blockBus != nil {
			done, err := b.blockBus.ReadBlock(address, destination, permission)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
		current := address
		remaining := destination
		for len(remaining) > 0 {
			count := physicalTransferWidth(current, len(remaining))
			if err := b.readSystemBus(current, remaining[:count], permission); err != nil {
				return err
			}
			current += uint32(count)
			remaining = remaining[count:]
		}
		return nil
	}
	current := address
	remaining := destination
	for len(remaining) > 0 {
		mapped, offset, err := b.findRegion(current, permission)
		if err != nil {
			return err
		}
		count := min(len(remaining), len(mapped.data)-offset)
		copy(remaining[:count], mapped.data[offset:offset+count])
		remaining = remaining[count:]
		if len(remaining) == 0 {
			break
		}
		if uint64(current)+uint64(count) > uint64(^uint32(0)) {
			return cpu.ErrInvalidAddress
		}
		current += uint32(count)
	}
	return nil
}

func (b *Backend) copyIn(address uint32, source []byte, permission cpu.Permissions) error {
	if len(source) == 0 {
		return nil
	}
	if b.systemBus != nil {
		if b.blockBus != nil {
			done, err := b.blockBus.WriteBlock(address, source, permission)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
		current := address
		remaining := source
		for len(remaining) > 0 {
			count := physicalTransferWidth(current, len(remaining))
			if err := b.writeSystemBus(current, remaining[:count], permission); err != nil {
				return err
			}
			current += uint32(count)
			remaining = remaining[count:]
		}
		return nil
	}
	current := address
	remaining := source
	for len(remaining) > 0 {
		mapped, offset, err := b.findRegion(current, permission)
		if err != nil {
			return err
		}
		count := min(len(remaining), len(mapped.data)-offset)
		copy(mapped.data[offset:offset+count], remaining[:count])
		if b.jitBlocks != nil || b.nativeBlocks != nil {
			b.smcInvalidate(current, uint32(count), mapped.permissions)
		}
		remaining = remaining[count:]
		if len(remaining) == 0 {
			break
		}
		if uint64(current)+uint64(count) > uint64(^uint32(0)) {
			return cpu.ErrInvalidAddress
		}
		current += uint32(count)
	}
	return nil
}

func physicalTransferWidth(address uint32, remaining int) int {
	if address&3 == 0 && remaining >= 4 {
		return 4
	}
	if address&1 == 0 && remaining >= 2 {
		return 2
	}
	return 1
}
