package brewrt

import (
	"bytes"
	"encoding/binary"
	"image"

	"golang.org/x/image/bmp"
)

// decodeHandsetBMP decodes a BMP the way a handset's DIB loader reads it.
//
// An indexed BMP may carry a shorter color table than its bit depth can
// address while its pixels still use the higher indices: 카우스블레이드 and
// 소드 데스티니 ship 4-bit sprites with a nine-entry table and pixels up to
// index 15. A handset copies the table into a full-size one whose remaining
// entries stay zero, so those pixels come out black. The host decoder instead
// either rejects the image (x/image v0.45 onwards) or returns an
// image.Paletted whose At panics on the stray index (earlier versions) - a
// host panic in the middle of a session. Padding the table first gives the
// handset result under every decoder version.
func decodeHandsetBMP(encoded []byte) (image.Image, error) {
	return bmp.Decode(bytes.NewReader(padBMPColorTable(encoded)))
}

// padBMPColorTable returns encoded itself unless it is an indexed
// BITMAPINFOHEADER image whose biClrUsed is below 1<<bpp; then it returns a
// copy whose color table has all 1<<bpp entries, the added ones black.
func padBMPColorTable(encoded []byte) []byte {
	if len(encoded) < 54 || encoded[0] != 'B' || encoded[1] != 'M' {
		return encoded
	}
	infoSize := uint64(binary.LittleEndian.Uint32(encoded[14:18]))
	bitsPerPixel := binary.LittleEndian.Uint16(encoded[28:30])
	if infoSize < 40 || bitsPerPixel == 0 || bitsPerPixel > 8 {
		return encoded
	}
	full := uint64(1) << bitsPerPixel
	used := uint64(binary.LittleEndian.Uint32(encoded[46:50]))
	if used == 0 || used >= full {
		return encoded
	}
	tableStart := 14 + infoSize
	pixelOffset := uint64(binary.LittleEndian.Uint32(encoded[10:14]))
	if tableStart+used*4 > pixelOffset || pixelOffset > uint64(len(encoded)) {
		return encoded
	}
	padded := make([]byte, 0, tableStart+full*4+uint64(len(encoded))-pixelOffset)
	padded = append(padded, encoded[:tableStart+used*4]...)
	padded = append(padded, make([]byte, (full-used)*4)...)
	padded = append(padded, encoded[pixelOffset:]...)
	binary.LittleEndian.PutUint32(padded[2:6], uint32(len(padded)))
	binary.LittleEndian.PutUint32(padded[10:14], uint32(tableStart+full*4))
	binary.LittleEndian.PutUint32(padded[46:50], uint32(full))
	return padded
}
