package loader

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
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
	source := packageIconSource(data)
	img, ok := selectIcon(source)
	if !ok {
		return nil, ErrNoIcon
	}
	return encodeIconPNG(img)
}

// iconSource is everything an icon may be chosen from: the launcher icons the
// distribution ZIP ships beside the JAR, the JAR's own resources, and the icon
// path a MIDlet descriptor declares.
type iconSource struct {
	// launcher holds the carrier's install-menu icons in preference order
	// (big before middle before small). KTF ZIPs ship big.icon/middle.icon/
	// small.icon and LGT Raptor ZIPs big.png/middle.png/small.png next to
	// app_info; either is the icon the handset showed, so it always wins over
	// a guess from the JAR's art.
	launcher  [][]byte
	resources map[string][]byte
	hint      string
}

// packageIconSource collects the icon candidates of a ZIP-based package,
// trying each format in turn. Each loader reports ErrNotPackage for archives
// that are not its format, so the order is a safe cascade ending at a plain
// MIDlet jar.
func packageIconSource(data []byte) iconSource {
	if pkg, err := ktf.Inspect(data); err == nil {
		return iconSource{launcher: launcherIcons(pkg.Files), resources: pkg.Resources}
	}
	if pkg, err := raptor.Inspect(data); err == nil {
		return iconSource{launcher: launcherIcons(pkg.Files), resources: pkg.Resources}
	}
	if pkg, err := skvm.Inspect(data); err == nil {
		hint := pkg.Descriptor.Raw["MIDlet-Icon"]
		if hint == "" {
			hint = midletIconField(pkg.Descriptor.Raw["MIDlet-1"])
		}
		return iconSource{
			launcher:  launcherIcons(pkg.Files),
			resources: pkg.Resources,
			hint:      hint,
		}
	}
	resources, hint := jarResources(data)
	return iconSource{resources: resources, hint: hint}
}

// launcherIconNames are the carrier install-menu icon files, largest first.
// They sit beside the descriptor in the distribution ZIP, sometimes below an
// installer's wrapping directories, so they are matched by base name.
var launcherIconNames = []string{
	"big.icon", "big.png",
	"middle.icon", "middle.png",
	"small.icon", "small.png",
}

// launcherIcons returns the distribution ZIP's install-menu icons in
// preference order. A name that appears more than once (an installer dump
// with several apps) resolves to the shallowest path, then alphabetically.
func launcherIcons(files map[string][]byte) [][]byte {
	var icons [][]byte
	for _, want := range launcherIconNames {
		bestKey := ""
		for key := range files {
			if !strings.EqualFold(path.Base(key), want) {
				continue
			}
			if bestKey == "" || shallowerPath(key, bestKey) {
				bestKey = key
			}
		}
		if bestKey != "" && looksLikeImage(files[bestKey]) {
			icons = append(icons, files[bestKey])
		}
	}
	return icons
}

func shallowerPath(a, b string) bool {
	depthA, depthB := strings.Count(a, "/"), strings.Count(b, "/")
	if depthA != depthB {
		return depthA < depthB
	}
	return a < b
}

// selectIcon decodes the best available icon: a launcher icon, then the
// declared descriptor icon, then well-known icon file names, then a plausible
// image among the JAR's resources. A candidate that does not decode, or that
// is not icon-shaped, is skipped rather than failing the whole lookup, since
// titles routinely keep sprite strips or non-BMP ".bmp"-signed data next to
// their art.
func selectIcon(source iconSource) (image.Image, bool) {
	for _, raw := range source.launcher {
		if img, ok := decodeIconCandidate(raw, false); ok {
			return img, true
		}
	}
	for _, key := range candidateKeys(source.hint) {
		if raw, ok := source.resources[key]; ok {
			if img, ok := decodeIconCandidate(raw, false); ok {
				return img, true
			}
		}
	}
	for _, name := range []string{"icon.png", "r/icon.png", "res/icon.png", "icon.bmp"} {
		if raw, ok := source.resources[name]; ok {
			if img, ok := decodeIconCandidate(raw, false); ok {
				return img, true
			}
		}
	}
	return selectIconResource(source.resources)
}

