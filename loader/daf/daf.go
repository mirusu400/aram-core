// Package daf recognizes the SKT native WIPI-C application: a ".daf"
// executable distributed beside a ".mod" installer record whose content type
// is application/x-wipi-app.
//
// The executable is a relocatable ARM image. Text is linked at address zero
// and data at LinkedDataBase; a compact relocation stream names every word
// that holds one of those addresses. The first data word is the import
// pointer through which the application reaches the platform's WIPI-C
// tables. A resource pack of named files follows the relocations.
package daf

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	// OuterHeaderSize covers the leading size word and the 16-byte
	// application identifier the ".mod" record repeats.
	OuterHeaderSize = 0x20
	// HeaderSize is the fixed image header that follows the outer header.
	HeaderSize = 0x50
	// LinkedDataBase is the address the data section is linked at. Text is
	// linked at zero.
	LinkedDataBase = uint32(0x01001000)
	// ImportPointerMarker fills the first data word until the platform stores
	// the import root there.
	ImportPointerMarker = uint32(0xcafecafe)

	MaxArchiveEntries = 4096
	MaxMemberSize     = 64 << 20
	MaxExpandedSize   = 256 << 20
	MaxResources      = 8192
)

// Magic is the image header tag.
var Magic = []byte("gwZH")

var ErrNotPackage = errors.New("not an SKT native WIPI-C package")

type FormatError struct {
	Path   string
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("SKT WIPI-C %s: %s", e.Path, e.Reason)
}

// Target is the section an address stored in a relocated word points into.
type Target uint8

const (
	TargetText Target = iota
	TargetData
	// TargetSelfRelative marks a word that already holds a displacement from
	// its own location. It needs no adjustment while a section moves whole.
	TargetSelfRelative
)

type Relocation struct {
	Offset uint32
	Target Target
}

// Entries are the Clet lifecycle functions as text offsets.
type Entries struct {
	Start       uint32
	Pause       uint32
	Resume      uint32
	Destroy     uint32
	Paint       uint32
	HandleEvent uint32
}

type Resource struct {
	Name string
	Data []byte
}

type Image struct {
	ID              [16]byte
	Text            []byte
	Data            []byte
	BSSSize         uint32
	Entries         Entries
	TextRelocations []Relocation
	DataRelocations []Relocation
	Resources       []Resource
}

// Package is one distribution archive.
type Package struct {
	BaseName string
	DAFName  string
	MODName  string
	Image    Image
	Module   []byte
	// Files holds every archive member by its stored name.
	Files map[string][]byte
}

// Inspect recognizes a distribution ZIP holding exactly one ".daf" executable
// with a basename-matched ".mod" record.
func Inspect(data []byte) (Package, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], []byte("PK\x03\x04")) {
		return Package{}, ErrNotPackage
	}
	files, err := readZIP(data)
	if err != nil {
		return Package{}, ErrNotPackage
	}
	var found []string
	for name := range files {
		if strings.EqualFold(path.Ext(name), ".daf") {
			found = append(found, name)
		}
	}
	if len(found) == 0 {
		return Package{}, ErrNotPackage
	}
	if len(found) != 1 {
		return Package{}, &FormatError{Path: "archive", Reason: "multiple .daf executables"}
	}
	dafName := found[0]
	base := strings.TrimSuffix(dafName, path.Ext(dafName))
	modName := ""
	for name := range files {
		if strings.EqualFold(name, base+".mod") {
			modName = name
		}
	}
	if modName == "" {
		return Package{}, &FormatError{Path: dafName, Reason: "no matching .mod installer record"}
	}
	image, err := Parse(files[dafName])
	if err != nil {
		return Package{}, err
	}
	return Package{
		BaseName: path.Base(base),
		DAFName:  dafName,
		MODName:  modName,
		Image:    image,
		Module:   files[modName],
		Files:    files,
	}, nil
}

