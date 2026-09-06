package loader

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func iconTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 8), G: uint8(y * 8), B: 0x40, A: 0xff})
		}
	}
	var buffer bytes.Buffer
	check(t, png.Encode(&buffer, img))
	return buffer.Bytes()
}

func iconMakeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		entry, err := writer.Create(name)
		check(t, err)
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	check(t, writer.Close())
	return buffer.Bytes()
}

func iconWriteTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	check(t, os.WriteFile(path, data, 0o644))
	return path
}

func iconPNGSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("returned icon is not valid PNG: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestIconExtractsKTFPackageResource(t *testing.T) {
	jar := iconMakeZip(t, map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
		"client.bin4096":       {0x04, 0xe0, 0x70, 0x47},
		"r/icon.png":           iconTestPNG(t, 16, 16),
	})
	archive := iconMakeZip(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:PD000001\r\nAID:01020304\r\nMClass:GameMain\r\n"),
	})
	// .dat extension but a real ZIP: detected as KindJava, resolved to KTF.
	path := iconWriteTemp(t, "game.dat", archive)

	data, err := Icon(path)
	if err != nil {
		t.Fatalf("Icon(KTF) error: %v", err)
	}
	if w, h := iconPNGSize(t, data); w != 16 || h != 16 {
		t.Fatalf("KTF icon = %dx%d, want 16x16", w, h)
	}
}

func TestIconRawJarPrefersManifestIcon(t *testing.T) {
	jar := iconMakeZip(t, map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\r\nMIDlet-Icon: /app.png\r\n"),
		"Game.class":           {0xca, 0xfe, 0xba, 0xbe},
		"app.png":              iconTestPNG(t, 12, 12),
		"aaa_decoy.png":        iconTestPNG(t, 4, 4), // sorts first; must be ignored
	})
	path := iconWriteTemp(t, "game.jar", jar)

	data, err := Icon(path)
	if err != nil {
		t.Fatalf("Icon(jar) error: %v", err)
	}
	if w, h := iconPNGSize(t, data); w != 12 || h != 12 {
		t.Fatalf("jar icon = %dx%d, want 12x12 (manifest MIDlet-Icon should win over the decoy)", w, h)
	}
}

func TestIconNoIconForRawDat(t *testing.T) {
	// A non-ZIP .dat is a raw WIPI code container: KindDAT, no embedded icon.
	path := iconWriteTemp(t, "builtin.dat", []byte{0x00, 0xb5, 0x01, 0x02, 0x03})
	if _, err := Icon(path); !errors.Is(err, ErrNoIcon) {
		t.Fatalf("Icon(.dat) error = %v, want ErrNoIcon", err)
	}
}

func TestIconSkipsAnnunciatorArt(t *testing.T) {
	// Real KTF titles often bundle "Annunciator.png": template art for the
	// handset's own status bar, not the title's icon (see
	// application/internal/ktf/ktf_annunciator.go). When it is the only
	// recognizable image in the package, Icon must report ErrNoIcon rather
	// than hand back a strip of signal/battery glyphs as if it were the icon.
	jar := iconMakeZip(t, map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
		"client.bin4096":       {0x04, 0xe0, 0x70, 0x47},
		"Annunciator.png":      iconTestPNG(t, 58, 9),
	})
	archive := iconMakeZip(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:PD000001\r\nAID:01020304\r\nMClass:GameMain\r\n"),
	})
	path := iconWriteTemp(t, "game.dat", archive)

	if _, err := Icon(path); !errors.Is(err, ErrNoIcon) {
		t.Fatalf("Icon(annunciator-only) error = %v, want ErrNoIcon", err)
	}
}

func TestIconPrefersRealIconOverAnnunciatorArt(t *testing.T) {
	jar := iconMakeZip(t, map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
		"client.bin4096":       {0x04, 0xe0, 0x70, 0x47},
		"Annunciator.png":      iconTestPNG(t, 58, 9),
		"r/icon.png":           iconTestPNG(t, 16, 16),
	})
	archive := iconMakeZip(t, map[string][]byte{
		"01020304.jar": jar,
		"__adf__":      []byte("PID:PD000001\r\nAID:01020304\r\nMClass:GameMain\r\n"),
	})
	path := iconWriteTemp(t, "game.dat", archive)

	data, err := Icon(path)
	if err != nil {
		t.Fatalf("Icon(icon+annunciator) error: %v", err)
	}
	if w, h := iconPNGSize(t, data); w != 16 || h != 16 {
		t.Fatalf("icon = %dx%d, want 16x16 (the title's own icon, not the annunciator strip)", w, h)
	}
}

func TestIconNoIconWhenArchiveHasNoImage(t *testing.T) {
	jar := iconMakeZip(t, map[string][]byte{
		"Game.class": {0xca, 0xfe, 0xba, 0xbe},
		"data.txt":   []byte("no image here"),
	})
	path := iconWriteTemp(t, "noicon.jar", jar)
	if _, err := Icon(path); !errors.Is(err, ErrNoIcon) {
		t.Fatalf("Icon(no image) error = %v, want ErrNoIcon", err)
	}
}

