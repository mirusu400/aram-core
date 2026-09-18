// Package brew recognizes BREW distribution ZIPs. MOD images remain opaque:
// recognition does not validate an executable, map memory, or supply a BREW ABI.
package brew

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/loader/internal/zipname"
)

const (
	MaxArchiveEntries = 10_000
	MaxMemberSize     = uint64(64 << 20)
	MaxExpandedSize   = uint64(128 << 20)
	// MIFTag is the observed little-endian tag, not a claimed SDK version.
	MIFTag = uint32(0x00010011)
	// FormatOpaqueNative does not certify CPU type, entry point or loadability.
	FormatOpaqueNative = "opaque-native"
)

var ErrNotPackage = errors.New("not a BREW package")

// FormatError reports a malformed recognized container or an unsafe ZIP.
// Offset identifies a byte within Path when known, or is -1 for archive rules.
type FormatError struct {
	Path   string
	Offset int64
	Reason string
}

func (e *FormatError) Error() string {
	if e.Offset >= 0 {
		return fmt.Sprintf("BREW package %q at offset 0x%x: %s", e.Path, e.Offset, e.Reason)
	}
	return fmt.Sprintf("BREW package %q: %s", e.Path, e.Reason)
}

func invalid(name string, offset int64, reason string) error {
	return &FormatError{Path: name, Offset: offset, Reason: reason}
}

// Module describes an archive member, not a decoded native executable.
type Module struct {
	Name   string
	Size   uint64
	Format string
	// SignaturePresent means only that a same-stem .sig member exists.
	// No signature or protection scheme is validated or bypassed.
	SignaturePresent bool
}

// Metadata exposes the empirically checked MIF envelope. The indexed records,
// ClassIDs, app names and links to MOD members are not decoded. Names describe
// bounded regions only, not their unverified resource semantics.
type Metadata struct {
	Name        string
	Tag         uint32
	TableOffset uint32
	TableSize   uint32
	IndexOffset uint32
	IndexCount  uint32
	DataOffset  uint32
	DataSize    uint32
}

// Package is recognized container metadata. MIF and MOD names do not reliably
// match, so they are returned independently and sorted, never paired by guess.
type Package struct {
	Modules []Module
	MIFs    []Metadata
	Files   map[string][]byte
}

// Inspect accepts ZIPs containing at least one observed MIF envelope and a
// nonempty MOD member. MIF-only distributions return a precise incomplete-input
// error. Unknown MIF tags, raw MOD, and other formats are not guessed as BREW.
func Inspect(data []byte) (Package, error) {
	if len(data) < 4 || !(bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) || bytes.Equal(data[:4], []byte{'P', 'K', 5, 6}) || bytes.Equal(data[:4], []byte{'P', 'K', 7, 8})) {
		return Package{}, ErrNotPackage
	}
	files, err := readZIP(data)
	if err != nil {
		return Package{}, err
	}
	pkg, err := inspectFiles(files)
	if err == nil {
		return pkg, nil
	}
	var formatErr *FormatError
	if !errors.As(err, &formatErr) || formatErr.Reason != "MIF metadata present but MOD member is missing" {
		return Package{}, err
	}
	flattened, ok, splitErr := flattenSplitPackage(files)
	if splitErr != nil {
		return Package{}, splitErr
	}
	if !ok {
		return Package{}, err
	}
	return inspectFiles(flattened)
}

func inspectFiles(files map[string][]byte) (Package, error) {
	var names []string
	nameKeys := make(map[string]bool, len(files))
	known := false
	for name, payload := range files {
		names = append(names, name)
		nameKeys[strings.ToLower(name)] = true
		if strings.EqualFold(path.Ext(name), ".mif") && len(payload) >= 4 && binary.LittleEndian.Uint32(payload) == MIFTag {
			known = true
		}
	}
	if !known {
		return Package{}, ErrNotPackage
	}
	sort.Strings(names)
	pkg := Package{Files: files}
	for _, name := range names {
		payload := files[name]
		switch strings.ToLower(path.Ext(name)) {
		case ".mif":
			metadata, err := parseMIF(name, payload)
			if err != nil {
				return Package{}, err
			}
			pkg.MIFs = append(pkg.MIFs, metadata)
		case ".mod":
			if len(payload) == 0 {
				return Package{}, invalid(name, 0, "empty MOD member")
			}
			sig := strings.TrimSuffix(name, path.Ext(name)) + ".sig"
			present := nameKeys[strings.ToLower(sig)]
			pkg.Modules = append(pkg.Modules, Module{Name: name, Size: uint64(len(payload)), Format: FormatOpaqueNative, SignaturePresent: present})
		}
	}
	if len(pkg.Modules) == 0 {
		return Package{}, invalid("archive", -1, "MIF metadata present but MOD member is missing")
	}
	return pkg, nil
}

