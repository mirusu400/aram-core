package brewrt

import (
	"encoding/binary"
	"image/color"
	"testing"
)

// shortTableBMP is a 2x1 4-bit BMP whose color table holds two entries (red,
// green) while its second pixel uses index 15, the shape 카우스블레이드 and
// 소드 데스티니 ship.
func shortTableBMP() []byte {
	const infoSize, used = 40, 2
	pixelOffset := 14 + infoSize + used*4
	data := make([]byte, pixelOffset+4)
	data[0], data[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(data[2:], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[10:], uint32(pixelOffset))
	binary.LittleEndian.PutUint32(data[14:], infoSize)
	binary.LittleEndian.PutUint32(data[18:], 2) // width
	binary.LittleEndian.PutUint32(data[22:], 1) // height
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 4)
	binary.LittleEndian.PutUint32(data[46:], used)
	copy(data[54:], []byte{0, 0, 0xff, 0, 0, 0xff, 0, 0}) // BGRx red, green
	data[pixelOffset] = 0x1f                              // pixel 0 = 1, pixel 1 = 15
	return data
}

func TestHandsetBMPPadsAShortColorTable(t *testing.T) {
	decoded, err := decodeHandsetBMP(shortTableBMP())
	if err != nil {
		t.Fatalf("short color table rejected: %v", err)
	}
	// Reading every pixel used to panic in image.Paletted.At.
	if got := color.RGBAModel.Convert(decoded.At(0, 0)).(color.RGBA); got != (color.RGBA{0, 0xff, 0, 0xff}) {
		t.Fatalf("pixel 0 = %v, want green", got)
	}
	if got := color.RGBAModel.Convert(decoded.At(1, 0)).(color.RGBA); got != (color.RGBA{0, 0, 0, 0xff}) {
		t.Fatalf("pixel 1 (index past the table) = %v, want black", got)
	}
	// nativeRGB565 is what createNativeBitmap feeds every decoded image to.
	if data := nativeRGB565(decoded, 4); len(data) != 4 {
		t.Fatalf("converted %d bytes", len(data))
	}
}

func TestHandsetBMPLeavesFullTablesAlone(t *testing.T) {
	data := shortTableBMP()
	binary.LittleEndian.PutUint32(data[46:], 0) // biClrUsed 0 means a full table
	if padded := padBMPColorTable(data); &padded[0] != &data[0] {
		t.Fatal("a BMP without a short table was copied")
	}
	truecolor := shortTableBMP()
	binary.LittleEndian.PutUint16(truecolor[28:], 24)
	if padded := padBMPColorTable(truecolor); &padded[0] != &truecolor[0] {
		t.Fatal("a 24-bit BMP was rewritten")
	}
}