// iconTestGIF encodes a checkered GIF of the given size.
func iconTestGIF(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{
		color.NRGBA{R: 0x20, G: 0x40, B: 0x80, A: 0xff},
		color.NRGBA{R: 0xf0, G: 0xf0, B: 0xf0, A: 0xff},
	})
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetColorIndex(x, y, uint8((x+y)%2))
		}
	}
	var buffer bytes.Buffer
	check(t, gif.Encode(&buffer, img, nil))
	return buffer.Bytes()
}

// iconKTFArchive wraps jarFiles as the AID-named JAR of a minimal KTF
// distribution ZIP, adding extra entries (launcher icons) beside it.
func iconKTFArchive(t *testing.T, jarFiles, extra map[string][]byte) []byte {
	t.Helper()
	files := map[string][]byte{
		"01020304.jar": iconMakeZip(t, jarFiles),
		"__adf__":      []byte("PID:PD000001\r\nAID:01020304\r\nMClass:GameMain\r\n"),
	}
	for name, data := range extra {
		files[name] = data
	}
	return iconMakeZip(t, files)
}

func iconMinimalKTFJar() map[string][]byte {
	return map[string][]byte{
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
		"client.bin4096":       {0x04, 0xe0, 0x70, 0x47},
	}
}

// TestIconPrefersKTFLauncherIcon covers the shipped KTF layout: the carrier's
// install-menu icons (big.icon / middle.icon / small.icon, PNG data despite
// the extension) sit in the distribution ZIP beside the JAR, while the JAR
// itself often carries only proprietary art. big.icon must win, even over an
// icon-named resource inside the JAR.
func TestIconPrefersKTFLauncherIcon(t *testing.T) {
	jar := iconMinimalKTFJar()
	jar["r/icon.png"] = iconTestPNG(t, 16, 16)
	jar["a.gif"] = iconTestGIF(t, 40, 40)
	archive := iconKTFArchive(t, jar, map[string][]byte{
		"big.icon":    iconTestPNG(t, 120, 110),
		"middle.icon": iconTestPNG(t, 20, 20),
		"small.icon":  iconTestPNG(t, 12, 12),
	})
	path := iconWriteTemp(t, "game.zip", archive)

	data, err := Icon(path)
	if err != nil {
		t.Fatalf("Icon(KTF launcher) error: %v", err)
	}
	if w, h := iconPNGSize(t, data); w != 120 || h != 110 {
		t.Fatalf("icon = %dx%d, want the 120x110 big.icon", w, h)
	}
}

// TestIconFindsLauncherIconBelowInstallerDirectories covers an installer
// dump (W/apps/<AID>/...) where the launcher icons sit next to a nested
// descriptor, and falls through to middle.icon when big.icon is corrupt.
func TestIconFindsLauncherIconBelowInstallerDirectories(t *testing.T) {
	archive := iconMakeZip(t, map[string][]byte{
		"W/apps/01020304/01020304.jar": iconMakeZip(t, iconMinimalKTFJar()),
		"W/apps/01020304/__adf__":      []byte("PID:PD000001\r\nAID:01020304\r\nMClass:GameMain\r\n"),
		"W/apps/01020304/big.icon":     append([]byte(pngSignature), "truncated"...),
		"W/apps/01020304/middle.icon":  iconTestPNG(t, 20, 20),
	})
	path := iconWriteTemp(t, "dump.zip", archive)

	data, err := Icon(path)
	if err != nil {
		t.Fatalf("Icon(nested launcher) error: %v", err)
	}
	if w, h := iconPNGSize(t, data); w != 20 || h != 20 {
		t.Fatalf("icon = %dx%d, want the 20x20 middle.icon after the corrupt big.icon", w, h)
	}
}

// TestIconGuessSkipsStripsAndFakeBitmaps keeps a guessed icon icon-shaped:
// a sprite strip, a tiny glyph, and a proprietary resource that merely starts
// with "BM" are all skipped in favour of the largest near-square image, and a
// GIF is an acceptable image.
func TestIconGuessSkipsStripsAndFakeBitmaps(t *testing.T) {
	jar := iconMinimalKTFJar()
	jar["a_strip.png"] = iconTestPNG(t, 16, 400)
	jar["b_glyph.png"] = iconTestPNG(t, 5, 7)
	jar["c_fake.img"] = []byte("BM not a bitmap at all")
	jar["d_title.gif"] = iconTestGIF(t, 96, 72)
	jar["e_small.png"] = iconTestPNG(t, 24, 24)
	path := iconWriteTemp(t, "game.zip", iconKTFArchive(t, jar, nil))

	data, err := Icon(path)
	if err != nil {
		t.Fatalf("Icon(guess) error: %v", err)
	}
	if w, h := iconPNGSize(t, data); w != 96 || h != 72 {
		t.Fatalf("icon = %dx%d, want the 96x72 GIF title art", w, h)
	}
}

// TestIconNoIconWhenOnlyStripsRemain reports ErrNoIcon rather than a strip
// when nothing icon-shaped exists and no launcher icon is shipped.
func TestIconNoIconWhenOnlyStripsRemain(t *testing.T) {
	jar := iconMinimalKTFJar()
	jar["font.png"] = iconTestPNG(t, 435, 10)
	path := iconWriteTemp(t, "game.zip", iconKTFArchive(t, jar, nil))
	if _, err := Icon(path); !errors.Is(err, ErrNoIcon) {
		t.Fatalf("Icon(strip only) error = %v, want ErrNoIcon", err)
	}
}
