package eads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"strings"
)

const HeaderSize = 0x30

var Magic = []byte("EADS")

type Image struct {
	RecordOffset uint32
	FormatWord   uint32
	BuildWord    uint32
	TextBase     uint32
	TextSize     uint32
	DataBase     uint32
	BSSSize      uint32
	Reserved     uint32
	Name         string
}

func (i Image) PayloadOffset() uint32 { return i.RecordOffset + HeaderSize }
func (i Image) RecordEnd() uint32     { return i.PayloadOffset() + i.TextSize }
func (i Image) GuestEntry() uint32    { return i.TextBase | 1 }

type LoadedImage struct {
	Image       Image
	GuestEntry  uint32
	TextAddress uint32
	TextSize    uint32
	DataAddress uint32
	BSSSize     uint32
}

type FormatError struct {
	Offset uint64
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("EADS at 0x%x: %s", e.Offset, e.Reason)
}

func Parse(data []byte, recordOffset uint32) (Image, error) {
	fail := func(offset uint64, reason string) (Image, error) {
		return Image{}, &FormatError{Offset: offset, Reason: reason}
	}
	recordStart := uint64(recordOffset)
	if recordStart > uint64(len(data)) || HeaderSize > uint64(len(data))-recordStart {
		return fail(recordStart, "truncated header")
	}
	if !bytes.Equal(data[recordStart:recordStart+4], Magic) {
		return fail(recordStart, "magic mismatch")
	}
	image := Image{
		RecordOffset: recordOffset,
		FormatWord:   u32(data, recordStart+4),
		BuildWord:    u32(data, recordStart+8),
		TextBase:     u32(data, recordStart+12),
		TextSize:     u32(data, recordStart+16),
		DataBase:     u32(data, recordStart+20),
		BSSSize:      u32(data, recordStart+24),
		Reserved:     u32(data, recordStart+28),
	}
	rawName := data[recordStart+0x20 : recordStart+0x30]
	if index := bytes.IndexByte(rawName, 0); index >= 0 {
		rawName = rawName[:index]
	}
	image.Name = string(rawName)

	payload := recordStart + HeaderSize
	if image.Name == "" || strings.IndexFunc(image.Name, func(r rune) bool {
		return r < 0x20 || r > 0x7e
	}) >= 0 {
		return fail(recordStart+0x20, "image name is not printable ASCII")
	}
	if image.TextBase&0xfff != 0 {
		return fail(recordStart+12, "text base is not page aligned")
	}
	if image.DataBase&0xfff != 0 {
		return fail(recordStart+20, "data base is not page aligned")
	}
	if image.TextSize < 4 {
		return fail(recordStart+16, "text section is smaller than its fixed veneer")
	}
	if image.BSSSize < 4 {
		return fail(recordStart+24, "BSS section is too small")
	}
	recordEnd := payload + uint64(image.TextSize)
	if recordEnd > uint64(len(data)) {
		return fail(recordStart+16, "text section exceeds input")
	}
	if recordEnd > math.MaxUint32 {
		return fail(recordStart, "record end exceeds 32-bit format")
	}
	textEnd := uint64(image.TextBase) + uint64(image.TextSize)
	dataEnd := uint64(image.DataBase) + uint64(image.BSSSize)
	if textEnd > 1<<32 {
		return fail(recordStart+16, "text range exceeds 32-bit guest address space")
	}
	if dataEnd > 1<<32 {
		return fail(recordStart+24, "BSS range exceeds 32-bit guest address space")
	}
	if uint64(image.TextBase) < dataEnd && uint64(image.DataBase) < textEnd {
		return fail(recordStart+12, "guest ranges overlap")
	}
	if !bytes.Equal(data[payload:payload+2], []byte{0x00, 0xb5}) {
		return fail(payload, "text does not start with a Thumb veneer")
	}
	return image, nil
}

func ExtractText(data []byte, image Image) ([]byte, error) {
	verified, err := Parse(data, image.RecordOffset)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(verified, image) {
		return nil, &FormatError{
			Offset: uint64(image.RecordOffset),
			Reason: "image metadata does not match input",
		}
	}
	start := uint64(image.PayloadOffset())
	end := start + uint64(image.TextSize)
	return append([]byte(nil), data[start:end]...), nil
}

// Load copies an EADS text payload into text and clears the declared BSS
// destination. Callers map the two buffers at TextAddress and DataAddress.
func Load(data []byte, image Image, text, bss []byte) (LoadedImage, error) {
	verified, err := Parse(data, image.RecordOffset)
	if err != nil {
		return LoadedImage{}, err
	}
	if !reflect.DeepEqual(verified, image) {
		return LoadedImage{}, &FormatError{
			Offset: uint64(image.RecordOffset),
			Reason: "image metadata does not match input",
		}
	}
	if uint64(len(text)) < uint64(image.TextSize) {
		return LoadedImage{}, &FormatError{
			Offset: uint64(image.RecordOffset) + 16,
			Reason: "text destination is too small",
		}
	}
	if uint64(len(bss)) < uint64(image.BSSSize) {
		return LoadedImage{}, &FormatError{
			Offset: uint64(image.RecordOffset) + 24,
			Reason: "BSS destination is too small",
		}
	}
	start := uint64(image.PayloadOffset())
	copy(text[:image.TextSize], data[start:start+uint64(image.TextSize)])
	clear(bss[:image.BSSSize])
	return LoadedImage{
		Image:       image,
		GuestEntry:  image.GuestEntry(),
		TextAddress: image.TextBase,
		TextSize:    image.TextSize,
		DataAddress: image.DataBase,
		BSSSize:     image.BSSSize,
	}, nil
}

func Inspect(data []byte) []Image {
	var images []Image
	for start := 0; start < len(data); {
		index := bytes.Index(data[start:], Magic)
		if index < 0 {
			break
		}
		offset := start + index
		image, err := Parse(data, uint32(offset))
		if err == nil {
			images = append(images, image)
			start = int(image.RecordEnd())
			continue
		}
		start = offset + 1
	}
	return images
}

func u32(data []byte, offset uint64) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}
