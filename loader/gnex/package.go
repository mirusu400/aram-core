// Package gnex parses SK Telecom "GNEX" distribution archives: SinjiSoft
// GVM titles shipped as a basename-matched pair of a small binary
// descriptor (a newer ".mod" installer record, or an older ".inf" record)
// and an ".SGS"/".sgs" ("Sinji Game Script") payload. It also recognizes
// standalone SGS payloads, raw or in a ZIP, using a stricter header check.
//
// This package recognizes a GNEX archive and decodes the payload's fixed
// header (format version, title). It does not decode the GVM bytecode or
// resource body that follows the header - that format has not been reverse
// engineered. See docs/gnex-format.md for what is and is not known.
package gnex

import (
	"archive/zip"
	"bytes"
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
	MaxMemberSize     = uint64(64 << 20)
	MaxExpandedSize   = uint64(128 << 20)
)

// ErrNotPackage is returned when the input is not a recognizable GNEX
// distribution ZIP or a standalone payload with a recognized SGS header.
var ErrNotPackage = errors.New("not a GNEX package")

// ManifestKind names which of the two observed descriptor shapes a package's
// manifest member is. Both are undecoded here beyond confirming they exist
// alongside the .SGS payload; see docs/gnex-format.md.
type ManifestKind string

const (
	ManifestMOD ManifestKind = "mod"
	ManifestINF ManifestKind = "inf"
	// ManifestNone identifies a standalone SGS, not a decoded descriptor.
	ManifestNone ManifestKind = ""
)

// Package is a recognized GNEX distribution.
type Package struct {
	// BaseName is the SGS archive member stem, shared with its manifest when
	// present. It is empty for raw standalone input whose name is not known.
	BaseName string
	// SGSName and ManifestName are the archive member names actually
	// matched (case as stored in the ZIP).
	SGSName      string
	ManifestName string
	ManifestKind ManifestKind
	// Header is the decoded SGS payload header (format version, title).
	Header Header
	// SGS is the complete raw .SGS/.sgs payload, header included. Bytes
	// from Header.BodyOffset onward are the undecoded GVM body.
	SGS []byte
	// Manifest is the complete raw manifest (.mod or .inf) bytes, or nil for
	// a standalone SGS.
	Manifest []byte
	// Files holds every archive member, for callers that want the
	// remaining files (e.g. a .wmr resource bundle, a .res icon, save
	// data) that this package does not otherwise interpret.
	Files map[string][]byte
}

// FormatError reports an invalid or ambiguous paired GNEX archive, an unsafe
// ZIP, or an oversized recognized raw SGS. An extension alone does not prove
// GNEX identity. ErrNotPackage means no supported recognition shape was found.
type FormatError struct {
	Path   string
	Offset int64
	Reason string
}

func (e *FormatError) Error() string {
	location := e.Path
	if location == "" {
		location = "archive"
	}
	if e.Offset >= 0 {
		return fmt.Sprintf("GNEX package %q at offset 0x%x: %s", location, e.Offset, e.Reason)
	}
	return fmt.Sprintf("GNEX package %q: %s", location, e.Reason)
}

func formatError(name string, offset int64, reason string) error {
	return &FormatError{Path: name, Offset: offset, Reason: reason}
}

// Inspect recognizes a GNEX distribution ZIP or raw standalone SGS. Standalone
// candidates require an observed header placement and a nonempty, opaque body.
// Raw payloads have no file name or descriptor. Recognition never validates or
// executes the bytecode body.
func Inspect(data []byte) (Package, error) {
	if !hasZIPMagic(data) {
		header, err := standaloneHeader(data)
		if err != nil {
			return Package{}, ErrNotPackage
		}
		if uint64(len(data)) > MaxMemberSize {
			return Package{}, formatError("SGS", 0, "payload exceeds size limit")
		}
		return Package{Header: header, SGS: bytes.Clone(data)}, nil
	}
	files, err := readZIP(data, "archive")
	if err != nil {
		return Package{}, err
	}

	type candidate struct {
		base         string
		sgsName      string
		manifestName string
		manifestKind ManifestKind
	}
	var found []candidate
	for name := range files {
		ext := path.Ext(name)
		if !strings.EqualFold(ext, ".sgs") {
			continue
		}
		base := strings.TrimSuffix(name, ext)
		if modName, ok := findCaseInsensitive(files, base+".mod"); ok {
			found = append(found, candidate{base, name, modName, ManifestMOD})
			continue
		}
		if infName, ok := findCaseInsensitive(files, base+".inf"); ok {
			found = append(found, candidate{base, name, infName, ManifestINF})
			continue
		}
		if _, err := standaloneHeader(files[name]); err == nil {
			found = append(found, candidate{base, name, "", ManifestNone})
		}
	}
	if len(found) == 0 {
		return Package{}, ErrNotPackage
	}
	if len(found) != 1 {
		return Package{}, formatError("archive", -1, "multiple GNEX application candidates")
	}
	selected := found[0]
	sgs := files[selected.sgsName]
	header, err := ParseHeader(sgs)
	if err != nil {
		return Package{}, formatError(selected.sgsName, -1, "SGS header: "+err.Error())
	}

	return Package{
		BaseName:     selected.base,
		SGSName:      selected.sgsName,
		ManifestName: selected.manifestName,
		ManifestKind: selected.manifestKind,
		Header:       header,
		SGS:          sgs,
		Manifest:     files[selected.manifestName],
		Files:        files,
	}, nil
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

func hasZIPMagic(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) ||
		bytes.Equal(data[:4], []byte{'P', 'K', 5, 6}) ||
		bytes.Equal(data[:4], []byte{'P', 'K', 7, 8})
}
