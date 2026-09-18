package brewrt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"

	"github.com/mirusu400/aram-core/cpu"
	"golang.org/x/image/bmp"
)

func (r *Runtime) createMemAStream() (uint32, error) {
	object, err := r.allocateGuest(4)
	if err != nil {
		return 0, err
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], memAStreamVTable)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		r.releaseGuest(object)
		return 0, fmt.Errorf("write BREW memory stream object: %w", err)
	}
	r.memAStreams[object] = brewMemAStream{refs: 1}
	return object, nil
}

func (r *Runtime) handleMemAStream(slot uint32) error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW memory stream object: %w", err)
	}
	stream, ok := r.memAStreams[object]
	if !ok {
		return fmt.Errorf("BREW memory stream object 0x%08x is not active", object)
	}

	switch slot {
	case 0: // AddRef
		stream.refs++
		r.memAStreams[object] = stream
		return r.cpu.WriteRegister(cpu.RegisterR0, stream.refs)
	case 1: // Release
		refs := r.releaseMemAStream(object)
		return r.cpu.WriteRegister(cpu.RegisterR0, refs)
	case 2: // Readable: memory streams never return AEE_STREAM_WOULDBLOCK.
		return nil
	case 3: // Read
		return r.readMemAStream(object, stream)
	case 4: // Cancel: no Readable callback can be pending for an in-memory stream.
		return nil
	case 5: // Set
		return r.setMemAStream(object, stream, false)
	case 6: // SetEx
		return r.setMemAStream(object, stream, true)
	default:
		return fmt.Errorf("BREW memory stream vtable slot %d is out of range", slot)
	}
}

func (r *Runtime) readMemAStream(object uint32, stream brewMemAStream) error {
	destination, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW memory stream destination: %w", err)
	}
	requested, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW memory stream byte count: %w", err)
	}
	remaining := stream.size - min(stream.offset, stream.size)
	// IAStream explicitly permits partial reads. Bound each transfer so a guest
	// cannot turn a forged stream size into an unbounded host allocation.
	count := min(requested, remaining, uint32(64<<10))
	if count != 0 {
		if stream.buffer > ^uint32(0)-stream.offset {
			return fmt.Errorf("BREW memory stream source address overflows")
		}
		data := make([]byte, count)
		if err := r.cpu.ReadMemory(stream.buffer+stream.offset, data); err != nil {
			return fmt.Errorf("read BREW memory stream source: %w", err)
		}
		if err := r.cpu.WriteMemory(destination, data); err != nil {
			return fmt.Errorf("write BREW memory stream destination: %w", err)
		}
		stream.offset += count
		r.memAStreams[object] = stream
	}
	return r.cpu.WriteRegister(cpu.RegisterR0, count)
}

func (r *Runtime) setMemAStream(object uint32, previous brewMemAStream, extended bool) error {
	buffer, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW memory stream buffer: %w", err)
	}
	size, err := r.cpu.ReadRegister(cpu.RegisterR2)
	if err != nil {
		return fmt.Errorf("read BREW memory stream size: %w", err)
	}
	offset, err := r.cpu.ReadRegister(cpu.RegisterR3)
	if err != nil {
		return fmt.Errorf("read BREW memory stream offset: %w", err)
	}
	stack, err := r.cpu.ReadRegister(cpu.RegisterSP)
	if err != nil {
		return fmt.Errorf("read BREW memory stream stack: %w", err)
	}
	var arguments [8]byte
	argumentBytes := 4
	if extended {
		argumentBytes = 8
	}
	if err := r.cpu.ReadMemory(stack, arguments[:argumentBytes]); err != nil {
		return fmt.Errorf("read BREW memory stream trailing arguments: %w", err)
	}

	r.clearMemAStream(previous)
	next := brewMemAStream{refs: previous.refs, buffer: buffer, size: size, offset: min(offset, size)}
	if extended {
		next.freeCallback = binary.LittleEndian.Uint32(arguments[0:4])
		next.freeContext = binary.LittleEndian.Uint32(arguments[4:8])
	}
	r.memAStreams[object] = next
	return nil
}

func (r *Runtime) releaseMemAStream(object uint32) uint32 {
	stream, ok := r.memAStreams[object]
	if !ok {
		return 0
	}
	if stream.refs > 1 {
		stream.refs--
		r.memAStreams[object] = stream
		return stream.refs
	}
	r.clearMemAStream(stream)
	delete(r.memAStreams, object)
	r.releaseGuest(object)
	return 0
}

func (r *Runtime) createWinBMPImage() (uint32, error) {
	bitmap, err := r.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		return 0, err
	}
	object, err := r.allocateGuest(8)
	if err != nil {
		r.releaseInterfaceObject(bitmap)
		return 0, err
	}
	var encoded [8]byte
	binary.LittleEndian.PutUint32(encoded[0:4], imageVTable)
	binary.LittleEndian.PutUint32(encoded[4:8], bitmap)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		r.releaseInterfaceObject(bitmap)
		r.releaseGuest(object)
		return 0, fmt.Errorf("write BREW WinBMP image object: %w", err)
	}
	return object, nil
}

func (r *Runtime) setImageStream() error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW image object for stream: %w", err)
	}
	streamObject, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW image stream object: %w", err)
	}
	stream, ok := r.memAStreams[streamObject]
	if !ok {
		return fmt.Errorf("BREW image stream 0x%08x is not an active IMemAStream", streamObject)
	}
	previousStream := r.imageStreams[object]
	if previousStream != streamObject {
		if previousStream != 0 {
			r.releaseMemAStream(previousStream)
		}
		stream.refs++
		r.memAStreams[streamObject] = stream
		r.imageStreams[object] = streamObject
	}
	remaining := stream.size - min(stream.offset, stream.size)
	if remaining == 0 || remaining > maxNativeImageBytes || stream.buffer > ^uint32(0)-stream.offset {
		return nil
	}
	encoded := make([]byte, remaining)
	if err := r.cpu.ReadMemory(stream.buffer+stream.offset, encoded); err != nil {
		return fmt.Errorf("read BREW WinBMP stream: %w", err)
	}
	config, err := bmp.DecodeConfig(bytes.NewReader(encoded))
	if err != nil || config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > maxNativeImagePixels {
		return nil
	}
	decoded, err := bmp.Decode(bytes.NewReader(encoded))
	if err != nil {
		return nil
	}
	bitmap, err := r.createNativeBitmap(decoded)
	if err != nil {
		return err
	}
	oldBitmap, err := r.imageBitmap(object)
	if err != nil {
		r.releaseInterfaceObject(bitmap)
		return err
	}
	var pointer [4]byte
	binary.LittleEndian.PutUint32(pointer[:], bitmap)
	if err := r.cpu.WriteMemory(object+4, pointer[:]); err != nil {
		r.releaseInterfaceObject(bitmap)
		return fmt.Errorf("write BREW WinBMP bitmap: %w", err)
	}
	r.releaseInterfaceObject(oldBitmap)
	stream.offset = stream.size
	r.memAStreams[streamObject] = stream
	return nil
}

func (r *Runtime) clearMemAStream(stream brewMemAStream) {
	if stream.buffer == 0 {
		return
	}
	if stream.freeCallback != 0 {
		r.cleanupCallbacks = append(r.cleanupCallbacks, brewCallback{function: stream.freeCallback, context: stream.freeContext})
		return
	}
	// Set transfers ownership to IMemAStream. SYSFREE and FREE share the same
	// bounded guest arena in this runtime, while non-arena pointers remain intact.
	r.releaseGuest(stream.buffer)
}
