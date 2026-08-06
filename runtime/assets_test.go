package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"reflect"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	pixels.SetNRGBA(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	pixels.SetNRGBA(1, 0, color.NRGBA{G: 0xff, A: 0x80})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, pixels); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestAssetsDecodeCacheAndStateRoundTrip(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := services.Assets.Decode(7, testPNG(t), DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := services.Assets.Decode(7, testPNG(t), DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("cached asset IDs = %s and %s", first, second)
	}
	info, err := services.Assets.Info(7, first)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 2 || info.Height != 1 || len(info.Frames) != 1 ||
		info.MediaType != "image/png" {
		t.Fatalf("decoded asset = %+v", info)
	}
	pixels, err := services.Graphics.RGBA(7, info.Frames[0].Surface)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pixels, []byte{0xff, 0, 0, 0xff, 0, 0xff, 0, 0x80}) {
		t.Fatalf("decoded pixels = %v", pixels)
	}

	state := services.Snapshot()
	clone, err := NewServices(state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Assets.Snapshot(), services.Assets.Snapshot()) {
		t.Fatal("asset state did not round-trip")
	}
}

func TestAssetsRejectGIFFrameLimitBeforeDecode(t *testing.T) {
	var encoded bytes.Buffer
	animation := gif.GIF{
		Image: []*image.Paletted{
			image.NewPaletted(
				image.Rect(0, 0, 1, 1),
				color.Palette{color.Black, color.White},
			),
			image.NewPaletted(
				image.Rect(0, 0, 1, 1),
				color.Palette{color.Black, color.White},
			),
		},
		Delay: []int{1, 1},
	}
	if err := gif.EncodeAll(&encoded, &animation); err != nil {
		t.Fatal(err)
	}
	limits := DefaultAssetLimits()
	limits.MaxFrames = 1
	if _, _, _, _, err := decodeImageAsset(
		encoded.Bytes(),
		DecodeOptions{MediaType: "image/gif"},
		limits,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("two-frame GIF error = %v", err)
	}
}

func TestAssetsRejectGIFOversizedFrameBeforeDecode(t *testing.T) {
	var encoded bytes.Buffer
	if err := gif.Encode(
		&encoded,
		image.NewPaletted(
			image.Rect(0, 0, 1, 1),
			color.Palette{color.Black, color.White},
		),
		nil,
	); err != nil {
		t.Fatal(err)
	}
	malformed := append([]byte(nil), encoded.Bytes()...)
	descriptor := bytes.IndexByte(malformed, 0x2c)
	if descriptor < 0 || len(malformed)-descriptor < 10 {
		t.Fatal("encoded GIF has no image descriptor")
	}
	binary.LittleEndian.PutUint16(
		malformed[descriptor+5:descriptor+7],
		math.MaxUint16,
	)
	if _, _, _, _, err := decodeImageAsset(
		malformed,
		DecodeOptions{MediaType: "image/gif"},
		DefaultAssetLimits(),
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("oversized GIF frame error = %v", err)
	}
}

