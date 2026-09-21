package brewrt

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/adler32"
	"image"
	"image/color"
	"io"
)

// The version-2 SIS/SAF images used by early BREW games store the dynamic
// DEFLATE tree once (command 0x0b), then omit it from each MLZ image block.
// Rejoining the two bitstreams lets the standard Go inflater do the actual
// decompression. Unknown SAF features remain on the transparent fallback path.
func decodeSAFImage(saf []byte) (image.Image, bool) {
	if len(saf) < 10 || len(saf) > 1<<20 || !bytes.HasPrefix(saf, []byte("SAF\x00")) ||
		saf[4] != 2 || int(binary.BigEndian.Uint16(saf[5:7])) != len(saf) {
		return nil, false
	}
	var canvasW, canvasH, depth int
	var paletteType byte
	var tree []byte
	objects := make(map[byte]*safObject)
	var frame []byte
	ended := false
	for pos := 7; pos+3 <= len(saf); {
		kind := saf[pos]
		size := int(binary.BigEndian.Uint16(saf[pos+1:]))
		pos += 3
		if size > len(saf)-pos {
			return nil, false
		}
		payload := saf[pos : pos+size]
		pos += size
		switch kind {
		case 7: // global header group
			if size != 2 {
				return nil, false
			}
			groupSize := int(binary.BigEndian.Uint16(payload))
			if groupSize > len(saf)-pos {
				return nil, false
			}
			var ok bool
			canvasW, canvasH, depth, paletteType, ok = safCanvas(saf[pos : pos+groupSize])
			if !ok {
				return nil, false
			}
			pos += groupSize
		case 11: // shared MLZ Huffman tree
			tree = payload
		case 4: // image object, followed by a separately sized compressed stream
			if size != 5 || pos+2 > len(saf) || canvasW == 0 || depth != 8 || len(tree) == 0 {
				return nil, false
			}
			compressedSize := int(binary.BigEndian.Uint16(saf[pos:]))
			pos += 2
			if compressedSize > len(saf)-pos {
				return nil, false
			}
			w, h := int(payload[1]), int(payload[2])
			if w == 0 || h == 0 || w > 255 || h > 255 || payload[3] != 1 {
				return nil, false
			}
			pixels, ok := inflateSAFMLZ(tree, saf[pos:pos+compressedSize], w*h)
			if !ok {
				return nil, false
			}
			objects[payload[0]&0x7f] = &safObject{w: w, h: h, key: payload[4], pixels: pixels}
			pos += compressedSize
		case 6:
			if frame == nil {
				frame = payload
			}
		case 8:
			if size != 0 || pos != len(saf) {
				return nil, false
			}
			ended = true
		default:
			// Other top-level commands may change the image. Do not guess.
			return nil, false
		}
		if ended {
			break
		}
	}
	if !ended || canvasW == 0 || canvasH == 0 || depth != 8 || paletteType != 0 ||
		len(frame) < 2 || len(objects) == 0 {
		return nil, false
	}
	result := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	background := safRGB332(frame[0])
	for y := 0; y < canvasH; y++ {
		for x := 0; x < canvasW; x++ {
			result.SetRGBA(x, y, background)
		}
	}
	position := 2
	for movement := 0; movement < int(frame[1]); movement++ {
		if position+7 > len(frame) || frame[position] != 0 || frame[position+1] != 5 ||
			frame[position+3] != 0 || frame[position+4]&0xf0 != 0 {
			return nil, false // transformed/scaled motion is not implemented
		}
		object := objects[frame[position+2]]
		if object == nil {
			return nil, false
		}
		x0, y0 := int(frame[position+5]), int(frame[position+6])
		transparent := frame[position+4]&0x0f != 0
		for y := 0; y < object.h; y++ {
			for x := 0; x < object.w; x++ {
				value := object.pixels[y*object.w+x]
				if !transparent || value != object.key {
					result.SetRGBA(x0+x, y0+y, safRGB332(value))
				}
			}
		}
		position += 7
	}
	if position != len(frame) {
		return nil, false
	}
	return result, true
}

type safObject struct {
	w, h   int
	key    byte
	pixels []byte
}

func safCanvas(group []byte) (width, height, depth int, paletteType byte, ok bool) {
	pos := 0
	for pos < len(group) {
		if pos+2 > len(group) {
			return 0, 0, 0, 0, false
		}
		kind, size := group[pos], int(group[pos+1])
		pos += 2
		if size > len(group)-pos {
			return 0, 0, 0, 0, false
		}
		payload := group[pos : pos+size]
		pos += size
		switch kind {
		case 1:
			if size < 3 {
				return 0, 0, 0, 0, false
			}
			width, height, depth = int(payload[0]), int(payload[1]), int(payload[2])
		case 2:
			if size < 2 {
				return 0, 0, 0, 0, false
			}
			paletteType = (payload[1] >> 5) & 3
		}
	}
	return width, height, depth, paletteType, width > 0 && height > 0
}