// flattenSplitPackage recognizes the observed KTF carrier layout where the
// outer ZIP contains the sole MIF and one nested ZIP contains the signed MOD
// and resources. It deliberately accepts only one level and one unambiguous
// application module. The nested ZIP itself is removed from the guest file
// namespace.
func flattenSplitPackage(outer map[string][]byte) (map[string][]byte, bool, error) {
	var nestedName string
	mifCount := 0
	for name := range outer {
		switch strings.ToLower(path.Ext(name)) {
		case ".mif":
			mifCount++
		case ".mod":
			return nil, false, nil
		case ".zip":
			if nestedName != "" {
				return nil, false, nil
			}
			nestedName = name
		}
	}
	if mifCount != 1 || nestedName == "" {
		return nil, false, nil
	}
	inner, err := readZIP(outer[nestedName])
	if err != nil {
		return nil, false, invalid(nestedName, -1, "invalid nested ZIP: "+err.Error())
	}
	innerModules := 0
	innerSignatures := make(map[string]bool)
	for name := range inner {
		ext := strings.ToLower(path.Ext(name))
		if ext == ".mif" || ext == ".zip" {
			return nil, false, nil
		}
		if ext == ".mod" {
			innerModules++
		}
		if ext == ".sig" {
			innerSignatures[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))] = true
		}
	}
	if innerModules != 1 {
		return nil, false, nil
	}
	for name := range inner {
		if strings.EqualFold(path.Ext(name), ".mod") && !innerSignatures[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))] {
			return nil, false, nil
		}
	}

	flattened := make(map[string][]byte, len(outer)+len(inner)-1)
	keys := make(map[string]bool, len(outer)+len(inner))
	var expanded uint64
	add := func(name string, payload []byte) error {
		key := strings.ToLower(name)
		if keys[key] {
			return invalid(name, -1, "split package member collides by case")
		}
		if uint64(len(payload)) > MaxExpandedSize-expanded {
			return invalid("archive", -1, "split package expanded data exceeds limit")
		}
		keys[key] = true
		expanded += uint64(len(payload))
		flattened[name] = payload
		return nil
	}
	for name, payload := range outer {
		if name == nestedName {
			continue
		}
		if err := add(name, payload); err != nil {
			return nil, false, err
		}
	}
	for name, payload := range inner {
		if err := add(name, payload); err != nil {
			return nil, false, err
		}
	}
	if len(flattened) > MaxArchiveEntries {
		return nil, false, invalid("archive", -1, "split package has too many entries")
	}
	return flattened, true, nil
}

func parseMIF(name string, data []byte) (Metadata, error) {
	if len(data) < 32 {
		return Metadata{}, invalid(name, int64(len(data)), "truncated MIF envelope")
	}
	word := func(offset int) uint32 { return binary.LittleEndian.Uint32(data[offset : offset+4]) }
	m := Metadata{Name: name, Tag: word(0), TableOffset: word(8), TableSize: word(12), IndexOffset: word(16), IndexCount: word(20), DataOffset: word(24), DataSize: word(28)}
	if m.Tag != MIFTag {
		return Metadata{}, invalid(name, 0, "unrecognized MIF tag")
	}
	// Use widened arithmetic for all untrusted offsets and counts. These
	// ordering/bounds relationships held for all 362 inspected MIF entries.
	if m.TableOffset < 32 || uint64(m.TableOffset)+uint64(m.TableSize) > uint64(len(data)) {
		return Metadata{}, invalid(name, 8, "MIF table span outside member")
	}
	if uint64(m.IndexOffset) < uint64(m.TableOffset)+uint64(m.TableSize) || uint64(m.IndexOffset)+(uint64(m.IndexCount)+1)*4 > uint64(len(data)) {
		return Metadata{}, invalid(name, 16, "MIF index span outside member or overlaps table")
	}
	if uint64(m.DataOffset) < uint64(m.IndexOffset)+(uint64(m.IndexCount)+1)*4 || uint64(m.DataOffset)+uint64(m.DataSize) > uint64(len(data)) {
		return Metadata{}, invalid(name, 24, "MIF data span outside member or overlaps index")
	}
	return m, nil
}

func readZIP(data []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("archive", 0, "invalid ZIP: "+err.Error())
	}
	if len(reader.File) > MaxArchiveEntries {
		return nil, invalid("archive", -1, "too many ZIP entries")
	}
	type member struct {
		entry *zip.File
		name  string
	}
	var members []member
	seen := make(map[string]bool, len(reader.File))
	var expanded uint64
	for _, entry := range reader.File {
		if entry.FileInfo().Mode()&fs.ModeSymlink != 0 {
			return nil, invalid(entry.Name, -1, "symbolic link is not allowed")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		name, safe := zipname.SafeName(entry.Name)
		if !safe {
			return nil, invalid(entry.Name, -1, "unsafe member path")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, invalid(name, -1, "duplicate member name by case")
		}
		seen[key] = true
		size := entry.UncompressedSize64
		if size > MaxMemberSize {
			return nil, invalid(name, -1, "member exceeds size limit")
		}
		if size > MaxExpandedSize-expanded {
			return nil, invalid("archive", -1, "expanded data exceeds limit")
		}
		expanded += size
		members = append(members, member{entry, name})
	}
	// Validate every declared member budget and path before allocating payloads.
	files := make(map[string][]byte, len(members))
	for _, member := range members {
		entry, name := member.entry, member.name
		size := entry.UncompressedSize64
		stream, err := entry.Open()
		if err != nil {
			return nil, invalid(name, -1, "open member: "+err.Error())
		}
		payload, readErr := io.ReadAll(io.LimitReader(stream, int64(size)+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, invalid(name, -1, "read member: "+readErr.Error())
		}
		if closeErr != nil {
			return nil, invalid(name, -1, "close member: "+closeErr.Error())
		}
		if uint64(len(payload)) != size {
			return nil, invalid(name, -1, "uncompressed size mismatch")
		}
		files[name] = payload
	}
	return files, nil
}
