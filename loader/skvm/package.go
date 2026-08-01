// Package skvm parses SK Telecom SK-VM distribution archives.
//
// An observed distribution is a ZIP containing a basename-matched quartet:
// a text .msd descriptor, a binary .mod installer record, a .wmr resource
// bundle, and a .jar containing Java class files. The JAR may be prefixed by
// an SKT header whose little-endian first word gives the ZIP offset.
package skvm

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	MaxArchiveEntries = 10_000
	MaxMemberSize     = uint64(256 << 20)
	MaxExpandedSize   = uint64(512 << 20)
	MaxJARHeaderSize  = uint32(4 << 10)
)

var ErrNotPackage = errors.New("not an SKVM package")

type Descriptor struct {
	Name          string
	Version       string
	Vendor        string
	MainClass     string
	JARURL        string
	JARSize       uint64
	Profiles      []string
	Configuration string
	ProgramName   string
	MIMEType      string
	Raw           map[string]string
}

type Class struct {
	Name         string
	MinorVersion uint16
	MajorVersion uint16
	Data         []byte
}

type Record struct {
	ID   uint32
	Data []byte
}

// RecordStore is an SKT installer-provided RMS image. NextID is kept from
// the handset metadata because RMS record IDs are never reused after records
// are deleted.
type RecordStore struct {
	Name    string
	NextID  uint32
	Records []Record
}

type Package struct {
	Descriptor   Descriptor
	BaseName     string
	MSDName      string
	MODName      string
	WMRName      string
	JARName      string
	JARHeader    []byte
	Module       []byte
	WMR          []byte
	Classes      map[string]Class
	Resources    map[string][]byte
	Files        map[string][]byte
	RecordStores []RecordStore
}

type FormatError struct {
	Path   string
	Offset int64
	Reason string
}

func (e *FormatError) Error() string {
	location := e.Path
	if location == "" {
		location = "package"
	}
	if e.Offset >= 0 {
		return fmt.Sprintf("SKVM package %q at offset 0x%x: %s", location, e.Offset, e.Reason)
	}
	return fmt.Sprintf("SKVM package %q: %s", location, e.Reason)
}

func formatError(name string, offset int64, reason string) error {
	return &FormatError{Path: name, Offset: offset, Reason: reason}
}

// Inspect validates and expands an SKVM distribution ZIP. ZIP data without an
// unambiguous SKTP descriptor and basename-matched JAR/MOD/WMR quartet is
// reported as ErrNotPackage so callers may continue probing other formats.
func Inspect(data []byte) (Package, error) {
	if !hasZIPMagic(data) {
		return Package{}, ErrNotPackage
	}
	files, err := readZIP(data, "archive")
	if err != nil {
		return Package{}, err
	}

	type candidate struct {
		base       string
		msdName    string
		modName    string
		wmrName    string
		jarName    string
		descriptor Descriptor
	}
	var found []candidate
	for name, payload := range files {
		if !strings.EqualFold(path.Ext(name), ".msd") {
			continue
		}
		descriptor, parseErr := ParseDescriptor(payload)
		if parseErr != nil || !descriptor.isSKVM() {
			continue
		}
		base := strings.TrimSuffix(name, path.Ext(name))
		jarName, jarOK := findCaseInsensitive(files, base+".jar")
		modName, modOK := findCaseInsensitive(files, base+".mod")
		wmrName, wmrOK := findCaseInsensitive(files, base+".wmr")
		if !jarOK || !modOK || !wmrOK {
			continue
		}
		found = append(found, candidate{
			base:       base,
			msdName:    name,
			modName:    modName,
			wmrName:    wmrName,
			jarName:    jarName,
			descriptor: descriptor,
		})
	}
	if len(found) == 0 {
		return Package{}, ErrNotPackage
	}
	if len(found) != 1 {
		return Package{}, formatError("archive", -1, "multiple SKVM application quartets")
	}
	selected := found[0]
	jarHeader, jarContents, err := unwrapJAR(selected.jarName, files[selected.jarName])
	if err != nil {
		return Package{}, err
	}
	contents, err := readZIP(jarContents, selected.jarName)
	if err != nil {
		return Package{}, err
	}

	classes := make(map[string]Class)
	resources := make(map[string][]byte)
	for name, payload := range contents {
		if !strings.EqualFold(path.Ext(name), ".class") {
			resources[name] = payload
			continue
		}
		className := strings.TrimSuffix(name, path.Ext(name))
		class, parseErr := inspectClass(name, className, payload)
		if parseErr != nil {
			return Package{}, parseErr
		}
		if _, duplicate := classes[className]; duplicate {
			return Package{}, formatError(name, -1, "duplicate class name")
		}
		classes[className] = class
	}
	if len(classes) == 0 {
		return Package{}, formatError(selected.jarName, -1, "JAR contains no class files")
	}
	recordStores, err := inspectRecordStores(files)
	if err != nil {
		return Package{}, err
	}
	mainName := strings.ReplaceAll(selected.descriptor.MainClass, ".", "/")
	if _, ok := classes[mainName]; !ok {
		return Package{}, formatError(
			selected.jarName,
			-1,
			fmt.Sprintf("main class %q is missing", selected.descriptor.MainClass),
		)
	}

	return Package{
		Descriptor:   selected.descriptor,
		BaseName:     selected.base,
		MSDName:      selected.msdName,
		MODName:      selected.modName,
		WMRName:      selected.wmrName,
		JARName:      selected.jarName,
		JARHeader:    jarHeader,
		Module:       files[selected.modName],
		WMR:          files[selected.wmrName],
		Classes:      classes,
		Resources:    resources,
		Files:        files,
		RecordStores: recordStores,
	}, nil
}