func safRGB332(value byte) color.RGBA {
	return color.RGBA{R: uint8(int(value>>5) * 255 / 7), G: uint8(int((value>>2)&7) * 255 / 7), B: (value & 3) * 85, A: 255}
}

func inflateSAFMLZ(tree, packed []byte, size int) ([]byte, bool) {
	if size <= 0 || size > 255*255 || len(packed) < 7 || len(tree) == 0 ||
		packed[0] != 0x78 || packed[1] != 0x9c || packed[2]&6 != 4 {
		return nil, false
	}
	treeBits, ok := safTreeBitLength(tree)
	if !ok || len(tree)*8-treeBits > 15 {
		return nil, false
	}
	deflated := packed[2 : len(packed)-4]
	joined := make([]byte, (len(deflated)*8+treeBits+7)/8)
	at := 0
	appendBits := func(source []byte, first, last int) {
		for bit := first; bit < last; bit++ {
			if source[bit/8]&(1<<(bit%8)) != 0 {
				joined[at/8] |= 1 << (at % 8)
			}
			at++
		}
	}
	appendBits(deflated, 0, 3)
	appendBits(tree, 0, treeBits)
	appendBits(deflated, 3, len(deflated)*8)
	reader := flate.NewReader(bytes.NewReader(joined))
	pixels, err := io.ReadAll(io.LimitReader(reader, int64(size+1)))
	reader.Close()
	if err != nil || len(pixels) != size || adler32.Checksum(pixels) != binary.BigEndian.Uint32(packed[len(packed)-4:]) {
		return nil, false
	}
	return pixels, true
}

type safBits struct {
	data []byte
	pos  int
}

func (b *safBits) read(count int) (uint16, bool) {
	if count < 0 || count > 16 || count > len(b.data)*8-b.pos {
		return 0, false
	}
	var value uint16
	for bit := 0; bit < count; bit++ {
		value |= uint16((b.data[b.pos/8]>>(b.pos%8))&1) << bit
		b.pos++
	}
	return value, true
}

func safTreeBitLength(tree []byte) (int, bool) {
	b := safBits{data: tree}
	hlit, ok := b.read(5)
	if !ok {
		return 0, false
	}
	hdist, ok := b.read(5)
	if !ok {
		return 0, false
	}
	hclen, ok := b.read(4)
	if !ok || hlit > 29 || hdist > 29 {
		return 0, false
	}
	order := [...]byte{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
	var lengths [19]byte
	for index := 0; index < int(hclen)+4; index++ {
		value, ok := b.read(3)
		if !ok {
			return 0, false
		}
		lengths[order[index]] = byte(value)
	}
	var counts [8]uint16
	for _, length := range lengths {
		counts[length]++
	}
	counts[0] = 0
	code := uint16(0)
	var next [8]uint16
	for bits := 1; bits <= 7; bits++ {
		code = (code + counts[bits-1]) << 1
		next[bits] = code
	}
	lookup := make(map[uint16]byte)
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		canonical := next[length]
		next[length]++
		var reversed uint16
		for bit := byte(0); bit < length; bit++ {
			reversed = (reversed << 1) | ((canonical >> bit) & 1)
		}
		lookup[uint16(length)<<8|reversed] = byte(symbol)
	}
	remaining := int(hlit) + 257 + int(hdist) + 1
	total := remaining
	for remaining > 0 {
		var bits uint16
		var symbol byte
		found := false
		for length := 1; length <= 7; length++ {
			value, ok := b.read(1)
			if !ok {
				return 0, false
			}
			bits |= value << (length - 1)
			if symbol, found = lookup[uint16(length)<<8|bits]; found {
				break
			}
		}
		if !found {
			return 0, false
		}
		repeat := 1
		switch symbol {
		case 16:
			if remaining == total {
				return 0, false
			}
			value, ok := b.read(2)
			if !ok {
				return 0, false
			}
			repeat = int(value) + 3
		case 17:
			value, ok := b.read(3)
			if !ok {
				return 0, false
			}
			repeat = int(value) + 3
		case 18:
			value, ok := b.read(7)
			if !ok {
				return 0, false
			}
			repeat = int(value) + 11
		}
		if repeat > remaining {
			return 0, false
		}
		remaining -= repeat
	}
	return b.pos, true
}
