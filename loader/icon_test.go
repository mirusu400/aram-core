package loader

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
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
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func iconMakeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func iconWriteTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
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
