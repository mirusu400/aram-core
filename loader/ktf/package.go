// Package ktf parses the carrier archive format used by KTF WIPI
// applications. A distributable package is a ZIP containing an __adf__
// descriptor and an AID-named JAR. The JAR contains resources plus a raw
// client.bin{bss_size} ARM image.
package ktf

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"strconv"
	"strings"

	"github.com/mirusu400/aram-core/loader/internal/zipname"
)

const (
	MaxArchiveEntries = 10_000
	MaxMemberSize     = uint64(256 << 20)
	MaxExpandedSize   = uint64(512 << 20)
	MaxBSSSize        = uint64(256 << 20)
)

var ErrNotPackage = errors.New("not a KTF WIPI package")

type Descriptor struct {
	AID       string
	PID       string
	MainClass string

	// DisplayWidth and DisplayHeight carry the handset screen the title was
	// built for, from the descriptor's "DisplaySize:W*H" line. Titles that
	// omit it leave both zero.
	DisplayWidth  int
	DisplayHeight int
}

type Package struct {
	Descriptor Descriptor
	JARName    string
	ClientName string
	BSSSize    uint32
	Client     []byte
	Files      map[string][]byte
	Resources  map[string][]byte
	Warnings   []string
}

type FormatError struct {
	Path   string
	Reason string
}

func (e *FormatError) Error() string {
	if e.Path == "" {
		return "KTF package: " + e.Reason
	}
	return fmt.Sprintf("KTF package %q: %s", e.Path, e.Reason)
}

// Inspect validates and expands a KTF distribution ZIP. All paths and sizes
// are checked before the returned bytes are exposed to the application
// machine. Some installers wrap the package in one or more directories, so
// the descriptor and its AID-named JAR may live below the archive root. ZIP
// data without an unambiguous __adf__ marker is reported as ErrNotPackage so
// callers can continue format detection.
func Inspect(data []byte) (Package, error) {
	adfName, err := findDescriptorEntry(data)
	if err != nil {
		return Package{}, err
	}
	if adfName == "" {
		return Package{}, ErrNotPackage
	}
	files, _, err := readZIP(data, "archive", false)
	if err != nil {
		return Package{}, err
	}
	adf, ok := files[adfName]
	if !ok {
		return Package{}, &FormatError{Path: adfName, Reason: "descriptor is unreadable"}
	}
	descriptor, err := ParseDescriptor(adf)
	if err != nil {
		return Package{}, err
	}
	jarName := path.Join(path.Dir(adfName), descriptor.AID+".jar")
	if path.Dir(adfName) == "." {
		jarName = descriptor.AID + ".jar"
	}
	jar, ok := files[jarName]
	if !ok {
		return Package{}, &FormatError{
			Path:   jarName,
			Reason: "AID-named JAR is missing",
		}
	}
	if isOMADCF(jar) {
		jar, err = unwrapOMADCF(jar, jarName)
		if err != nil {
			return Package{}, err
		}
	}
	resources, warnings, err := readZIP(jar, jarName, true)
	if err != nil {
		return Package{}, err
	}

	clientName := ""
	var client []byte
	for name, payload := range resources {
		if !strings.HasPrefix(name, "client.bin") {
			continue
		}
		if clientName != "" {
			return Package{}, &FormatError{
				Path:   jarName,
				Reason: "multiple client.bin images",
			}
		}
		clientName = name
		client = payload
	}
	if clientName == "" {
		return Package{}, &FormatError{
			Path:   jarName,
			Reason: "client.bin{bss_size} is missing",
		}
	}
	bssSize, err := ParseBSSSize(clientName)
	if err != nil {
		return Package{}, err
	}
	if len(client) == 0 {
		return Package{}, &FormatError{
			Path:   clientName,
			Reason: "ARM image is empty",
		}
	}
	delete(resources, clientName)
	return Package{
		Descriptor: descriptor,
		JARName:    jarName,
		ClientName: clientName,
		BSSSize:    bssSize,
		Client:     client,
		Files:      files,
		Resources:  resources,
		Warnings:   warnings,
	}, nil
}

func ParseDescriptor(data []byte) (Descriptor, error) {
	var descriptor Descriptor
	for _, rawLine := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSuffix(rawLine, []byte{'\r'})
		switch {
		case bytes.HasPrefix(line, []byte("AID:")):
			descriptor.AID = string(line[4:])
		case bytes.HasPrefix(line, []byte("PID:")):
			descriptor.PID = string(line[4:])
		case bytes.HasPrefix(line, []byte("MClass:")):
			descriptor.MainClass = string(line[7:])
		case bytes.HasPrefix(line, []byte("DisplaySize:")):
			descriptor.DisplayWidth, descriptor.DisplayHeight = parseDisplaySize(
				string(line[12:]),
			)
		}
	}
	if err := validateDescriptorValue("AID", descriptor.AID, true); err != nil {
		return Descriptor{}, err
	}
	if err := validateDescriptorValue("PID", descriptor.PID, false); err != nil {
		return Descriptor{}, err
	}
	if err := validateDescriptorValue("MClass", descriptor.MainClass, false); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

// parseDisplaySize reads the "W*H" form KTF descriptors use. Anything that is
// not two positive in-range numbers is reported as absent rather than as an
// error, because the field is optional and a malformed one should not keep an
// otherwise loadable title from starting.
func parseDisplaySize(value string) (int, int) {
	const maxDisplayEdge = 4096
	width, height, ok := strings.Cut(strings.TrimSpace(value), "*")
	if !ok {
		return 0, 0
	}
	parsed := make([]int, 0, 2)
	for _, field := range []string{width, height} {
		number, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || number <= 0 || number > maxDisplayEdge {
			return 0, 0
		}
		parsed = append(parsed, number)
	}
	return parsed[0], parsed[1]
}

