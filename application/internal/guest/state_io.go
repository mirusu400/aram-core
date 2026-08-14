package guest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"math"
	"unicode/utf8"
)

type StateWriter struct {
	output io.Writer
	hash   hash.Hash
	Offset int64
	Err    error
}

func NewStateWriter(output io.Writer) *StateWriter {
	digest := sha256.New()
	return &StateWriter{
		output: io.MultiWriter(output, digest),
		hash:   digest,
	}
}

func (w *StateWriter) Write(data []byte) {
	if w.Err != nil {
		return
	}
	count, err := w.output.Write(data)
	w.Offset += int64(count)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}
	w.Err = err
}

func (w *StateWriter) U8(value uint8) {
	w.Write([]byte{value})
}

func (w *StateWriter) U32(value uint32) {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	w.Write(data[:])
}

func (w *StateWriter) U64(value uint64) {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	w.Write(data[:])
}

func (w *StateWriter) String16(value string) {
	if w.Err != nil {
		return
	}
	if !utf8.ValidString(value) || len(value) > math.MaxUint16 {
		w.Err = fmt.Errorf("invalid or oversized state string")
		return
	}
	var size [2]byte
	binary.LittleEndian.PutUint16(size[:], uint16(len(value)))
	w.Write(size[:])
	w.Write([]byte(value))
}

func (w *StateWriter) Digest() []byte {
	return w.hash.Sum(nil)
}

type StateDecoder struct {
	Reader *bytes.Reader
	Offset int64
	Err    error
}

func (d *StateDecoder) Bytes(size int) []byte {
	if d.Err != nil {
		return nil
	}
	if size < 0 || int64(size) > int64(d.Reader.Len()) {
		d.Err = fmt.Errorf("load state at offset 0x%x: truncated field", d.Offset)
		return nil
	}
	data := make([]byte, size)
	count, err := io.ReadFull(d.Reader, data)
	d.Offset += int64(count)
	if err != nil {
		d.Err = fmt.Errorf("load state at offset 0x%x: %w", d.Offset-int64(count), err)
		return nil
	}
	return data
}

func (d *StateDecoder) U8() uint8 {
	data := d.Bytes(1)
	if len(data) != 1 {
		return 0
	}
	return data[0]
}

func (d *StateDecoder) U32() uint32 {
	data := d.Bytes(4)
	if len(data) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data)
}

func (d *StateDecoder) U64() uint64 {
	data := d.Bytes(8)
	if len(data) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}

func (d *StateDecoder) String16() string {
	sizeData := d.Bytes(2)
	if len(sizeData) != 2 {
		return ""
	}
	value := string(d.Bytes(int(binary.LittleEndian.Uint16(sizeData))))
	if d.Err == nil && !utf8.ValidString(value) {
		d.Err = fmt.Errorf("load state at offset 0x%x: invalid UTF-8 string", d.Offset-int64(len(value)))
	}
	return value
}

func (d *StateDecoder) Reserved(size int) {
	data := d.Bytes(size)
	if d.Err == nil {
		for _, value := range data {
			if value != 0 {
				d.Err = fmt.Errorf("load state at offset 0x%x: nonzero reserved field", d.Offset-int64(size))
				return
			}
		}
	}
}

func (d *StateDecoder) Fail(reason string) error {
	if d.Err != nil {
		return d.Err
	}
	return fmt.Errorf("load state at offset 0x%x: %s", d.Offset, reason)
}

func WriteFull(output io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := output.Write(data)
		if count > 0 {
			data = data[count:]
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
