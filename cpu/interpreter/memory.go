package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

// dataHit returns the cached data-region slice and the offset of a size-byte
// access within it when the cache fully covers the access with the required
// permission. It lets repeated data reads/writes with locality skip the sorted
// findRegion lookup, mirroring the executeData fetch cache.
func (b *Backend) dataHit(address uint32, size int, permission cpu.Permissions) ([]byte, int, bool) {
	if b.dataData == nil ||
		b.dataPermissions&permission != permission ||
		address < b.dataAddress {
		return nil, 0, false
	}
	offset := uint64(address - b.dataAddress)
	if offset+uint64(size) > uint64(len(b.dataData)) {
		return nil, 0, false
	}
	return b.dataData, int(offset), true
}

// cacheData records a region as the most recently accessed data region. It
// stores value copies of the region's address/permissions/slice, which stay
// valid across region re-sorts because regions never overlap and own stable
// backing arrays; the cache is invalidated wherever executeData is.
func (b *Backend) cacheData(mapped *region) {
	b.dataAddress = mapped.address
	b.dataPermissions = mapped.permissions
	b.dataData = mapped.data
}

func (b *Backend) read16(address uint32, permission cpu.Permissions) (uint16, error) {
	if b.mmuEnabled() {
		var data [2]byte
		if err := b.readVirtual(address, data[:], permission); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint16(data[:]), nil
	}
	if b.systemBus != nil {
		var data [2]byte
		if err := b.systemBus.Read(address, data[:], permission); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint16(data[:]), nil
	}
	if permission == cpu.PermissionExecute {
		return b.fetch16(address)
	}
	if data, offset, ok := b.dataHit(address, 2, permission); ok {
		return binary.LittleEndian.Uint16(data[offset : offset+2]), nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset >= 2 {
		b.cacheData(mapped)
		return binary.LittleEndian.Uint16(mapped.data[offset : offset+2]), nil
	}
	var data [2]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data[:]), nil
}

func (b *Backend) read32(address uint32, permission cpu.Permissions) (uint32, error) {
	if b.mmuEnabled() {
		var data [4]byte
		if err := b.readVirtual(address, data[:], permission); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(data[:]), nil
	}
	if b.systemBus != nil {
		var data [4]byte
		if err := b.systemBus.Read(address, data[:], permission); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(data[:]), nil
	}
	if permission == cpu.PermissionExecute {
		return b.fetch32(address)
	}
	if data, offset, ok := b.dataHit(address, 4, permission); ok {
		return binary.LittleEndian.Uint32(data[offset : offset+4]), nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset >= 4 {
		b.cacheData(mapped)
		return binary.LittleEndian.Uint32(mapped.data[offset : offset+4]), nil
	}
	var data [4]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (b *Backend) fetch16(address uint32) (uint16, error) {
	if b.mmuEnabled() {
		var data [2]byte
		if err := b.readVirtual(address, data[:], cpu.PermissionExecute); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint16(data[:]), nil
	}
	if b.systemBus != nil {
		var data [2]byte
		if err := b.systemBus.Read(address, data[:], cpu.PermissionExecute); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint16(data[:]), nil
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
	if b.mmuEnabled() {
		var data [4]byte
		if err := b.readVirtual(address, data[:], cpu.PermissionExecute); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(data[:]), nil
	}
	if b.systemBus != nil {
		var data [4]byte
		if err := b.systemBus.Read(address, data[:], cpu.PermissionExecute); err != nil {
			return 0, err
		}
		return binary.LittleEndian.Uint32(data[:]), nil
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
	if b.mmuEnabled() {
		var data [2]byte
		binary.LittleEndian.PutUint16(data[:], value)
		return b.writeVirtual(address, data[:], permission)
	}
	if b.systemBus != nil {
		var data [2]byte
		binary.LittleEndian.PutUint16(data[:], value)
		return b.systemBus.Write(address, data[:], permission)
	}
	if data, offset, ok := b.dataHit(address, 2, permission); ok {
		binary.LittleEndian.PutUint16(data[offset:offset+2], value)
		return nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	if len(mapped.data)-offset >= 2 {
		b.cacheData(mapped)
		binary.LittleEndian.PutUint16(mapped.data[offset:offset+2], value)
		return nil
	}
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) write32(address, value uint32, permission cpu.Permissions) error {
	if b.mmuEnabled() {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], value)
		return b.writeVirtual(address, data[:], permission)
	}
	if b.systemBus != nil {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], value)
		return b.systemBus.Write(address, data[:], permission)
	}
	if data, offset, ok := b.dataHit(address, 4, permission); ok {
		binary.LittleEndian.PutUint32(data[offset:offset+4], value)
		return nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	if len(mapped.data)-offset >= 4 {
		b.cacheData(mapped)
		binary.LittleEndian.PutUint32(mapped.data[offset:offset+4], value)
		return nil
	}
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) read8(address uint32, permission cpu.Permissions) (byte, error) {
	if b.mmuEnabled() {
		var data [1]byte
		if err := b.readVirtual(address, data[:], permission); err != nil {
			return 0, err
		}
		return data[0], nil
	}
	if b.systemBus != nil {
		var data [1]byte
		if err := b.systemBus.Read(address, data[:], permission); err != nil {
			return 0, err
		}
		return data[0], nil
	}
	if data, offset, ok := b.dataHit(address, 1, permission); ok {
		return data[offset], nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	b.cacheData(mapped)
	return mapped.data[offset], nil
}

func (b *Backend) write8(address uint32, value byte, permission cpu.Permissions) error {
	if b.mmuEnabled() {
		return b.writeVirtual(address, []byte{value}, permission)
	}
	if b.systemBus != nil {
		return b.systemBus.Write(address, []byte{value}, permission)
	}
	if data, offset, ok := b.dataHit(address, 1, permission); ok {
		data[offset] = value
		return nil
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	b.cacheData(mapped)
	mapped.data[offset] = value
	return nil
}

func (b *Backend) copyOut(address uint32, destination []byte, permission cpu.Permissions) error {
	if len(destination) == 0 {
		return nil
	}
	if b.systemBus != nil {
		current := address
		remaining := destination
		for len(remaining) > 0 {
			count := physicalTransferWidth(current, len(remaining))
			if err := b.systemBus.Read(current, remaining[:count], permission); err != nil {
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
		current := address
		remaining := source
		for len(remaining) > 0 {
			count := physicalTransferWidth(current, len(remaining))
			if err := b.systemBus.Write(current, remaining[:count], permission); err != nil {
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
