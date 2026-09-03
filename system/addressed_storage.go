package system

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var ErrAddressedStorageWindowMMIO = errors.New("unsupported addressed storage-window access")

// AddressedReadOnlyStorageWindow exposes a bounded read aperture into larger
// storage. Its paired command register selects the source byte range; command
// flag bits are excluded by the profile-supplied address mask.
type AddressedReadOnlyStorageWindow struct {
	storage      ReadOnlyStorage
	size         uint32
	addressMask  uint32
	resetCommand uint32
	command      uint32
}

func NewAddressedReadOnlyStorageWindow(
	storage ReadOnlyStorage,
	size uint32,
	addressMask uint32,
	resetCommand uint32,
) (*AddressedReadOnlyStorageWindow, error) {
	if storage == nil || storage.Size() <= 0 || size == 0 ||
		uint64(resetCommand&addressMask)+uint64(size) > uint64(storage.Size()) {
		return nil, fmt.Errorf("create addressed storage window: %w", ErrInvalidRegion)
	}
	window := &AddressedReadOnlyStorageWindow{
		storage: storage, size: size, addressMask: addressMask, resetCommand: resetCommand,
	}
	_ = window.Reset()
	return window, nil
}

func (d *AddressedReadOnlyStorageWindow) Reset() error {
	d.command = d.resetCommand
	return nil
}

func (d *AddressedReadOnlyStorageWindow) validAccess(offset uint32, width Width) bool {
	return (width == Width8 || width == Width16 || width == Width32) &&
		offset%uint32(width) == 0 && uint64(offset)+uint64(width) <= uint64(d.size)
}

func (d *AddressedReadOnlyStorageWindow) Read(offset uint32, width Width) (uint32, error) {
	if !d.validAccess(offset, width) {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrAddressedStorageWindowMMIO, width*8, offset)
	}
	var data [4]byte
	storageOffset := uint64(d.command&d.addressMask) + uint64(offset)
	read, err := d.storage.ReadAt(data[:width], int64(storageOffset))
	if err != nil && !errors.Is(err, io.EOF) || read != int(width) {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, fmt.Errorf("%w: storage read at 0x%x: %v", ErrAddressedStorageWindowMMIO, storageOffset, err)
	}
	switch width {
	case Width8:
		return uint32(data[0]), nil
	case Width16:
		return uint32(binary.LittleEndian.Uint16(data[:2])), nil
	default:
		return binary.LittleEndian.Uint32(data[:]), nil
	}
}

func (d *AddressedReadOnlyStorageWindow) Write(offset uint32, width Width, value uint32) error {
	return fmt.Errorf(
		"%w: write%d value 0x%x at 0x%x",
		ErrAddressedStorageWindowMMIO,
		width*8,
		value,
		offset,
	)
}

func (d *AddressedReadOnlyStorageWindow) setCommand(value uint32) error {
	storageOffset := uint64(value & d.addressMask)
	if storageOffset+uint64(d.size) > uint64(d.storage.Size()) {
		return fmt.Errorf("%w: command address 0x%x", ErrAddressedStorageWindowMMIO, storageOffset)
	}
	d.command = value
	return nil
}

func (d *AddressedReadOnlyStorageWindow) SaveState() ([]byte, error) {
	state := make([]byte, 24)
	copy(state, "ARSW")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	binary.LittleEndian.PutUint32(state[8:12], d.size)
	binary.LittleEndian.PutUint32(state[12:16], d.addressMask)
	binary.LittleEndian.PutUint32(state[16:20], d.resetCommand)
	binary.LittleEndian.PutUint32(state[20:24], d.command)
	return state, nil
}

func (d *AddressedReadOnlyStorageWindow) LoadState(state []byte) error {
	if len(state) != 24 || string(state[:4]) != "ARSW" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 ||
		binary.LittleEndian.Uint32(state[8:12]) != d.size ||
		binary.LittleEndian.Uint32(state[12:16]) != d.addressMask ||
		binary.LittleEndian.Uint32(state[16:20]) != d.resetCommand {
		return ErrInvalidState
	}
	if err := d.setCommand(binary.LittleEndian.Uint32(state[20:24])); err != nil {
		return ErrInvalidState
	}
	return nil
}

// AddressedStorageCommandRegister is the exact-width selector paired with an
// AddressedReadOnlyStorageWindow.
type AddressedStorageCommandRegister struct {
	window *AddressedReadOnlyStorageWindow
	width  Width
}

func NewAddressedStorageCommandRegister(
	window *AddressedReadOnlyStorageWindow,
	width Width,
) (*AddressedStorageCommandRegister, error) {
	if window == nil || (width != Width8 && width != Width16 && width != Width32) ||
		width < Width32 && (window.addressMask >= uint32(1)<<(uint32(width)*8) ||
			window.resetCommand >= uint32(1)<<(uint32(width)*8)) {
		return nil, fmt.Errorf("create addressed storage command register: %w", ErrInvalidRegion)
	}
	return &AddressedStorageCommandRegister{window: window, width: width}, nil
}

func (d *AddressedStorageCommandRegister) Reset() error {
	return d.window.Reset()
}

func (d *AddressedStorageCommandRegister) Read(offset uint32, width Width) (uint32, error) {
	if offset != 0 || width != d.width {
		return 0, fmt.Errorf("%w: command read%d at 0x%x", ErrAddressedStorageWindowMMIO, width*8, offset)
	}
	return d.window.command, nil
}

func (d *AddressedStorageCommandRegister) Write(offset uint32, width Width, value uint32) error {
	if offset != 0 || width != d.width ||
		d.width < Width32 && value >= uint32(1)<<(uint32(d.width)*8) {
		return fmt.Errorf(
			"%w: command write%d value 0x%x at 0x%x",
			ErrAddressedStorageWindowMMIO,
			width*8,
			value,
			offset,
		)
	}
	return d.window.setCommand(value)
}

func (d *AddressedStorageCommandRegister) SaveState() ([]byte, error) {
	return d.window.SaveState()
}

func (d *AddressedStorageCommandRegister) LoadState(state []byte) error {
	return d.window.LoadState(state)
}

var (
	_ Device         = (*AddressedReadOnlyStorageWindow)(nil)
	_ StatefulDevice = (*AddressedReadOnlyStorageWindow)(nil)
	_ Device         = (*AddressedStorageCommandRegister)(nil)
	_ StatefulDevice = (*AddressedStorageCommandRegister)(nil)
)