func ParseBSSSize(filename string) (uint32, error) {
	const prefix = "client.bin"
	if !strings.HasPrefix(filename, prefix) {
		return 0, &FormatError{
			Path:   filename,
			Reason: "client image name does not start with client.bin",
		}
	}
	suffix := strings.TrimPrefix(filename, prefix)
	if suffix == "" {
		return 0, &FormatError{
			Path:   filename,
			Reason: "client image name has no BSS size",
		}
	}
	size, err := strconv.ParseUint(suffix, 10, 32)
	if err != nil {
		return 0, &FormatError{
			Path:   filename,
			Reason: "client image BSS size is not a decimal uint32",
		}
	}
	if size > MaxBSSSize {
		return 0, &FormatError{
			Path:   filename,
			Reason: "client image BSS exceeds limit",
		}
	}
	return uint32(size), nil
}

func validateDescriptorValue(name, value string, required bool) error {
	if value == "" {
		if required {
			return &FormatError{Path: "__adf__", Reason: name + " is missing"}
		}
		return nil
	}
	if len(value) > 255 {
		return &FormatError{Path: "__adf__", Reason: name + " exceeds 255 bytes"}
	}
	for _, character := range []byte(value) {
		if character < 0x20 || character > 0x7e {
			return &FormatError{
				Path:   "__adf__",
				Reason: name + " is not printable ASCII",
			}
		}
	}
	if strings.ContainsAny(value, `/\`) || value == "." || value == ".." {
		return &FormatError{
			Path:   "__adf__",
			Reason: name + " is not a plain identifier",
		}
	}
	return nil
}

func findDescriptorEntry(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", &FormatError{Path: "archive", Reason: "invalid ZIP: " + err.Error()}
	}
	var found string
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name, ok := zipname.SafeName(entry.Name)
		if !ok || path.Base(name) != "__adf__" {
			continue
		}
		if found != "" && found != name {
			return "", &FormatError{
				Path:   "archive",
				Reason: "multiple __adf__ descriptors",
			}
		}
		found = name
	}
	return found, nil
}

func readZIP(data []byte, label string, tolerateResourceErrors bool) (map[string][]byte, []string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, &FormatError{Path: label, Reason: "invalid ZIP: " + err.Error()}
	}
	if len(reader.File) > MaxArchiveEntries {
		return nil, nil, &FormatError{
			Path:   label,
			Reason: fmt.Sprintf("contains more than %d entries", MaxArchiveEntries),
		}
	}
	files := make(map[string][]byte, len(reader.File))
	seen := make(map[string]string, len(reader.File))
	var warnings []string
	var expanded uint64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.FileInfo().Mode()&fs.ModeSymlink != 0 {
			return nil, nil, &FormatError{Path: entry.Name, Reason: "symbolic link is not allowed"}
		}
		name, ok := zipname.SafeName(entry.Name)
		if !ok {
			return nil, nil, &FormatError{Path: entry.Name, Reason: "unsafe member path"}
		}
		key := strings.ToLower(name)
		if previous, duplicate := seen[key]; duplicate {
			if previous == name {
				continue
			}
			return nil, nil, &FormatError{
				Path:   name,
				Reason: fmt.Sprintf("duplicates %q by case", previous),
			}
		}
		seen[key] = name
		size := entry.UncompressedSize64
		if size > MaxMemberSize {
			return nil, nil, &FormatError{Path: name, Reason: "member exceeds size limit"}
		}
		if expanded > math.MaxUint64-size || expanded+size > MaxExpandedSize {
			return nil, nil, &FormatError{Path: label, Reason: "expanded data exceeds limit"}
		}
		expanded += size

		stream, err := entry.Open()
		if err != nil {
			if tolerateResourceErrors && !strings.HasPrefix(name, "client.bin") {
				warnings = append(warnings, fmt.Sprintf("%s: open member: %v", name, err))
				continue
			}
			return nil, nil, &FormatError{Path: name, Reason: "open member: " + err.Error()}
		}
		payload, readErr := io.ReadAll(io.LimitReader(stream, int64(size)+1))
		closeErr := stream.Close()
		if readErr != nil {
			if tolerateResourceErrors &&
				!strings.HasPrefix(name, "client.bin") &&
				errors.Is(readErr, zip.ErrChecksum) &&
				uint64(len(payload)) == size {
				warnings = append(
					warnings,
					name+": checksum mismatch; retained complete payload",
				)
			} else if tolerateResourceErrors &&
				!strings.HasPrefix(name, "client.bin") {
				warnings = append(warnings, fmt.Sprintf("%s: read member: %v", name, readErr))
				continue
			} else {
				return nil, nil, &FormatError{Path: name, Reason: "read member: " + readErr.Error()}
			}
		}
		if closeErr != nil {
			if tolerateResourceErrors && !strings.HasPrefix(name, "client.bin") {
				warnings = append(warnings, fmt.Sprintf("%s: close member: %v", name, closeErr))
				continue
			}
			return nil, nil, &FormatError{Path: name, Reason: "close member: " + closeErr.Error()}
		}
		if uint64(len(payload)) != size {
			if tolerateResourceErrors && !strings.HasPrefix(name, "client.bin") {
				warnings = append(warnings, name+": uncompressed size mismatch")
				continue
			}
			return nil, nil, &FormatError{Path: name, Reason: "uncompressed size mismatch"}
		}
		files[name] = payload
	}
	return files, warnings, nil
}