// Parse decodes one ".daf" executable.
func Parse(data []byte) (Image, error) {
	fail := func(reason string) (Image, error) {
		return Image{}, &FormatError{Path: "image", Reason: reason}
	}
	if len(data) < OuterHeaderSize+HeaderSize {
		return fail("truncated header")
	}
	if binary.LittleEndian.Uint32(data) != OuterHeaderSize {
		return fail("unexpected outer header size")
	}
	header := data[OuterHeaderSize : OuterHeaderSize+HeaderSize]
	if !bytes.Equal(header[:4], Magic) {
		return fail("magic mismatch")
	}
	word := func(offset int) uint32 { return binary.LittleEndian.Uint32(header[offset:]) }
	if word(8) != HeaderSize {
		return fail("unexpected image header size")
	}
	textSize, dataSize, bssSize := word(0x1c), word(0x20), word(0x24)
	relocationSize, resourceSize := word(0x28), word(0x30)
	total := uint64(OuterHeaderSize+HeaderSize) + uint64(textSize) + uint64(dataSize) +
		uint64(relocationSize) + uint64(resourceSize)
	if total != uint64(len(data)) {
		return fail("section sizes do not add up to the file size")
	}
	if textSize == 0 || textSize&3 != 0 || dataSize < 4 || dataSize&3 != 0 {
		return fail("text or data section has an invalid size")
	}
	if uint64(dataSize)+uint64(bssSize) > 64<<20 {
		return fail("data section exceeds the supported size")
	}
	offset := OuterHeaderSize + HeaderSize
	image := Image{BSSSize: bssSize}
	copy(image.ID[:], data[8:24])
	image.Text = bytes.Clone(data[offset : offset+int(textSize)])
	offset += int(textSize)
	image.Data = bytes.Clone(data[offset : offset+int(dataSize)])
	offset += int(dataSize)
	relocations := data[offset : offset+int(relocationSize)]
	offset += int(relocationSize)
	resources := data[offset:]

	if binary.LittleEndian.Uint32(image.Data) != ImportPointerMarker {
		return fail("data does not start with the import pointer marker")
	}
	entries := [6]uint32{}
	for index := range entries {
		entries[index] = word(0x38 + index*4)
		if entries[index]&^1 >= textSize {
			return fail("lifecycle entry is outside text")
		}
	}
	image.Entries = Entries{
		Start: entries[0], Pause: entries[1], Resume: entries[2],
		Destroy: entries[3], Paint: entries[4], HandleEvent: entries[5],
	}

	if len(relocations) < 8 {
		return fail("truncated relocation header")
	}
	textStream := uint64(binary.LittleEndian.Uint32(relocations))
	dataStream := uint64(binary.LittleEndian.Uint32(relocations[4:]))
	if 8+textStream+dataStream > uint64(len(relocations)) {
		return fail("relocation streams exceed their section")
	}
	var err error
	image.TextRelocations, err = decodeRelocations(relocations[8:8+textStream], image.Text, textSize, dataSize+bssSize)
	if err != nil {
		return fail("text relocations: " + err.Error())
	}
	image.DataRelocations, err = decodeRelocations(
		relocations[8+textStream:8+textStream+dataStream], image.Data, textSize, dataSize+bssSize)
	if err != nil {
		return fail("data relocations: " + err.Error())
	}
	image.Resources, err = parseResources(resources)
	if err != nil {
		return fail("resources: " + err.Error())
	}
	return image, nil
}

// decodeRelocations reads one relocation stream. Each entry starts with a
// byte whose high nibble is its kind and whose low nibble starts the distance
// from the previous entry:
//
//	bit 2 of the kind  the word holds a data address, otherwise a text address
//	low two bits 0     distance is the low nibble
//	low two bits 1     distance is 0x10 plus the low nibble
//	low two bits 2     distance is 12 bits: the low nibble and one more byte
//	low two bits 3     distance is 20 bits: the low nibble and two more bytes
//	bit 3 of the kind  a self-relative word; distance is 28 bits over three
//	                   more bytes
func decodeRelocations(stream, section []byte, textSize, dataSpan uint32) ([]Relocation, error) {
	var result []Relocation
	position := uint64(0)
	for index := 0; index < len(stream); {
		lead := stream[index]
		kind, low := lead>>4, uint64(lead&0x0f)
		index++
		extra := 0
		switch {
		case kind&8 != 0:
			extra = 3
		case kind&3 == 2:
			extra = 1
		case kind&3 == 3:
			extra = 2
		}
		if index+extra > len(stream) {
			return nil, errors.New("truncated entry")
		}
		distance := low
		if kind&8 == 0 && kind&3 == 1 {
			distance += 0x10
		}
		for range extra {
			distance = distance<<8 | uint64(stream[index])
			index++
		}
		position += distance
		if position+4 > uint64(len(section)) {
			return nil, fmt.Errorf("entry at 0x%x is outside its section", position)
		}
		relocation := Relocation{Offset: uint32(position)}
		value := binary.LittleEndian.Uint32(section[position:])
		switch {
		case kind&8 != 0:
			relocation.Target = TargetSelfRelative
		case kind&4 != 0:
			relocation.Target = TargetData
			if value < LinkedDataBase || uint64(value-LinkedDataBase) > uint64(dataSpan) {
				return nil, fmt.Errorf("data address 0x%08x at 0x%x is outside data", value, position)
			}
		default:
			relocation.Target = TargetText
			if value > textSize {
				return nil, fmt.Errorf("text address 0x%08x at 0x%x is outside text", value, position)
			}
		}
		result = append(result, relocation)
	}
	return result, nil
}

