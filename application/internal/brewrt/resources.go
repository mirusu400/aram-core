package brewrt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"path"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/bmp"
)

const brewResourceMagic = uint16(0x0011)

const brewStringResourceKind = uint16(1)

const (
	brewImageResourceKind = uint16(6)
	brewViewerHandler     = uint32(0)
	brewSoundHandler      = uint32(1)
	brewBitmapClassID     = uint32(0x01001021)
)

func resourceData(data []byte, kind, id uint16) ([]byte, bool) {
	if len(data) < 0x20 || binary.LittleEndian.Uint16(data) != brewResourceMagic {
		return nil, false
	}
	indexCount := uint64(binary.LittleEndian.Uint16(data[0x06:]))
	indexOffset := uint64(binary.LittleEndian.Uint32(data[0x08:]))
	tableOffset := uint64(binary.LittleEndian.Uint32(data[0x10:]))
	sectionCount := uint64(binary.LittleEndian.Uint32(data[0x14:]))
	if indexCount > uint64(len(data))/8 || indexOffset+indexCount*8 > uint64(len(data)) ||
		sectionCount > uint64(len(data))/4 || tableOffset+(sectionCount+1)*4 > uint64(len(data)) {
		return nil, false
	}
	section := uint64(0)
	found := false
	for index := uint64(0); index < indexCount; index++ {
		at := indexOffset + index*8
		rangeKind := binary.LittleEndian.Uint16(data[at:])
		firstID := binary.LittleEndian.Uint16(data[at+2:])
		count := uint64(binary.LittleEndian.Uint16(data[at+4:])) + 1
		firstSection := uint64(binary.LittleEndian.Uint16(data[at+6:]))
		if rangeKind != kind || id < firstID {
			continue
		}
		offset := uint64(id - firstID)
		if offset < count {
			section = firstSection + offset
			found = true
			break
		}
	}
	if !found || section >= sectionCount {
		return nil, false
	}
	startAt := tableOffset + section*4
	endAt := startAt + 4
	start := uint64(binary.LittleEndian.Uint32(data[startAt:]))
	end := uint64(binary.LittleEndian.Uint32(data[endAt:]))
	if start > end || end > uint64(len(data)) {
		return nil, false
	}
	return data[start:end], true
}

func (r *Runtime) loadShellResourceData() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW resource path pointer: %w", err)
	}
	resourceID, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW resource ID: %w", err)
	}
	resourceKind, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW resource type: %w", err)
	}
	var container []byte
	if pathPointer != 0 {
		name, err := r.readCString(pathPointer)
		if err != nil {
			return err
		}
		container, _, _ = r.lookupGuestFile(normalizeGuestPath(name))
	} else {
		var match []byte
		for name, data := range r.files {
			if !strings.EqualFold(path.Ext(name), ".bar") {
				continue
			}
			if match != nil {
				match = nil
				break
			}
			match = data
		}
		container = match
	}
	data, ok := resourceData(container, uint16(resourceKind), uint16(resourceID))
	if !ok || len(data) == 0 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	address, err := r.allocateGuest(uint32(len(data)))
	if err != nil {
		return err
	}
	if err := r.cpu.WriteMemory(address, data); err != nil {
		return fmt.Errorf("write BREW resource data: %w", err)
	}
	if err := r.cpu.WriteRegister(cpu.RegisterR0, address); err != nil {
		return fmt.Errorf("return BREW resource data: %w", err)
	}
	return nil
}

func (r *Runtime) resourceContainer(pathPointer uint32) ([]byte, error) {
	if pathPointer != 0 {
		name, err := r.readCString(pathPointer)
		if err != nil {
			return nil, err
		}
		container, _, _ := r.lookupGuestFile(normalizeGuestPath(name))
		return container, nil
	}
	var match []byte
	for name, data := range r.files {
		if !strings.EqualFold(path.Ext(name), ".bar") {
			continue
		}
		if match != nil {
			return nil, nil
		}
		match = data
	}
	return match, nil
}

