package brewrt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"path"
	"strings"
	"unicode/utf16"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/bmp"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

const brewResourceMagic = uint16(0x0011)

const brewStringResourceKind = uint16(1)

const (
	brewImageResourceKind = uint16(6)
	brewViewerHandler     = uint32(0)
	brewSoundHandler      = uint32(1)
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

func (r *Runtime) loadShellResourceDataEx() error {
	pathPointer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW extended resource path pointer: %w", err)
	}
	resourceID, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW extended resource ID: %w", err)
	}
	resourceKind, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW extended resource type: %w", err)
	}
	sp, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW extended resource stack: %w", err)
	}
	var stack [8]byte
	if err := r.cpu.ReadMemory(sp, stack[:]); err != nil {
		return fmt.Errorf("read BREW extended resource arguments: %w", err)
	}
	buffer := binary.LittleEndian.Uint32(stack[0:4])
	lengthPointer := binary.LittleEndian.Uint32(stack[4:8])
	container, err := r.resourceContainer(pathPointer)
	if err != nil {
		return err
	}
	data, ok := resourceData(container, uint16(resourceKind), uint16(resourceID))
	if !ok {
		data = nil
	}
	capacity := uint32(len(data))
	if lengthPointer != 0 {
		var encoded [4]byte
		if err := r.cpu.ReadMemory(lengthPointer, encoded[:]); err == nil {
			capacity = binary.LittleEndian.Uint32(encoded[:])
		}
		binary.LittleEndian.PutUint32(encoded[:], uint32(len(data)))
		if err := r.cpu.WriteMemory(lengthPointer, encoded[:]); err != nil {
			return fmt.Errorf("write BREW extended resource length: %w", err)
		}
	}
	if len(data) == 0 || buffer == ^uint32(0) {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	if buffer == 0 {
		buffer, err = r.allocateGuest(uint32(len(data)))
		if err != nil {
			return err
		}
		capacity = uint32(len(data))
	}
	count := min(uint32(len(data)), capacity)
	if count != 0 {
		if err := r.cpu.WriteMemory(buffer, data[:count]); err != nil {
			return fmt.Errorf("write BREW extended resource data: %w", err)
		}
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, buffer)
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
	units := decodeBREWResourceString(data)
	capacity := int(size/2) - 1
	if len(units) > capacity {
		units = units[:capacity]
	}
	encoded := make([]byte, (len(units)+1)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(encoded[index*2:], unit)
	}
	if err := r.cpu.WriteMemory(destination, encoded); err != nil {
		return fmt.Errorf("write BREW string resource: %w", err)
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, uint32(len(units)))
}

