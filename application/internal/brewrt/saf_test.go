package brewrt

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image/color"
	"math/rand"
	"testing"
)

func TestDecodeSAFImageMLZ(t *testing.T) {
	const width, height = 64, 64
	pixels := make([]byte, width*height)
	random := rand.New(rand.NewSource(314))
	colors := []byte{0x00, 0xff, 0xe3, 0x92, 0xff, 0xff, 0xff, 0xff}
	for index := range pixels {
		pixels[index] = colors[random.Intn(len(colors))]
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(pixels); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	standard := compressed.Bytes()
	if standard[2]&6 != 4 { // dynamic-Huffman block
		t.Fatalf("synthetic zlib block type = %d, want dynamic", standard[2]&7)
	}
	deflated := standard[2 : len(standard)-4]
	shifted := safTestBitSlice(deflated, 3, len(deflated)*8)
	treeBits, ok := safTreeBitLength(shifted)
	if !ok {
		t.Fatal("could not locate synthetic dynamic-Huffman tree end")
	}
	// MLZ puts the tree in a separate command and retains the block's 3-bit
	// header, coded pixels, and Adler-32 in the object stream.
	tree := append(safTestBitSlice(deflated, 3, 3+treeBits), 0)
	imageBits := safTestBitSlice(deflated, 0, 3)
	imageBits = safTestAppendBits(imageBits, 3, deflated, 3+treeBits, len(deflated)*8)
	packed := append([]byte(nil), standard[:2]...)
	packed = append(packed, imageBits...)
	packed = append(packed, standard[len(standard)-4:]...)
	group := []byte{1, 6, width, height, 8, 4, 0, 0, 2, 2, 1, 0}
	saf := []byte{'S', 'A', 'F', 0, 2, 0, 0}
	saf = safTestCommand(saf, 7, []byte{0, byte(len(group))})
	saf = append(saf, group...)
	saf = safTestCommand(saf, 11, tree)
	saf = safTestCommand(saf, 4, []byte{0, width, height, 1, 0xe3})
	saf = binary.BigEndian.AppendUint16(saf, uint16(len(packed)))
	saf = append(saf, packed...)
	saf = safTestCommand(saf, 6, []byte{0xff, 1, 0, 5, 0, 0, 0, 0, 0})
	saf = safTestCommand(saf, 8, nil)
	binary.BigEndian.PutUint16(saf[5:7], uint16(len(saf)))
	decoded, ok := decodeSAFImage(saf)
	if !ok || decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		t.Fatalf("synthetic MLZ image = %v, ok=%v", decoded, ok)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := safRGB332(pixels[y*width+x])
			if got := color.RGBAModel.Convert(decoded.At(x, y)).(color.RGBA); got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
	resource := append([]byte{12, 0}, []byte("image/sis\x00")...)
	resource = append(resource, saf...)
	if result, ok := decodeBREWResourceImage(resource); !ok || result.Bounds() != decoded.Bounds() {
		t.Fatalf("BREW resource SAF decode = %v, ok=%v", result, ok)
	}
	for _, mutate := range []func([]byte){
		func(data []byte) { data[6]-- },             // inconsistent declared size
		func(data []byte) { data[len(data)-1] = 1 }, // invalid end command
		func(data []byte) { data[4] = 3 },           // unknown SAF version
	} {
		bad := append([]byte(nil), saf...)
		mutate(bad)
		if _, ok := decodeSAFImage(bad); ok {
			t.Fatal("malformed SAF image was accepted")
		}
	}
	for length := 0; length < len(saf); length++ {
		if _, ok := decodeSAFImage(saf[:length]); ok {
			t.Fatalf("truncated SAF prefix of %d bytes was accepted", length)
		}
	}
}

func safTestCommand(dst []byte, kind byte, payload []byte) []byte {
	dst = append(dst, kind)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(payload)))
	return append(dst, payload...)
}

func safTestBitSlice(source []byte, first, last int) []byte {
	return safTestAppendBits(nil, 0, source, first, last)
}

func safTestAppendBits(dst []byte, at int, source []byte, first, last int) []byte {
	for bit := first; bit < last; bit++ {
		if at/8 >= len(dst) {
			dst = append(dst, 0)
		}
		if source[bit/8]&(1<<(bit%8)) != 0 {
			dst[at/8] |= 1 << (at % 8)
		}
		at++
	}
	return dst
}
