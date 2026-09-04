package loader

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"golang.org/x/image/bmp"

	"github.com/mirusu400/aram-core/loader/ktf"
	"github.com/mirusu400/aram-core/loader/raptor"
	"github.com/mirusu400/aram-core/loader/skvm"
)

// ErrNoIcon reports that a package format carries no embedded application icon
// (raw WIPI .dat/EADS/ABHS code containers and firmware images hold only code).
// Callers fall back to a generated placeholder.
var ErrNoIcon = errors.New("loader: package has no embedded icon")

const (
	// maxIconSourceBytes bounds how large a package Icon will read from disk.
	maxIconSourceBytes = int64(256 << 20)
	// maxIconResourceBytes bounds a single candidate icon resource.
	maxIconResourceBytes = 4 << 20
	// maxIconDimension rejects an implausibly large decoded icon.
	maxIconDimension = 512
)

// Icon returns a PNG-encoded application icon for the package at path, or
// ErrNoIcon when the format has none. It never instantiates a machine and
// imports no GUI code, so it is safe to call from the headless loader.
func Icon(path string) ([]byte, error) {
	report, err := InspectFile(path)
	if err != nil {
		return nil, err
	}
	// Only ZIP-based packages (KTF WIPI, Raptor WIPI-C, SK-VM MIDP, and raw
	// MIDlet jars) can embed an icon. InspectFile classifies every ZIP as
	// KindJava; the concrete format is resolved by trying each loader below.
	// Raw .dat/EADS/ABHS containers and firmware images hold only code.
	if report.Kind != KindJava {
		return nil, ErrNoIcon
	}
	data, err := readBoundedFile(path, maxIconSourceBytes)
	if err != nil {
		return nil, err
	}
	resources, hint := packageIconSource(data)
	if len(resources) == 0 {
		return nil, ErrNoIcon
	}
	raw, ok := selectIconResource(resources, hint)
	if !ok {
		return nil, ErrNoIcon
	}
	return normalizeIconPNG(raw)
}

// packageIconSource returns the package's non-code resources plus an optional
// descriptor-declared icon path, trying each ZIP-based format in turn. Each
// loader reports ErrNotPackage for archives that are not its format, so the
// order is a safe cascade ending at a plain MIDlet jar.
func packageIconSource(data []byte) (map[string][]byte, string) {
	if pkg, err := ktf.Inspect(data); err == nil {
		return pkg.Resources, ""
	}
	if pkg, err := raptor.Inspect(data); err == nil {
		return pkg.Resources, ""
	}
	if pkg, err := skvm.Inspect(data); err == nil {
		hint := pkg.Descriptor.Raw["MIDlet-Icon"]
		if hint == "" {
			hint = midletIconField(pkg.Descriptor.Raw["MIDlet-1"])
		}
		return pkg.Resources, hint
	}
	return jarResources(data)
}