func TestAssetsNormalizesDecodeMediaTypeBeforeCaching(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	encoded := testPNG(t)
	first, err := services.Assets.Decode(
		1,
		encoded,
		DecodeOptions{MediaType: " IMAGE/PNG "},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := services.Assets.Decode(
		1,
		encoded,
		DecodeOptions{MediaType: "image/png"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("normalized asset cache IDs = %s and %s", first, second)
	}
	state := services.Assets.Snapshot()
	if got := state.Assets[0].RequestedMediaType; got != "image/png" {
		t.Fatalf("saved requested media type = %q", got)
	}
}

func TestAssetsRejectMalformedInputBeforeMutation(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	beforeRegistry := services.Registry.Snapshot()
	beforeGraphics := services.Graphics.Snapshot()
	if _, err := services.Assets.Decode(
		1,
		[]byte("not an image"),
		DecodeOptions{},
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Decode malformed error = %v", err)
	}
	if !reflect.DeepEqual(services.Registry.Snapshot(), beforeRegistry) ||
		!reflect.DeepEqual(services.Graphics.Snapshot(), beforeGraphics) {
		t.Fatal("malformed decode mutated registry or graphics")
	}
}

func testLBMP(pixelType, width, height uint32, pixels []byte) []byte {
	encoded := make([]byte, 24+len(pixels)+3)
	copy(encoded[:4], "LBMP")
	binary.LittleEndian.PutUint32(encoded[4:8], pixelType)
	binary.LittleEndian.PutUint32(encoded[8:12], width)
	binary.LittleEndian.PutUint32(encoded[12:16], height)
	binary.LittleEndian.PutUint32(encoded[16:20], uint32(len(pixels)))
	copy(encoded[24:], pixels)
	copy(encoded[len(encoded)-3:], []byte{0xaa, 0xbb, 0xcc})
	return encoded
}

func TestAssetsDecodeSKVMLBMP(t *testing.T) {
	for _, test := range []struct {
		name      string
		encoded   []byte
		wantPixel []byte
	}{
		{
			name:      "RGB332 with trailing guest buffer bytes",
			encoded:   testLBMP(8, 2, 1, []byte{0xe0, 0x1c}),
			wantPixel: []byte{0xfc, 0, 0, 0xff, 0, 0xfc, 0, 0xff},
		},
		{
			name: "RGB565 little endian",
			encoded: testLBMP(16, 2, 1, []byte{
				0x00, 0xf8,
				0xe0, 0x07,
			}),
			wantPixel: []byte{0xff, 0, 0, 0xff, 0, 0xff, 0, 0xff},
		},
		{
			name: "RGB332 mask",
			encoded: func() []byte {
				encoded := testLBMP(8, 2, 1, []byte{0xe0, 0x1c})
				binary.LittleEndian.PutUint32(encoded[20:24], 1)
				encoded[26] = 0x02
				return encoded
			}(),
			wantPixel: []byte{0xfc, 0, 0, 0xff, 0, 0xfc, 0, 0},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			services, err := NewServices(Config{})
			if err != nil {
				t.Fatal(err)
			}
			asset, err := services.Assets.Decode(7, test.encoded, DecodeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			info, err := services.Assets.Info(7, asset)
			if err != nil {
				t.Fatal(err)
			}
			if info.Width != 2 || info.Height != 1 || info.MediaType != "image/x-lbmp" {
				t.Fatalf("decoded LBMP = %+v", info)
			}
			pixels, err := services.Graphics.RGBA(7, info.Frames[0].Surface)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pixels, test.wantPixel) {
				t.Fatalf("decoded pixels = % x; want % x", pixels, test.wantPixel)
			}
		})
	}
}

func TestAssetsRejectMalformedSKVMLBMP(t *testing.T) {
	wrongSize := testLBMP(8, 2, 1, []byte{0xe0})
	truncated := testLBMP(8, 2, 1, []byte{0xe0, 0x1c})[:25]
	truncatedMask := testLBMP(8, 2, 1, []byte{0xe0, 0x1c})[:26]
	binary.LittleEndian.PutUint32(truncatedMask[20:24], 1)
	for _, test := range []struct {
		name    string
		encoded []byte
	}{
		{name: "truncated header", encoded: []byte("LBMP")},
		{name: "unsupported pixel type", encoded: testLBMP(24, 1, 1, []byte{0, 0, 0})},
		{name: "mismatched size", encoded: wrongSize},
		{name: "truncated pixels", encoded: truncated},
		{name: "truncated mask", encoded: truncatedMask},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, _, err := decodeImageAsset(
				test.encoded,
				DecodeOptions{},
				DefaultAssetLimits(),
			); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("malformed LBMP error = %v", err)
			}
		})
	}
}

func testIndexedBMP(bits uint16, packedPixel byte) []byte {
	encoded := make([]byte, 66)
	copy(encoded[:2], "BM")
	binary.LittleEndian.PutUint32(encoded[2:6], uint32(len(encoded)))
	binary.LittleEndian.PutUint32(encoded[10:14], 62)
	binary.LittleEndian.PutUint32(encoded[14:18], 40)
	binary.LittleEndian.PutUint32(encoded[18:22], 2)
	binary.LittleEndian.PutUint32(encoded[22:26], 1)
	binary.LittleEndian.PutUint16(encoded[26:28], 1)
	binary.LittleEndian.PutUint16(encoded[28:30], bits)
	binary.LittleEndian.PutUint32(encoded[34:38], 4)
	binary.LittleEndian.PutUint32(encoded[46:50], 2)
	copy(encoded[54:62], []byte{
		0x00, 0x00, 0xff, 0x00,
		0x00, 0xff, 0x00, 0x00,
	})
	encoded[62] = packedPixel
	return encoded
}

