package loader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MarkerLimit = 64

type Kind string

const (
	KindUnknown  Kind = "unknown"
	KindDAT      Kind = "wipi-dat"
	KindABHS     Kind = "abhs"
	KindEADS     Kind = "eads"
	KindELF      Kind = "elf"
	KindJava     Kind = "java-archive"
	KindKTF      Kind = "ktf-wipi"
	KindRaptor   Kind = "raptor-wipi-c"
	KindWBIN     Kind = "samsung-wbin"
	KindWBT      Kind = "samsung-wbt"
	KindFont     Kind = "samsung-font"
	KindFirmware Kind = "firmware-image"
)

type Marker struct {
	Magic  string `json:"magic"`
	Offset int64  `json:"offset"`
}

type OffsetError struct {
	Offset int64
	Op     string
	Err    error
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("%s at offset 0x%x: %v", e.Op, e.Offset, e.Err)
}

func (e *OffsetError) Unwrap() error {
	return e.Err
}

type Report struct {
	Path    string   `json:"path"`
	Size    int64    `json:"size"`
	SHA256  string   `json:"sha256"`
	Kind    Kind     `json:"kind"`
	Markers []Marker `json:"markers,omitempty"`
}

func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func InspectFile(path string) (Report, error) {
	file, err := os.Open(path)
	if err != nil {
		return Report{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Report{}, fmt.Errorf("stat %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return Report{}, fmt.Errorf("%q is not a regular file", path)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	report, err := Inspect(absolute, file, info.Size())
	if err != nil {
		return Report{}, fmt.Errorf("inspect %q: %w", path, err)
	}
	return report, nil
}

func InspectBytes(name string, data []byte) (Report, error) {
	return Inspect(name, bytes.NewReader(data), int64(len(data)))
}

// Inspect identifies and hashes a bounded byte source without assuming that it
// is backed by a normal filesystem path. It reads exactly size bytes.
func Inspect(name string, reader io.ReaderAt, size int64) (Report, error) {
	if reader == nil {
		return Report{}, fmt.Errorf("inspect: reader is nil")
	}
	if size < 0 {
		return Report{}, fmt.Errorf("inspect: negative size %d", size)
	}

	hash := sha256.New()
	var (
		first   []byte
		carry   []byte
		offset  int64
		markers []Marker
	)
	buffer := make([]byte, 1024*1024)
	for offset < size {
		want := min(int64(len(buffer)), size-offset)
		count, readErr := reader.ReadAt(buffer[:want], offset)
		if count > 0 {
			chunk := append(append([]byte(nil), carry...), buffer[:count]...)
			if _, err := hash.Write(buffer[:count]); err != nil {
				return Report{}, &OffsetError{Offset: offset, Op: "hash", Err: err}
			}
			if len(first) < 64 {
				needed := min(64-len(first), count)
				first = append(first, buffer[:needed]...)
			}
			base := offset - int64(len(carry))
			markers = appendMarkers(markers, chunk, base)
			if len(chunk) > 3 {
				carry = append(carry[:0], chunk[len(chunk)-3:]...)
			} else {
				carry = append(carry[:0], chunk...)
			}
			offset += int64(count)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && offset == size {
				break
			}
			if errors.Is(readErr, io.EOF) {
				readErr = io.ErrUnexpectedEOF
			}
			return Report{}, &OffsetError{Offset: offset, Op: "read", Err: readErr}
		}
		if count == 0 {
			return Report{}, &OffsetError{Offset: offset, Op: "read", Err: io.ErrNoProgress}
		}
	}

	return Report{
		Path:    name,
		Size:    size,
		SHA256:  hex.EncodeToString(hash.Sum(nil)),
		Kind:    detectKind(name, first, markers),
		Markers: markers,
	}, nil
}

func appendMarkers(markers []Marker, data []byte, base int64) []Marker {
	if len(markers) >= MarkerLimit {
		return markers
	}
	for position := 0; position+4 <= len(data) && len(markers) < MarkerLimit; position++ {
		var magic string
		switch string(data[position : position+4]) {
		case "ABHS":
			magic = "ABHS"
		case "EADS":
			magic = "EADS"
		default:
			continue
		}
		absolute := base + int64(position)
		if absolute >= 0 && !hasMarker(markers, magic, absolute) {
			markers = append(markers, Marker{Magic: magic, Offset: absolute})
		}
	}
	return markers
}

func hasMarker(markers []Marker, magic string, offset int64) bool {
	for _, marker := range markers {
		if marker.Magic == magic && marker.Offset == offset {
			return true
		}
	}
	return false
}

func detectKind(path string, first []byte, markers []Marker) Kind {
	switch {
	case bytes.HasPrefix(first, []byte("ABHS")):
		return KindABHS
	case bytes.HasPrefix(first, []byte("EADS")):
		return KindEADS
	case bytes.HasPrefix(first, []byte{0x7f, 'E', 'L', 'F'}):
		return KindELF
	case bytes.HasPrefix(first, []byte{'P', 'K', 3, 4}):
		return KindJava
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".dat":
		return KindDAT
	case ".wbin":
		return KindWBIN
	case ".wbt":
		return KindWBT
	case ".fnt":
		return KindFont
	case ".jar":
		return KindJava
	case ".bin", ".rom", ".img", ".mbn":
		return KindFirmware
	}
	for _, marker := range markers {
		switch marker.Magic {
		case "ABHS":
			return KindABHS
		case "EADS":
			return KindEADS
		}
	}
	return KindUnknown
}