// selectIconResource picks the icon bytes from a resource map: the declared
// hint first, then common icon filenames, then any image resource (chosen
// deterministically).
func selectIconResource(resources map[string][]byte, hint string) ([]byte, bool) {
	for _, key := range candidateKeys(hint) {
		if data, ok := resources[key]; ok && looksLikeImage(data) {
			return data, true
		}
	}
	for _, name := range []string{"icon.png", "r/icon.png", "res/icon.png", "icon.bmp"} {
		if data, ok := resources[name]; ok && looksLikeImage(data) {
			return data, true
		}
	}
	keys := make([]string, 0, len(resources))
	for key := range resources {
		if isNonIconSystemResource(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Prefer a resource whose name mentions "icon", then any image.
	for _, wantIcon := range []bool{true, false} {
		for _, key := range keys {
			if wantIcon && !strings.Contains(strings.ToLower(path.Base(key)), "icon") {
				continue
			}
			if looksLikeImage(resources[key]) {
				return resources[key], true
			}
		}
	}
	return nil, false
}

// isNonIconSystemResource reports whether name is known handset chrome bundled
// alongside a title's own art rather than its app icon. KTF/Raptor WIPI
// packages commonly ship an "Annunciator.png"-style resource: template art for
// the handset's own status bar (signal and battery glyphs, see
// application/internal/ktf/ktf_annunciator.go), not anything the title drew.
// Without this exclusion it is often the only resource selectIconResource can
// recognize as an image, so it wins the "any image" fallback and the launcher
// shows a strip of status-bar glyphs instead of a plain placeholder tile.
func isNonIconSystemResource(key string) bool {
	return strings.Contains(strings.ToLower(path.Base(key)), "annunciator")
}

// candidateKeys expands a declared icon path into the forms a resource map may
// key it under (with and without a leading slash, plus the bare base name).
func candidateKeys(hint string) []string {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return nil
	}
	trimmed := strings.TrimPrefix(hint, "/")
	keys := []string{trimmed, hint, path.Base(trimmed)}
	seen := make(map[string]struct{}, len(keys))
	unique := keys[:0]
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	return unique
}

// midletIconField returns the icon path from a "MIDlet-1: Name, Icon, Class"
// value (the middle field), or "" when absent.
func midletIconField(midlet1 string) string {
	parts := strings.Split(midlet1, ",")
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// jarResources reads a raw .jar's non-class members and any MIDlet-Icon the
// manifest declares.
func jarResources(data []byte) (map[string][]byte, string) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ""
	}
	resources := make(map[string][]byte)
	hint := ""
	var total int
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(file.Name), ".class") {
			continue
		}
		if file.UncompressedSize64 > maxIconResourceBytes {
			continue
		}
		payload, err := readZipEntry(file)
		if err != nil {
			continue
		}
		total += len(payload)
		if total > 64<<20 {
			break
		}
		resources[file.Name] = payload
		if strings.EqualFold(path.Base(file.Name), "MANIFEST.MF") {
			hint = midletIconFromManifest(payload)
		}
	}
	return resources, hint
}

func readZipEntry(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxIconResourceBytes))
}

// midletIconFromManifest parses MIDlet-Icon (or the MIDlet-1 middle field) from
// a JAR manifest.
func midletIconFromManifest(manifest []byte) string {
	for _, line := range strings.Split(string(manifest), "\n") {
		line = strings.TrimRight(line, "\r")
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if strings.EqualFold(key, "MIDlet-Icon") {
			return value
		}
		if strings.EqualFold(key, "MIDlet-1") {
			if icon := midletIconField(value); icon != "" {
				return icon
			}
		}
	}
	return ""
}

// looksLikeImage reports whether data begins with a PNG or BMP signature.
func looksLikeImage(data []byte) bool {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return true
	}
	if len(data) >= 2 && data[0] == 'B' && data[1] == 'M' {
		return true
	}
	return false
}

// normalizeIconPNG decodes a PNG or BMP icon and re-encodes it as PNG so callers
// only ever handle PNG.
func normalizeIconPNG(raw []byte) ([]byte, error) {
	img, err := decodeIconImage(raw)
	if err != nil {
		return nil, fmt.Errorf("loader: decode icon: %w", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 ||
		bounds.Dx() > maxIconDimension || bounds.Dy() > maxIconDimension {
		return nil, fmt.Errorf("loader: icon size %dx%d out of range", bounds.Dx(), bounds.Dy())
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return nil, fmt.Errorf("loader: encode icon: %w", err)
	}
	return buffer.Bytes(), nil
}

func decodeIconImage(raw []byte) (image.Image, error) {
	if img, err := png.Decode(bytes.NewReader(raw)); err == nil {
		return img, nil
	}
	return bmp.Decode(bytes.NewReader(raw))
}

func readBoundedFile(name string, limit int64) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("loader: %q is too large to scan for an icon (%d bytes)", name, info.Size())
	}
	return os.ReadFile(name)
}