func TestAssetsDecodeIndexedBMP(t *testing.T) {
	for _, test := range []struct {
		name        string
		bits        uint16
		packedPixel byte
	}{
		{name: "one-bit", bits: 1, packedPixel: 0x40},
		{name: "four-bit", bits: 4, packedPixel: 0x01},
	} {
		t.Run(test.name, func(t *testing.T) {
			services, err := NewServices(Config{})
			if err != nil {
				t.Fatal(err)
			}
			asset, err := services.Assets.Decode(
				7,
				testIndexedBMP(test.bits, test.packedPixel),
				DecodeOptions{},
			)
			if err != nil {
				t.Fatal(err)
			}
			info, err := services.Assets.Info(7, asset)
			if err != nil {
				t.Fatal(err)
			}
			pixels, err := services.Graphics.RGBA(
				7,
				info.Frames[0].Surface,
			)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte{
				0xff, 0x00, 0x00, 0xff,
				0x00, 0xff, 0x00, 0xff,
			}
			if !bytes.Equal(pixels, want) {
				t.Fatalf("decoded pixels = %v; want %v", pixels, want)
			}
		})
	}
}

func TestAssetsEncodeSurfaceFormatsAreBoundedAndDecodable(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	surface, err := services.Graphics.CreateSurface(7, SurfaceDescriptor{
		Width: 2, Height: 2, Format: PixelRGBA8888,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range []Color{
		RGB(255, 0, 0),
		RGB(0, 255, 0),
		RGB(0, 0, 255),
		RGB(255, 255, 255),
	} {
		if err := services.Graphics.SetPixel(
			7,
			surface,
			int32(index%2),
			int32(index/2),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, mediaType := range []string{
		"image/bmp",
		"image/png",
		"image/jpeg",
		"image/gif",
	} {
		t.Run(mediaType, func(t *testing.T) {
			encoded, err := services.Assets.EncodeSurface(
				7,
				surface,
				mediaType,
				Rectangle{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) == 0 ||
				uint64(len(encoded)) > services.Config.Limits.Assets.MaxEncodedBytes {
				t.Fatalf("encoded size = %d", len(encoded))
			}
			asset, err := services.Assets.Decode(
				7,
				encoded,
				DecodeOptions{MediaType: mediaType},
			)
			if err != nil {
				t.Fatal(err)
			}
			info, err := services.Assets.Info(7, asset)
			if err != nil {
				t.Fatal(err)
			}
			if info.Width != 2 || info.Height != 2 {
				t.Fatalf("decoded encoded surface = %+v", info)
			}
		})
	}
	if _, err := services.Assets.EncodeSurface(
		7,
		surface,
		"image/png",
		Rectangle{X: 1},
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("EncodeSurface accepted positioned empty region: %v", err)
	}
}

func TestAssetsDecodeBI_RGB32TreatsReservedByteAsOpaque(t *testing.T) {
	encoded := make([]byte, 58)
	copy(encoded[:2], "BM")
	binary.LittleEndian.PutUint32(encoded[2:6], uint32(len(encoded)))
	binary.LittleEndian.PutUint32(encoded[10:14], 54)
	binary.LittleEndian.PutUint32(encoded[14:18], 40)
	binary.LittleEndian.PutUint32(encoded[18:22], 1)
	binary.LittleEndian.PutUint32(encoded[22:26], 1)
	binary.LittleEndian.PutUint16(encoded[26:28], 1)
	binary.LittleEndian.PutUint16(encoded[28:30], 32)
	binary.LittleEndian.PutUint32(encoded[34:38], 4)
	copy(encoded[54:], []byte{0x33, 0x22, 0x11, 0x00})

	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	asset, err := services.Assets.Decode(
		1,
		encoded,
		DecodeOptions{MediaType: "image/bmp"},
	)
	if err != nil {
		t.Fatal(err)
	}
	info, err := services.Assets.Info(1, asset)
	if err != nil {
		t.Fatal(err)
	}
	pixels, err := services.Graphics.RGBA(1, info.Frames[0].Surface)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pixels, []byte{0x11, 0x22, 0x33, 0xff}) {
		t.Fatalf("BI_RGB32 pixel = % x", pixels)
	}
}
