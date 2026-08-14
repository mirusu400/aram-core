package interpreter

import (
	"encoding/binary"

	"github.com/mirusu400/aram-core/cpu"
)

func (b *Backend) read16(address uint32, permission cpu.Permissions) (uint16, error) {
	if permission == cpu.PermissionExecute {
		return b.fetch16(address)
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset >= 2 {
		return binary.LittleEndian.Uint16(mapped.data[offset : offset+2]), nil
	}
	var data [2]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data[:]), nil
}

func (b *Backend) read32(address uint32, permission cpu.Permissions) (uint32, error) {
	if permission == cpu.PermissionExecute {
		return b.fetch32(address)
	}
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	if len(mapped.data)-offset >= 4 {
		return binary.LittleEndian.Uint32(mapped.data[offset : offset+4]), nil
	}
	var data [4]byte
	if err := b.copyOut(address, data[:], permission); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

func (b *Backend) fetch16(address uint32) (uint16, error) {
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
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	if len(mapped.data)-offset >= 2 {
		binary.LittleEndian.PutUint16(mapped.data[offset:offset+2], value)
		return nil
	}
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) write32(address, value uint32, permission cpu.Permissions) error {
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	if len(mapped.data)-offset >= 4 {
		binary.LittleEndian.PutUint32(mapped.data[offset:offset+4], value)
		return nil
	}
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return b.copyIn(address, data[:], permission)
}

func (b *Backend) read8(address uint32, permission cpu.Permissions) (byte, error) {
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return 0, err
	}
	return mapped.data[offset], nil
}

func (b *Backend) write8(address uint32, value byte, permission cpu.Permissions) error {
	mapped, offset, err := b.findRegion(address, permission)
	if err != nil {
		return err
	}
	mapped.data[offset] = value
	return nil
}

func (b *Backend) copyOut(address uint32, destination []byte, permission cpu.Permissions) error {
	if len(destination) == 0 {
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
