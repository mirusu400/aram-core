// Package raptor parses Raptor-packaged WIPI-C applications. These
// distributions are ZIP files with an app_info descriptor and an AID-named
// JAR. The JAR contains resources and a section-loaded ARM ELF named
// binary.mod (or binary.mie).
package raptor

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
	"strings"

	"github.com/mirusu400/aram-core/loader/internal/zipname"
)

const (
	MaxArchiveEntries = 10_000
	MaxMemberSize     = uint64(256 << 20)
	MaxExpandedSize   = uint64(512 << 20)
	MaxSections       = 1_024
	MaxSectionSize    = uint64(256 << 20)
)

const (
	elfClass32     = 1
	elfDataLittle  = 1
	elfVersion     = 1
	elfTypeExec    = 2
	elfMachineARM  = 40
	elfHeaderSize  = 52
	sectionHdrSize = 40

	sectionNull     = 0
	sectionProgBits = 1
	sectionString   = 3
	sectionNoBits   = 8
	sectionREL      = 9

	sectionWrite = 1
	sectionAlloc = 2
	sectionExec  = 4
)

var (
	ErrNotPackage = errors.New("not a Raptor WIPI-C package")
	raptorMagic   = []byte("RAPT")
)

type Descriptor struct {
	AID       string
	PID       string
	Name      string
	Version   string
	MainClass string
	Vendor    string
}

type Metadata struct {
	Version      uint32
	Size         uint32
	EntryOffset  uint32
	Checksum     uint32
	ABIVersion   uint32
	Flags        uint32
	Identifier   string
	Dependencies []string
	Raw          []byte
}

type Section struct {
	Index     int
	Name      string
	Type      uint32
	Flags     uint32
	Address   uint32
	Alignment uint32
	Data      []byte
	Size      uint32
}

func (s Section) Writable() bool {
	return s.Flags&sectionWrite != 0
}

func (s Section) Allocated() bool {
	return s.Flags&sectionAlloc != 0
}

func (s Section) Executable() bool {
	return s.Flags&sectionExec != 0
}

func (s Section) ZeroFill() bool {
	return s.Type == sectionNoBits
}

type Relocation struct {
	Section string
	Target  string
	Offset  uint32
	Symbol  uint32
	Type    uint8
}

type Image struct {
	Entry       uint32
	Flags       uint32
	Sections    []Section
	Relocations []Relocation
	Metadata    Metadata
}

func (i Image) AllocatedSections() []Section {
	sections := make([]Section, 0, len(i.Sections))
	for _, section := range i.Sections {
		if section.Allocated() && section.Size != 0 {
			sections = append(sections, section)
		}
	}
	return sections
}

func (i Image) Section(name string) (Section, bool) {
	for _, section := range i.Sections {
		if section.Name == name {
			return section, true
		}
	}
	return Section{}, false
}

// Raptor modules ship from two toolchains. GNU binutils emits .text/.data/.bss
// while ARM RVCT emits its region names ER_RO/ER_RW/ER_ZI, so a module built
// with the ARM chain carries no section called .text at all. Select the roles
// from the section flags, which both layouts set identically, and fall back to
// the lowest matching index so a module keeps one canonical answer.
func (i Image) role(match func(Section) bool) (Section, bool) {
	for _, section := range i.Sections {
		if section.Allocated() && section.Size != 0 && match(section) {
			return section, true
		}
	}
	return Section{}, false
}

// CodeSection returns the allocated executable region holding the entry point.
func (i Image) CodeSection() (Section, bool) {
	return i.role(func(section Section) bool {
		return section.Executable() && !section.ZeroFill()
	})
}

// DataSection returns the initialized read-write region.
func (i Image) DataSection() (Section, bool) {
	return i.role(func(section Section) bool {
		return section.Writable() && !section.ZeroFill()
	})
}

// ZeroSection returns the zero-filled region.
func (i Image) ZeroSection() (Section, bool) {
	return i.role(Section.ZeroFill)
}

type Package struct {
	Descriptor Descriptor
	JARName    string
	ModuleName string
	Image      Image
	Files      map[string][]byte
	Resources  map[string][]byte
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
		return fmt.Sprintf("Raptor package %q at offset 0x%x: %s", location, e.Offset, e.Reason)
	}
	return fmt.Sprintf("Raptor package %q: %s", location, e.Reason)
}

func formatError(name string, offset int64, reason string) error {
	return &FormatError{Path: name, Offset: offset, Reason: reason}
}

// Inspect validates and expands a Raptor WIPI-C distribution. Inputs without
// an app_info marker are reported as ErrNotPackage so callers can continue
// probing other WIPI containers.
func Inspect(data []byte) (Package, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) {
		return Package{}, ErrNotPackage
	}
	appInfoName, err := findAppInfo(data)
	if err != nil {
		return Package{}, err
	}
	if appInfoName == "" {
		return Package{}, ErrNotPackage
	}
	files, err := readZIP(data, "archive")
	if err != nil {
		return Package{}, err
	}
	descriptor, err := ParseDescriptor(files[appInfoName])
	if err != nil {
		return Package{}, err
	}
	jarName := path.Join(path.Dir(appInfoName), descriptor.AID+".jar")
	if path.Dir(appInfoName) == "." {
		jarName = descriptor.AID + ".jar"
	}
	jar, ok := files[jarName]
	if !ok {
		return Package{}, formatError(jarName, -1, "AID-named JAR is missing")
	}
	contents, err := readZIP(jar, jarName)
	if err != nil {
		return Package{}, err
	}

	moduleName := ""
	var module []byte
	for name, payload := range contents {
		base := strings.ToLower(path.Base(name))
		if base != "binary.mod" && base != "binary.mie" {
			continue
		}
		if moduleName != "" {
			return Package{}, formatError(jarName, -1, "multiple Raptor module images")
		}
		moduleName = name
		module = payload
	}
	if moduleName == "" {
		return Package{}, ErrNotPackage
	}
	image, err := InspectELF(moduleName, module)
	if err != nil {
		return Package{}, err
	}
	if image.Metadata.Identifier != descriptor.AID {
		return Package{}, formatError(
			moduleName,
			-1,
			fmt.Sprintf(
				"Raptor identifier %q does not match descriptor AID %q",
				image.Metadata.Identifier,
				descriptor.AID,
			),
		)
	}
	delete(contents, moduleName)
	return Package{
		Descriptor: descriptor,
		JARName:    jarName,
		ModuleName: moduleName,
		Image:      image,
		Files:      files,
		Resources:  contents,
	}, nil
}