func inspectRecordStores(files map[string][]byte) ([]RecordStore, error) {
	metadataNames := make([]string, 0)
	for name := range files {
		if strings.EqualFold(path.Dir(name), "rs") &&
			strings.EqualFold(path.Ext(name), ".sb") {
			metadataNames = append(metadataNames, name)
		}
	}
	sort.Slice(metadataNames, func(i, j int) bool {
		return strings.ToLower(metadataNames[i]) < strings.ToLower(metadataNames[j])
	})
	stores := make([]RecordStore, 0, len(metadataNames))
	seen := make(map[string]bool, len(metadataNames))
	for _, metadataName := range metadataNames {
		databaseName := strings.TrimSuffix(
			metadataName,
			path.Ext(metadataName),
		) + ".db"
		matchedName, ok := findCaseInsensitive(files, databaseName)
		if !ok {
			return nil, formatError(metadataName, -1, "record store data file is missing")
		}
		store, err := inspectRecordStore(
			metadataName,
			files[metadataName],
			files[matchedName],
		)
		if err != nil {
			return nil, err
		}
		if seen[store.Name] {
			return nil, formatError(metadataName, -1, "duplicate record store name")
		}
		seen[store.Name] = true
		stores = append(stores, store)
	}
	sort.Slice(stores, func(i, j int) bool {
		return stores[i].Name < stores[j].Name
	})
	return stores, nil
}