// selectIconResource guesses an icon from a resource map when the package
// declares none: a resource whose name mentions "icon" wins, then the largest
// icon-shaped image (see decodeIconCandidate), ties broken by name so the
// choice is deterministic. Undecodable or oddly shaped images are skipped.
func selectIconResource(resources map[string][]byte) (image.Image, bool) {
	keys := make([]string, 0, len(resources))
	for key := range resources {
		if isNonIconSystemResource(key) || !looksLikeImage(resources[key]) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !strings.Contains(strings.ToLower(path.Base(key)), "icon") {
			continue
		}
		if img, ok := decodeIconCandidate(resources[key], true); ok {
			return img, true
		}
	}
	bestKey, bestArea := "", 0
	for _, key := range keys {
		config, err := decodeIconConfig(resources[key])
		if err != nil || !iconShaped(config.Width, config.Height, true) {
			continue
		}
		if area := config.Width * config.Height; area > bestArea {
			bestKey, bestArea = key, area
		}
	}
	if bestKey == "" {
		return nil, false
	}
	return decodeIconCandidate(resources[bestKey], true)
}

// minGuessedIconSize is the smallest edge a resource guessed to be an icon may
// have; anything smaller is a cursor, a bullet, or a font glyph.
const minGuessedIconSize = 8

// iconShaped reports whether a decoded size is plausible for an icon. A
// declared or launcher icon only has to fit the size cap; a guessed one must
// also be near square, which rules out sprite strips, fonts, and UI bars.
func iconShaped(width, height int, guessed bool) bool {
	if width <= 0 || height <= 0 || width > maxIconDimension || height > maxIconDimension {
		return false
	}
	if !guessed {
		return true
	}
	if width < minGuessedIconSize || height < minGuessedIconSize {
		return false
	}
	return width <= 2*height && height <= 2*width
}

// decodeIconCandidate decodes raw as an icon, reporting false for data that
// is not a decodable PNG, BMP, or GIF or whose size is not icon-shaped.
func decodeIconCandidate(raw []byte, guessed bool) (image.Image, bool) {
	config, err := decodeIconConfig(raw)
	if err != nil || !iconShaped(config.Width, config.Height, guessed) {
		return nil, false
	}
	img, err := decodeIconImage(raw)
	if err != nil {
		return nil, false
	}
	bounds := img.Bounds()
	if !iconShaped(bounds.Dx(), bounds.Dy(), guessed) {
		return nil, false
	}
	return img, true
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

// looksLikeImage reports whether data begins with a PNG, BMP, or GIF signature.
func looksLikeImage(data []byte) bool {
	if bytes.HasPrefix(data, pngSignature) {
		return true
	}
	if len(data) >= 2 && data[0] == 'B' && data[1] == 'M' {
		return true
	}
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return true
	}
	return false
}

// encodeIconPNG re-encodes a decoded icon as PNG so callers only ever handle
// PNG.
func encodeIconPNG(img image.Image) ([]byte, error) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return nil, fmt.Errorf("loader: encode icon: %w", err)
	}
	return buffer.Bytes(), nil
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// decodeIconConfig reads only the image header, which is enough to rank and
// reject candidates without decoding every resource in a package.
func decodeIconConfig(raw []byte) (image.Config, error) {
	switch {
	case bytes.HasPrefix(raw, pngSignature):
		return png.DecodeConfig(bytes.NewReader(raw))
	case bytes.HasPrefix(raw, []byte("GIF8")):
		return gif.DecodeConfig(bytes.NewReader(raw))
	default:
		return bmp.DecodeConfig(bytes.NewReader(raw))
	}
}

func decodeIconImage(raw []byte) (image.Image, error) {
	switch {
	case bytes.HasPrefix(raw, pngSignature):
		return png.Decode(bytes.NewReader(raw))
	case bytes.HasPrefix(raw, []byte("GIF8")):
		return gif.Decode(bytes.NewReader(raw))
	default:
		return bmp.Decode(bytes.NewReader(raw))
	}
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
