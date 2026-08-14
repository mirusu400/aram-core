package ktf

import (
	"context"
	"encoding/binary"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"unicode/utf16"

	shared "github.com/mirusu400/aram-core/runtime"
)

func (r *Runtime) handleInputStreamMethod(
	ctx context.Context,
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	streamInstance := instance
	for depth := 0; depth < 64; depth++ {
		redirected := r.inputTargets[streamInstance]
		if redirected == 0 || redirected == streamInstance {
			break
		}
		streamInstance = redirected
	}
	stream := r.inputStreams[streamInstance]
	readBytes := func(count uint32) ([]byte, bool, error) {
		if delegated, valueErr := r.shouldDelegateInputRead(streamInstance); valueErr != nil {
			return nil, false, valueErr
		} else if delegated {
			data := make([]byte, count)
			for index := range data {
				value, valueErr := r.invokeJavaVirtual(
					ctx,
					streamInstance,
					"read",
					"()I",
				)
				if valueErr != nil {
					return nil, false, valueErr
				}
				if value == ^uint32(0) {
					return nil, false, nil
				}
				data[index] = byte(value)
			}
			return data, true, nil
		}
		if stream == nil ||
			stream.position > uint32(len(stream.data)) ||
			count > uint32(len(stream.data))-stream.position {
			return nil, false, nil
		}
		data := stream.data[stream.position : stream.position+count]
		stream.position += count
		return data, true, nil
	}
	switch name + descriptor {
	case "<init>()V":
		if stream == nil {
			r.inputStreams[instance] = &ktfInputStream{}
		}
		return 0, nil
	case "<init>([B)V", "<init>([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		var data []byte
		if array == 0 {
			data = nil
		} else if descriptor == "([BII)V" {
			offset, valueErr := r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			count, valueErr := r.parameter(4)
			if valueErr != nil {
				return 0, valueErr
			}
			data, valueErr = r.readJavaByteArrayRange(array, offset, count)
			if valueErr != nil {
				return 0, valueErr
			}
		} else {
			data, valueErr = r.readJavaByteArray(array)
			if valueErr != nil {
				return 0, valueErr
			}
		}
		r.inputStreams[instance] = &ktfInputStream{data: data}
		return 0, nil
	case "<init>(Ljava/io/InputStream;)V":
		source, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.inputTargets[instance] = source
		return 0, nil
	case "available()I":
		if stream == nil || stream.position >= uint32(len(stream.data)) {
			return 0, nil
		}
		return uint32(len(stream.data)) - stream.position, nil
	case "read()I":
		if stream == nil || stream.position >= uint32(len(stream.data)) {
			return ^uint32(0), nil
		}
		value := stream.data[stream.position]
		stream.position++
		return uint32(value), nil
	case "read([B)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return ^uint32(0), nil
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamInto(stream, array, 0, length)
	case "read([BII)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if array == 0 {
			return ^uint32(0), nil
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamInto(stream, array, offset, count)
	case "close()V":
		delete(r.inputStreams, streamInstance)
		delete(r.inputTargets, instance)
		return 0, nil
	case "skip(J)J":
		if stream == nil {
			return 0, nil
		}
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		requested := uint64(high)<<32 | uint64(low)
		remaining := uint64(len(stream.data)) - uint64(stream.position)
		if requested > remaining {
			requested = remaining
		}
		stream.position += uint32(requested)
		return r.javaLongResult(requested), nil
	case "mark(I)V":
		// Streams are fully buffered in host memory, so the read-ahead limit
		// carries no obligation and every mark stays valid.
		if stream != nil {
			stream.mark = stream.position
		}
		return 0, nil
	case "markSupported()Z":
		return 1, nil
	case "reset()V":
		// reset returns to the last mark, not to the start. Rewinding to zero
		// silently desynchronised every subsequent read for titles that scan
		// a resource with mark/reset, which then decoded record lengths from
		// the wrong offset.
		if stream != nil {
			stream.position = stream.mark
		}
		return 0, nil
	case "readBoolean()Z", "readUnsignedByte()I":
		data, ok, valueErr := readBytes(1)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		return uint32(data[0]), nil
	case "readByte()B":
		data, ok, valueErr := readBytes(1)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		return uint32(int32(int8(data[0]))), nil
	case "readShort()S", "readUnsignedShort()I", "readChar()C":
		data, ok, valueErr := readBytes(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		value := binary.BigEndian.Uint16(data)
		if name == "readShort" {
			return uint32(int32(int16(value))), nil
		}
		return uint32(value), nil
	case "readInt()I", "readLong()J":
		count := uint32(4)
		if name == "readLong" {
			count = 8
		}
		data, ok, valueErr := readBytes(count)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		if count == 8 {
			return r.javaLongResult(binary.BigEndian.Uint64(data)), nil
		}
		return binary.BigEndian.Uint32(data), nil
	case "readUTF()Ljava/lang/String;":
		header, ok, valueErr := readBytes(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		data, ok, valueErr := readBytes(
			uint32(binary.BigEndian.Uint16(header)),
		)
		if valueErr != nil {
			return 0, valueErr
		}
		if !ok {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		value, valueErr := decodeKTFModifiedUTF8(data)
		if valueErr != nil {
			return r.raiseJavaException("java/io/UTFDataFormatException", 0)
		}
		return r.NewJavaString(value)
	case "readFully([B)V", "readFully([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset := uint32(0)
		count, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		if descriptor == "([BII)V" {
			offset, valueErr = r.parameter(3)
			if valueErr != nil {
				return 0, valueErr
			}
			count, valueErr = r.parameter(4)
			if valueErr != nil {
				return 0, valueErr
			}
		}
		if count == 0 {
			return 0, nil
		}
		read, valueErr := r.readInputStreamInto(stream, array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		if read != count {
			return r.raiseJavaException("java/io/EOFException", 0)
		}
		return 0, nil
	case "skipBytes(I)I":
		requested, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if stream == nil {
			return 0, nil
		}
		remaining := uint32(len(stream.data)) - stream.position
		if requested > remaining {
			requested = remaining
		}
		stream.position += requested
		return requested, nil
	default:
		return 0, nil
	}
}

func decodeKTFModifiedUTF8(data []byte) (string, error) {
	units := make([]uint16, 0, len(data))
	for offset := 0; offset < len(data); {
		first := data[offset]
		switch {
		case first&0x80 == 0:
			units = append(units, uint16(first))
			offset++
		case first&0xe0 == 0xc0:
			if offset+1 >= len(data) || data[offset+1]&0xc0 != 0x80 {
				return "", fmt.Errorf(
					"malformed modified UTF-8 at byte %d",
					offset,
				)
			}
			units = append(
				units,
				uint16(first&0x1f)<<6|uint16(data[offset+1]&0x3f),
			)
			offset += 2
		case first&0xf0 == 0xe0:
			if offset+2 >= len(data) ||
				data[offset+1]&0xc0 != 0x80 ||
				data[offset+2]&0xc0 != 0x80 {
				return "", fmt.Errorf(
					"malformed modified UTF-8 at byte %d",
					offset,
				)
			}
			units = append(
				units,
				uint16(first&0x0f)<<12|
					uint16(data[offset+1]&0x3f)<<6|
					uint16(data[offset+2]&0x3f),
			)
			offset += 3
		default:
			return "", fmt.Errorf(
				"malformed modified UTF-8 at byte %d",
				offset,
			)
		}
	}
	return string(utf16.Decode(units)), nil
}

func (r *Runtime) shouldDelegateInputRead(instance uint32) (bool, error) {
	if instance == 0 {
		return false, nil
	}
	words, err := r.ReadWords(instance, 2)
	if err != nil {
		return false, err
	}
	methodAddress, err := r.resolveJavaMethod(words[1], "read", "()I")
	if err != nil {
		return false, nil
	}
	method, err := r.InspectJavaMethod(methodAddress)
	if err != nil {
		return false, err
	}
	if method.Body == 0 {
		return false, nil
	}
	_, isHostMethod := r.hostCalls[method.Body&^1]
	return !isHostMethod, nil
}

func (r *Runtime) readInputStreamInto(
	stream *ktfInputStream,
	array, offset, count uint32,
) (uint32, error) {
	if stream == nil || stream.position >= uint32(len(stream.data)) {
		return ^uint32(0), nil
	}
	length, err := r.javaArrayLength(array)
	if err != nil {
		return 0, err
	}
	if offset > length || count > length-offset {
		return 0, fmt.Errorf(
			"KTF Java byte array range [%d,%d) exceeds length %d",
			offset,
			offset+count,
			length,
		)
	}
	remaining := uint32(len(stream.data)) - stream.position
	if count > remaining {
		count = remaining
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return 0, err
	}
	if err := r.CPU.WriteMemory(
		fields+8+offset,
		stream.data[stream.position:stream.position+count],
	); err != nil {
		return 0, err
	}
	stream.position += count
	return count, nil
}

func (r *Runtime) handleInputStreamReaderMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>(Ljava/io/InputStream;)V":
		source, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.inputTargets[instance] = source
		return 0, nil
	case "read()I":
		source := r.inputReaderSource(instance)
		stream := r.inputStreams[source]
		if stream == nil || stream.position >= uint32(len(stream.data)) {
			return ^uint32(0), nil
		}
		characters, next, valueErr := r.decodeInputStreamReaderChars(stream, 1)
		if valueErr != nil {
			return 0, valueErr
		}
		if len(characters) == 0 {
			return ^uint32(0), nil
		}
		stream.position = next
		return uint32(characters[0]), nil
	case "read([C)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		length, valueErr := r.javaArrayLength(array)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamReaderChars(instance, array, 0, length)
	case "read([CII)I":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		return r.readInputStreamReaderChars(instance, array, offset, count)
	case "ready()Z":
		stream := r.inputStreams[r.inputReaderSource(instance)]
		return boolWord(
			stream != nil && stream.position < uint32(len(stream.data)),
		), nil
	case "skip(J)J":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		requested := int64(uint64(high)<<32 | uint64(low))
		if requested <= 0 {
			return r.javaLongResult(0), nil
		}
		stream := r.inputStreams[r.inputReaderSource(instance)]
		if stream == nil {
			return r.javaLongResult(0), nil
		}
		remaining := uint64(len(stream.data)) - uint64(stream.position)
		count := uint64(requested)
		if count > remaining {
			count = remaining
		}
		characters, next, valueErr := r.decodeInputStreamReaderChars(
			stream,
			uint32(count),
		)
		if valueErr != nil {
			return 0, valueErr
		}
		stream.position = next
		return r.javaLongResult(uint64(len(characters))), nil
	case "close()V":
		delete(r.inputTargets, instance)
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) inputReaderSource(instance uint32) uint32 {
	source := r.inputTargets[instance]
	for depth := 0; source != 0 && depth < 64; depth++ {
		redirected := r.inputTargets[source]
		if redirected == 0 || redirected == source {
			break
		}
		source = redirected
	}
	return source
}

func (r *Runtime) readInputStreamReaderChars(
	instance, array, offset, count uint32,
) (uint32, error) {
	length, err := r.javaArrayLength(array)
	if err != nil {
		return 0, err
	}
	if offset > length || count > length-offset {
		return 0, fmt.Errorf(
			"KTF Java char array range [%d,%d) exceeds length %d",
			offset,
			offset+count,
			length,
		)
	}
	if count == 0 {
		return 0, nil
	}
	stream := r.inputStreams[r.inputReaderSource(instance)]
	if stream == nil || stream.position >= uint32(len(stream.data)) {
		return ^uint32(0), nil
	}
	characters, next, err := r.decodeInputStreamReaderChars(stream, count)
	if err != nil {
		return 0, err
	}
	fields, err := r.ReadU32(array)
	if err != nil {
		return 0, err
	}
	encoded := make([]byte, len(characters)*2)
	for index, character := range characters {
		binary.LittleEndian.PutUint16(
			encoded[index*2:],
			character,
		)
	}
	if err := r.CPU.WriteMemory(fields+8+offset*2, encoded); err != nil {
		return 0, err
	}
	stream.position = next
	return uint32(len(characters)), nil
}

func (r *Runtime) decodeInputStreamReaderChars(
	stream *ktfInputStream,
	count uint32,
) ([]uint16, uint32, error) {
	if stream == nil {
		return nil, 0, nil
	}
	if count == 0 || stream.position >= uint32(len(stream.data)) {
		return nil, stream.position, nil
	}
	remaining := uint32(len(stream.data)) - stream.position
	characters := make([]uint16, 0, min(count, remaining))
	position := stream.position
	for uint32(len(characters)) < count && position < uint32(len(stream.data)) {
		encodedSize := uint32(1)
		if stream.data[position]&0x80 != 0 {
			encodedSize = 2
		}
		if encodedSize > uint32(len(stream.data))-position {
			return nil, stream.position, fmt.Errorf("KTF Java InputStreamReader has truncated EUC-KR input")
		}
		value, err := r.Services.Text.Decode(
			stream.data[position:position+encodedSize],
			shared.EncodingEUCKR,
		)
		if err != nil {
			return nil, stream.position, err
		}
		decoded := []rune(value)
		if len(decoded) != 1 || decoded[0] > math.MaxUint16 {
			return nil, stream.position, fmt.Errorf("KTF Java InputStreamReader decoded an invalid character")
		}
		characters = append(characters, uint16(decoded[0]))
		position += encodedSize
	}
	return characters, position, nil
}

func (r *Runtime) handleByteArrayOutputStreamMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	switch name + descriptor {
	case "<init>()V", "<init>(I)V":
		r.outputStreams[instance] = nil
		return 0, nil
	case "write(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		r.outputStreams[instance] = append(
			r.outputStreams[instance],
			byte(value),
		)
		return 0, nil
	case "write([B)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		r.outputStreams[instance] = append(r.outputStreams[instance], data...)
		return 0, nil
	case "write([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		r.outputStreams[instance] = append(r.outputStreams[instance], data...)
		return 0, nil
	case "toByteArray()[B":
		return r.newJavaByteArray(r.outputStreams[instance])
	case "size()I":
		return uint32(len(r.outputStreams[instance])), nil
	case "close()V":
		return 0, nil
	default:
		return 0, nil
	}
}

func (r *Runtime) handleOutputStreamMethod(
	name, descriptor string,
) (uint32, error) {
	instance, err := r.parameter(1)
	if err != nil {
		return 0, err
	}
	target := instance
	if redirected := r.outputTargets[instance]; redirected != 0 {
		target = redirected
	}
	appendBytes := func(data []byte) error {
		r.outputStreams[target] = append(r.outputStreams[target], data...)
		if fileInstance := r.fileStreamTargets[target]; fileInstance != 0 {
			_, err := r.writeKTFFile(fileInstance, data)
			return err
		}
		return nil
	}
	switch name + descriptor {
	case "<init>()V":
		r.outputStreams[instance] = nil
		return 0, nil
	case "<init>(Ljava/io/OutputStream;)V":
		redirected, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if redirected == 0 {
			redirected = instance
		}
		r.outputTargets[instance] = redirected
		return 0, nil
	case "write(I)V", "writeByte(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, appendBytes([]byte{byte(value)})
	case "writeBoolean(Z)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		if value != 0 {
			value = 1
		}
		return 0, appendBytes([]byte{byte(value)})
	case "writeShort(I)V", "writeChar(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], uint16(value))
		return 0, appendBytes(encoded[:])
	case "writeInt(I)V":
		value, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], value)
		return 0, appendBytes(encoded[:])
	case "writeLong(J)V":
		low, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		high, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(high)<<32|uint64(low))
		return 0, appendBytes(encoded[:])
	case "write([B)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArray(array)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, appendBytes(data)
	case "write([BII)V":
		array, valueErr := r.parameter(2)
		if valueErr != nil {
			return 0, valueErr
		}
		offset, valueErr := r.parameter(3)
		if valueErr != nil {
			return 0, valueErr
		}
		count, valueErr := r.parameter(4)
		if valueErr != nil {
			return 0, valueErr
		}
		data, valueErr := r.readJavaByteArrayRange(array, offset, count)
		if valueErr != nil {
			return 0, valueErr
		}
		return 0, appendBytes(data)
	case "flush()V", "close()V":
		return 0, nil
	default:
		return 0, nil
	}
}