func (r *Runtime) loadShellResourceString() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW string resource path: %w", err)
	}
	resourceID, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW string resource ID: %w", err)
	}
	destination, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW string resource destination: %w", err)
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW string resource stack: %w", err)
	}
	var sizeBytes [4]byte
	if err := r.cpu.ReadMemory(sp, sizeBytes[:]); err != nil {
		return fmt.Errorf("read BREW string resource buffer size: %w", err)
	}
	size := binary.LittleEndian.Uint32(sizeBytes[:])
	if destination == 0 || size < 2 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	container, err := r.resourceContainer(pathPointer)
	if err != nil {
		return err
	}
	data, ok := resourceData(container, brewStringResourceKind, uint16(resourceID))
	if !ok {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	// String resources are stored as UCS-2 bytes. Copy whole code units, reserve
	// space for the terminating NUL, and return the number of characters copied.
	count := min(uint32(len(data))&^1, size-2)
	for count >= 2 && data[count-2] == 0 && data[count-1] == 0 {
		count -= 2
	}
	encoded := make([]byte, count+2)
	copy(encoded, data[:count])
	if err := r.cpu.WriteMemory(destination, encoded); err != nil {
		return fmt.Errorf("write BREW string resource: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, count/2)
}

func (r *Runtime) loadShellResourceObject() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW resource-object path: %w", err)
	}
	resourceID, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW resource-object ID: %w", err)
	}
	classID, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW resource-object class: %w", err)
	}
	container, err := r.resourceContainer(pathPointer)
	if err != nil {
		return err
	}
	if classID == brewSoundHandler {
		// The silent player remains a correctly typed object. Resource ownership is
		// retained by the immutable package for the runtime lifetime.
		if data, ok := resourceData(container, brewImageResourceKind, uint16(resourceID)); ok && len(data) != 0 {
			return r.cpu.WriteRegister(cpu.RegisterR0, soundPlayerObject)
		}
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if classID != brewViewerHandler && classID != brewBitmapClassID {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	data, ok := resourceData(container, brewImageResourceKind, uint16(resourceID))
	if !ok || len(data) < 4 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	offset := int(binary.LittleEndian.Uint16(data[:2]))
	if offset < 2 || offset >= len(data) {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	decoded, err := bmp.Decode(bytes.NewReader(data[offset:]))
	if err != nil {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	bitmap, err := r.createNativeBitmap(decoded)
	if err != nil {
		return err
	}
	if classID == brewBitmapClassID {
		return r.cpu.WriteRegister(cpu.RegisterR0, bitmap)
	}
	object, err := r.allocateGuest(8)
	if err != nil {
		return err
	}
	var encoded [8]byte
	binary.LittleEndian.PutUint32(encoded[0:], imageVTable)
	binary.LittleEndian.PutUint32(encoded[4:], bitmap)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		return fmt.Errorf("write BREW image object: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, object)
}

func decodeBREWResourceImage(data []byte) (image.Image, bool) {
	if len(data) < 4 {
		return nil, false
	}
	offset := int(binary.LittleEndian.Uint16(data[:2]))
	if offset < 2 || offset >= len(data) {
		return nil, false
	}
	decoded, err := bmp.Decode(bytes.NewReader(data[offset:]))
	return decoded, err == nil
}

func (r *Runtime) allocateGuest(size uint32) (uint32, error) {
	size = (size + 7) &^ 7
	if size == 0 || size > heapBase+heapSize-r.heapNext {
		return 0, fmt.Errorf("BREW allocation %d exceeds heap", size)
	}
	address := r.heapNext
	r.heapNext += size
	return address, nil
}

func (r *Runtime) preferenceArguments() (brewPreferenceKey, uint32, uint32, error) {
	classID, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return brewPreferenceKey{}, 0, 0, err
	}
	version, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return brewPreferenceKey{}, 0, 0, err
	}
	buffer, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return brewPreferenceKey{}, 0, 0, err
	}
	stack, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return brewPreferenceKey{}, 0, 0, err
	}
	var encoded [4]byte
	if err := r.cpu.ReadMemory(stack, encoded[:]); err != nil {
		return brewPreferenceKey{}, 0, 0, err
	}
	size := binary.LittleEndian.Uint32(encoded[:])
	return brewPreferenceKey{classID: classID, version: uint16(version)}, buffer, size, nil
}

func (r *Runtime) getPreferences() error {
	key, buffer, size, err := r.preferenceArguments()
	if err != nil {
		return fmt.Errorf("read BREW preference arguments: %w", err)
	}
	data, ok := r.preferences[key]
	if !ok {
		return r.cpu.WriteRegister(cpu.RegisterR0, 1) // EFAILED
	}
	if buffer == 0 || size < uint32(len(data)) {
		return r.cpu.WriteRegister(cpu.RegisterR0, uint32(len(data)))
	}
	if err := r.cpu.WriteMemory(buffer, data); err != nil {
		return fmt.Errorf("write BREW preferences: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}

func (r *Runtime) setPreferences() error {
	key, buffer, size, err := r.preferenceArguments()
	if err != nil {
		return fmt.Errorf("read BREW preference arguments: %w", err)
	}
	if buffer == 0 || size == 0 || size > 64<<10 {
		return r.cpu.WriteRegister(cpu.RegisterR0, 2) // EBADPARM
	}
	data := make([]byte, size)
	if err := r.cpu.ReadMemory(buffer, data); err != nil {
		return fmt.Errorf("read BREW preferences: %w", err)
	}
	r.preferences[key] = data
	return r.cpu.WriteRegister(cpu.RegisterR0, 0)
}