// parseResources reads the resource pack: a count at 0x18, that many
// (offset, size) pairs from 0x1c, the NUL-terminated names in the same order,
// and then the file bodies the offsets index.
func parseResources(pack []byte) ([]Resource, error) {
	if len(pack) == 0 {
		return nil, nil
	}
	if len(pack) < 0x1c {
		return nil, errors.New("truncated pack header")
	}
	count := uint64(binary.LittleEndian.Uint32(pack[0x18:]))
	if count > MaxResources || 0x1c+count*8 > uint64(len(pack)) {
		return nil, errors.New("entry table exceeds the pack")
	}
	cursor := 0x1c + int(count)*8
	names := make([]string, count)
	for index := range names {
		end := bytes.IndexByte(pack[cursor:], 0)
		if end < 0 {
			return nil, errors.New("unterminated resource name")
		}
		names[index] = string(pack[cursor : cursor+end])
		cursor += end + 1
	}
	bodies := pack[cursor:]
	result := make([]Resource, 0, count)
	for index := range names {
		offset := uint64(binary.LittleEndian.Uint32(pack[0x1c+index*8:]))
		size := uint64(binary.LittleEndian.Uint32(pack[0x1c+index*8+4:]))
		if offset+size > uint64(len(bodies)) {
			return nil, fmt.Errorf("resource %q exceeds the pack", names[index])
		}
		result = append(result, Resource{Name: names[index], Data: bodies[offset : offset+size]})
	}
	return result, nil
}

// Loaded is an image placed at chosen guest addresses.
type Loaded struct {
	TextBase uint32
	DataBase uint32
	Text     []byte
	// Data holds the initialized data followed by the zeroed BSS.
	Data    []byte
	Entries Entries
}

// Load relocates the image to textBase and dataBase and stores importRoot in
// the import pointer.
func (i Image) Load(textBase, dataBase, importRoot uint32) (Loaded, error) {
	if textBase&3 != 0 || dataBase&3 != 0 {
		return Loaded{}, &FormatError{Path: "image", Reason: "section base is not word aligned"}
	}
	loaded := Loaded{
		TextBase: textBase,
		DataBase: dataBase,
		Text:     bytes.Clone(i.Text),
		Data:     make([]byte, len(i.Data)+int(i.BSSSize)),
	}
	copy(loaded.Data, i.Data)
	apply := func(section []byte, relocations []Relocation) {
		for _, relocation := range relocations {
			value := binary.LittleEndian.Uint32(section[relocation.Offset:])
			switch relocation.Target {
			case TargetText:
				value += textBase
			case TargetData:
				value = value - LinkedDataBase + dataBase
			default:
				continue
			}
			binary.LittleEndian.PutUint32(section[relocation.Offset:], value)
		}
	}
	apply(loaded.Text, i.TextRelocations)
	apply(loaded.Data, i.DataRelocations)
	binary.LittleEndian.PutUint32(loaded.Data, importRoot)
	loaded.Entries = Entries{
		Start:       i.Entries.Start + textBase,
		Pause:       i.Entries.Pause + textBase,
		Resume:      i.Entries.Resume + textBase,
		Destroy:     i.Entries.Destroy + textBase,
		Paint:       i.Entries.Paint + textBase,
		HandleEvent: i.Entries.HandleEvent + textBase,
	}
	return loaded, nil
}

func readZIP(data []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(reader.File) > MaxArchiveEntries {
		return nil, errors.New("too many archive entries")
	}
	files := make(map[string][]byte, len(reader.File))
	var expanded uint64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > MaxMemberSize {
			return nil, errors.New("archive member exceeds size limit")
		}
		expanded += entry.UncompressedSize64
		if expanded > MaxExpandedSize {
			return nil, errors.New("archive exceeds expanded size limit")
		}
		handle, err := entry.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(handle, MaxMemberSize+1))
		_ = handle.Close()
		if err != nil {
			return nil, err
		}
		if uint64(len(content)) > MaxMemberSize {
			return nil, errors.New("archive member exceeds size limit")
		}
		files[strings.ReplaceAll(entry.Name, "\\", "/")] = content
	}
	return files, nil
}