func inspectRecordStore(
	metadataName string,
	metadata, database []byte,
) (RecordStore, error) {
	if len(metadata) < 6 || binary.BigEndian.Uint32(metadata) != 2 {
		return RecordStore{}, formatError(metadataName, 0, "unsupported record store metadata")
	}
	nameSize := int(binary.BigEndian.Uint16(metadata[4:]))
	header := 6 + nameSize
	if nameSize == 0 || header < 6 || header+20 > len(metadata) {
		return RecordStore{}, formatError(metadataName, 4, "truncated record store name")
	}
	name, ok := decodeModifiedUTF8(metadata[6:header])
	if !ok || strings.TrimSpace(name) == "" || strings.IndexByte(name, 0) >= 0 {
		return RecordStore{}, formatError(metadataName, 6, "invalid record store name")
	}
	nextID := binary.BigEndian.Uint32(metadata[header:])
	recordCount := binary.BigEndian.Uint32(metadata[header+4:])
	databaseSize := binary.BigEndian.Uint32(metadata[header+8:])
	expected := uint64(header) + 20 + uint64(recordCount)*12
	if nextID == 0 || nextID == math.MaxUint32 ||
		expected != uint64(len(metadata)) ||
		uint64(databaseSize) != uint64(len(database)) {
		return RecordStore{}, formatError(metadataName, int64(header), "invalid record store layout")
	}
	records := make([]Record, 0, recordCount)
	seen := make(map[uint32]bool, recordCount)
	for index := uint32(0); index < recordCount; index++ {
		offset := header + 20 + int(index)*12
		recordID := binary.BigEndian.Uint32(metadata[offset:])
		dataOffset := binary.BigEndian.Uint32(metadata[offset+4:])
		dataSize := binary.BigEndian.Uint32(metadata[offset+8:])
		dataEnd := uint64(dataOffset) + uint64(dataSize)
		if recordID == 0 || recordID >= nextID || seen[recordID] ||
			dataEnd > uint64(len(database)) {
			return RecordStore{}, formatError(
				metadataName,
				int64(offset),
				"invalid record store entry",
			)
		}
		seen[recordID] = true
		records = append(records, Record{
			ID: recordID,
			Data: append(
				[]byte(nil),
				database[dataOffset:uint32(dataEnd)]...,
			),
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
	return RecordStore{Name: name, NextID: nextID, Records: records}, nil
}

// Java DataOutputStream.writeUTF uses modified UTF-8: NUL is a two-byte
// sequence and supplementary code points are encoded as UTF-16 surrogates.
func decodeModifiedUTF8(data []byte) (string, bool) {
	units := make([]uint16, 0, len(data))
	for offset := 0; offset < len(data); {
		first := data[offset]
		switch {
		case first > 0 && first < 0x80:
			units = append(units, uint16(first))
			offset++
		case first&0xe0 == 0xc0 && offset+1 < len(data):
			second := data[offset+1]
			if second&0xc0 != 0x80 {
				return "", false
			}
			value := uint16(first&0x1f)<<6 | uint16(second&0x3f)
			if value != 0 && value < 0x80 {
				return "", false
			}
			units = append(units, value)
			offset += 2
		case first&0xf0 == 0xe0 && offset+2 < len(data):
			second, third := data[offset+1], data[offset+2]
			if second&0xc0 != 0x80 || third&0xc0 != 0x80 {
				return "", false
			}
			value := uint16(first&0x0f)<<12 |
				uint16(second&0x3f)<<6 | uint16(third&0x3f)
			if value < 0x800 {
				return "", false
			}
			units = append(units, value)
			offset += 3
		default:
			return "", false
		}
	}
	for index, unit := range units {
		if 0xd800 <= unit && unit <= 0xdbff {
			if index+1 >= len(units) || units[index+1] < 0xdc00 ||
				units[index+1] > 0xdfff {
				return "", false
			}
		} else if 0xdc00 <= unit && unit <= 0xdfff &&
			(index == 0 || units[index-1] < 0xd800 || units[index-1] > 0xdbff) {
			return "", false
		}
	}
	return string(utf16.Decode(units)), true
}

func ParseDescriptor(data []byte) (Descriptor, error) {
	descriptor := Descriptor{Raw: make(map[string]string)}
	for _, rawLine := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSpace(bytes.TrimSuffix(rawLine, []byte{'\r'}))
		if len(line) == 0 {
			continue
		}
		rawKey, rawValue, ok := bytes.Cut(line, []byte{':'})
		if !ok {
			continue
		}
		key := string(bytes.TrimSpace(rawKey))
		value := string(bytes.TrimSpace(rawValue))
		descriptor.Raw[key] = value
		switch strings.ToLower(key) {
		case "midlet-name":
			descriptor.Name = value
		case "midlet-version":
			descriptor.Version = value
		case "midlet-vendor":
			descriptor.Vendor = value
		case "midlet-jar-url":
			descriptor.JARURL = value
		case "midlet-jar-size":
			if value == "" {
				continue
			}
			size, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return Descriptor{}, formatError(".msd", -1, "MIDlet-Jar-Size is not a decimal integer")
			}
			descriptor.JARSize = size
		case "microedition-profile":
			for _, profile := range strings.Split(value, ",") {
				if profile = strings.TrimSpace(profile); profile != "" {
					descriptor.Profiles = append(descriptor.Profiles, profile)
				}
			}
		case "microedition-configuration":
			descriptor.Configuration = value
		case "midlet-1":
			parts := strings.Split(value, ",")
			if len(parts) != 0 {
				descriptor.MainClass = strings.TrimSpace(parts[len(parts)-1])
			}
		case "dd-progname":
			descriptor.ProgramName = value
		case "dd-mime-type":
			descriptor.MIMEType = value
		}
	}
	if descriptor.MainClass == "" {
		return Descriptor{}, formatError(".msd", -1, "MIDlet-1 main class is missing")
	}
	if !validClassName(descriptor.MainClass) {
		return Descriptor{}, formatError(".msd", -1, "MIDlet-1 main class is invalid")
	}
	return descriptor, nil
}

func (d Descriptor) isSKVM() bool {
	for _, profile := range d.Profiles {
		if strings.HasPrefix(strings.ToUpper(profile), "SKTP-") {
			return true
		}
	}
	return false
}

func validClassName(name string) bool {
	if name == "" || len(name) > 512 || strings.HasPrefix(name, ".") ||
		strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, character := range []byte(name) {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '$' || character == '.' ||
			character == '/' {
			continue
		}
		return false
	}
	return true
}

func inspectClass(pathName, fallbackName string, data []byte) (Class, error) {
	if len(data) < 8 {
		return Class{}, formatError(pathName, 0, "class header is truncated")
	}
	if binary.BigEndian.Uint32(data[:4]) != 0xcafebabe {
		return Class{}, formatError(pathName, 0, "class magic is missing")
	}
	return Class{
		Name:         fallbackName,
		MinorVersion: binary.BigEndian.Uint16(data[4:6]),
		MajorVersion: binary.BigEndian.Uint16(data[6:8]),
		Data:         data,
	}, nil
}

func unwrapJAR(name string, data []byte) ([]byte, []byte, error) {
	if hasZIPMagic(data) {
		return nil, data, nil
	}
	if len(data) < 8 {
		return nil, nil, formatError(name, 0, "JAR wrapper is truncated")
	}
	headerSize := binary.LittleEndian.Uint32(data[:4])
	if headerSize < 4 || headerSize > MaxJARHeaderSize {
		return nil, nil, formatError(name, 0, "invalid JAR wrapper size")
	}
	if uint64(headerSize)+4 > uint64(len(data)) {
		return nil, nil, formatError(name, 0, "JAR wrapper exceeds file size")
	}
	if !hasZIPMagic(data[headerSize:]) {
		return nil, nil, formatError(name, int64(headerSize), "wrapped ZIP magic is missing")
	}
	return data[:headerSize], data[headerSize:], nil
}

func readZIP(data []byte, label string) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, formatError(label, 0, "invalid ZIP: "+err.Error())
	}
	if len(reader.File) > MaxArchiveEntries {
		return nil, formatError(label, -1, fmt.Sprintf("contains more than %d entries", MaxArchiveEntries))
	}
	files := make(map[string][]byte, len(reader.File))
	seen := make(map[string]string, len(reader.File))
	var expanded uint64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.FileInfo().Mode()&fs.ModeSymlink != 0 {
			return nil, formatError(entry.Name, -1, "symbolic link is not allowed")
		}
		name, ok := safeName(entry.Name)
		if !ok {
			return nil, formatError(entry.Name, -1, "unsafe member path")
		}
		key := strings.ToLower(name)
		if previous, duplicate := seen[key]; duplicate {
			return nil, formatError(name, -1, fmt.Sprintf("duplicates %q by case", previous))
		}
		seen[key] = name
		size := entry.UncompressedSize64
		if size > MaxMemberSize {
			return nil, formatError(name, -1, "member exceeds size limit")
		}
		if expanded > math.MaxUint64-size || expanded+size > MaxExpandedSize {
			return nil, formatError(label, -1, "expanded data exceeds limit")
		}
		expanded += size
		stream, openErr := entry.Open()
		if openErr != nil {
			return nil, formatError(name, -1, "open member: "+openErr.Error())
		}
		payload, readErr := io.ReadAll(io.LimitReader(stream, int64(size)+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, formatError(name, -1, "read member: "+readErr.Error())
		}
		if closeErr != nil {
			return nil, formatError(name, -1, "close member: "+closeErr.Error())
		}
		if uint64(len(payload)) != size {
			return nil, formatError(name, -1, "uncompressed size mismatch")
		}
		files[name] = payload
	}
	return files, nil
}

func findCaseInsensitive(files map[string][]byte, requested string) (string, bool) {
	for name := range files {
		if strings.EqualFold(name, requested) {
			return name, true
		}
	}
	return "", false
}

func safeName(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.IndexByte(name, 0) >= 0 || strings.HasPrefix(name, "/") {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if strings.Contains(strings.Split(cleaned, "/")[0], ":") {
		return "", false
	}
	return cleaned, cleaned == name
}

func hasZIPMagic(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) ||
		bytes.Equal(data[:4], []byte{'P', 'K', 5, 6}) ||
		bytes.Equal(data[:4], []byte{'P', 'K', 7, 8})
}