func decodeBREWResourceString(data []byte) []uint16 {
	if len(data) >= 2 && data[0] == 0xfe && data[1] == 0xfe {
		payload := data[2:]
		if terminator := bytes.IndexByte(payload, 0); terminator >= 0 {
			payload = payload[:terminator]
		}
		decoded, _, err := transform.Bytes(korean.EUCKR.NewDecoder(), payload)
		if err == nil {
			return trimWideStringTerminator(utf16.Encode([]rune(string(decoded))))
		}
		// BREW's STREXPAND fallback widens ASCII bytes and preserves each
		// double-byte handset character as one AECHAR.
		var units []uint16
		for index := 0; index < len(payload); index++ {
			unit := uint16(payload[index])
			if payload[index]&0x80 != 0 && index+1 < len(payload) {
				index++
				unit |= uint16(payload[index]) << 8
			}
			units = append(units, unit)
		}
		return trimWideStringTerminator(units)
	}
	units := make([]uint16, 0, len(data)/2)
	for index := 0; index+1 < len(data); index += 2 {
		unit := binary.LittleEndian.Uint16(data[index:])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return units
}

func trimWideStringTerminator(units []uint16) []uint16 {
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return units
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
	if classID != brewViewerHandler && classID != BitmapClassID {
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
	decoded, ok := decodeBREWResourceImage(data)
	if !ok {
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	bitmap, err := r.createNativeBitmap(decoded)
	if err != nil {
		return err
	}
	if classID == BitmapClassID {
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
	if err == nil {
		return decoded, true
	}
	// KTF titles commonly package handset-native SAF animations as image/sis.
	// A full SAF renderer is not portable yet, but the native BREW shell still
	// returns a valid IImage for these resources. Preserve that object contract
	// with a transparent handset-sized surface instead of returning NULL and
	// letting otherwise compatible applets immediately dereference it.
	if bytes.Equal(data[2:offset], []byte("image/sis\x00")) &&
		bytes.HasPrefix(data[offset:], []byte("SAF\x00")) {
		return image.NewRGBA(image.Rect(0, 0, int(framebufferWidth), int(framebufferHeight))), true
	}
	return nil, false
}

func (r *Runtime) allocateGuest(size uint32) (uint32, error) {
	size = (size + 7) &^ 7
	if size == 0 {
		return 0, fmt.Errorf("BREW allocation %d exceeds heap", size)
	}
	var address uint32
	for index, block := range r.heapFree {
		if block.size < size {
			continue
		}
		address = block.address
		if block.size == size {
			r.heapFree = append(r.heapFree[:index], r.heapFree[index+1:]...)
		} else {
			r.heapFree[index].address += size
			r.heapFree[index].size -= size
		}
		break
	}
	if address == 0 {
		if size > heapBase+heapSize-r.heapNext {
			return 0, fmt.Errorf("BREW allocation %d exceeds heap", size)
		}
		address = r.heapNext
		r.heapNext += size
	}
	r.heapAllocated[address] = size
	// BREW's MALLOC zero-fills by default. Legacy titles rely on that contract
	// when a recently freed block is reused for state arrays and scene objects.
	if err := r.cpu.WriteMemory(address, make([]byte, size)); err != nil {
		r.releaseGuest(address)
		return 0, fmt.Errorf("zero BREW allocation: %w", err)
	}
	return address, nil
}

func (r *Runtime) releaseGuest(address uint32) {
	if address == 0 {
		return
	}
	size, ok := r.heapAllocated[address]
	if !ok {
		return
	}
	delete(r.heapAllocated, address)
	index := 0
	for index < len(r.heapFree) && r.heapFree[index].address < address {
		index++
	}
	r.heapFree = append(r.heapFree, brewHeapBlock{})
	copy(r.heapFree[index+1:], r.heapFree[index:])
	r.heapFree[index] = brewHeapBlock{address: address, size: size}
	merged := r.heapFree[:0]
	for _, block := range r.heapFree {
		if len(merged) != 0 && merged[len(merged)-1].address+merged[len(merged)-1].size == block.address {
			merged[len(merged)-1].size += block.size
			continue
		}
		merged = append(merged, block)
	}
	r.heapFree = merged
	for len(r.heapFree) != 0 {
		last := r.heapFree[len(r.heapFree)-1]
		if last.address+last.size != r.heapNext {
			break
		}
		r.heapNext = last.address
		r.heapFree = r.heapFree[:len(r.heapFree)-1]
	}
}

func (r *Runtime) releaseInterfaceObject(address uint32) {
	if _, allocated := r.heapAllocated[address]; !allocated {
		return
	}
	var encoded [12]byte
	if err := r.cpu.ReadMemory(address, encoded[:]); err == nil {
		switch binary.LittleEndian.Uint32(encoded[0:4]) {
		case imageVTable:
			r.releaseInterfaceObject(binary.LittleEndian.Uint32(encoded[4:8]))
			if stream := r.imageStreams[address]; stream != 0 {
				r.releaseMemAStream(stream)
				delete(r.imageStreams, address)
			}
		case bitmapVTable:
			delete(r.nativeImages, address)
			r.releaseGuest(binary.LittleEndian.Uint32(encoded[8:12]))
		case fileVTable:
			delete(r.fileHandles, address)
		}
	}
	r.releaseGuest(address)
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