func ParseDescriptor(data []byte) (Descriptor, error) {
	var descriptor Descriptor
	for _, rawLine := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSuffix(rawLine, []byte{'\r'})
		key, value, ok := bytes.Cut(line, []byte{':'})
		if !ok {
			continue
		}
		switch string(key) {
		case "AID":
			descriptor.AID = string(value)
		case "PID":
			descriptor.PID = string(value)
		case "Name":
			descriptor.Name = string(value)
		case "Ver":
			descriptor.Version = string(value)
		case "MClass":
			descriptor.MainClass = string(value)
		case "Vdr":
			descriptor.Vendor = string(value)
		}
	}
	if err := validateIdentifier("app_info", "AID", descriptor.AID); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

func parseMetadata(name string, data []byte) (Metadata, error) {
	if len(data) < 0x30 || !bytes.Equal(data[:4], raptorMagic) {
		return Metadata{}, formatError(name, -1, "invalid .raptor metadata")
	}
	size := binary.LittleEndian.Uint32(data[8:12])
	if size < 0x30 || uint64(size) != uint64(len(data)) {
		return Metadata{}, formatError(name, -1, ".raptor size does not match section size")
	}
	identifierOffset := binary.LittleEndian.Uint32(data[0x24:0x28])
	dependenciesOffset := binary.LittleEndian.Uint32(data[0x2c:0x30])
	identifier, ok := readCString(data[:size], identifierOffset)
	if !ok {
		return Metadata{}, formatError(name, int64(identifierOffset), "invalid Raptor identifier string")
	}
	if err := validateIdentifier(name, "identifier", identifier); err != nil {
		return Metadata{}, err
	}
	dependencyText, ok := readCString(data[:size], dependenciesOffset)
	if !ok {
		return Metadata{}, formatError(name, int64(dependenciesOffset), "invalid Raptor dependency string")
	}
	dependencies := strings.Fields(dependencyText)
	if strings.Join(dependencies, " ") != dependencyText {
		return Metadata{}, formatError(name, int64(dependenciesOffset), "invalid Raptor dependency list")
	}
	for _, dependency := range dependencies {
		if err := validateIdentifier(name, "dependency", dependency); err != nil {
			return Metadata{}, err
		}
	}
	return Metadata{
		Version:      binary.LittleEndian.Uint32(data[4:8]),
		Size:         size,
		EntryOffset:  binary.LittleEndian.Uint32(data[0x0c:0x10]),
		Checksum:     binary.LittleEndian.Uint32(data[0x14:0x18]),
		ABIVersion:   binary.LittleEndian.Uint32(data[0x18:0x1c]),
		Flags:        binary.LittleEndian.Uint32(data[0x1c:0x20]),
		Identifier:   identifier,
		Dependencies: dependencies,
		Raw:          append([]byte(nil), data[:size]...),
	}, nil
}

func validateIdentifier(pathName, field, value string) error {
	if value == "" {
		return formatError(pathName, -1, field+" is missing")
	}
	if len(value) > 255 {
		return formatError(pathName, -1, field+" exceeds 255 bytes")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return formatError(pathName, -1, field+" is not printable ASCII")
		}
	}
	if strings.ContainsAny(value, `/\:`) || value == "." || value == ".." {
		return formatError(pathName, -1, field+" is not a plain identifier")
	}
	return nil
}

func findAppInfo(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", formatError("archive", 0, "invalid ZIP: "+err.Error())
	}
	var found string
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name, ok := zipname.SafeName(entry.Name)
		if !ok || path.Base(name) != "app_info" {
			continue
		}
		if found != "" && found != name {
			return "", formatError("archive", -1, "multiple app_info descriptors")
		}
		found = name
	}
	return found, nil
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
		name, ok := zipname.SafeName(entry.Name)
		if !ok {
			return nil, formatError(entry.Name, -1, "unsafe member path")
		}
		key := strings.ToLower(name)
		if previous, duplicate := seen[key]; duplicate {
			return nil, formatError(name, -1, fmt.Sprintf("duplicates %q", previous))
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
		stream, err := entry.Open()
		if err != nil {
			return nil, formatError(name, -1, "open member: "+err.Error())
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

func readCString(data []byte, offset uint32) (string, bool) {
	if uint64(offset) >= uint64(len(data)) {
		return "", false
	}
	end := bytes.IndexByte(data[offset:], 0)
	if end < 0 {
		return "", false
	}
	return string(data[offset : uint64(offset)+uint64(end)]), true
}

func fileRange(data []byte, offset, size uint64) bool {
	return offset <= uint64(len(data)) &&
		size <= uint64(len(data))-offset
}
