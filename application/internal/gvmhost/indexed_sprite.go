package gvmhost

import "errors"

var (
	errInvalidSpriteType       = errors.New("gvmhost: invalid sprite type")
	errInvalidSpriteDimensions = errors.New("gvmhost: invalid sprite dimensions")
	errTruncatedSprite         = errors.New("gvmhost: truncated sprite")
)

type indexedSurface struct {
	width  int
	height int
	pixels []byte
}

func newIndexedSurface(width, height int) indexedSurface {
	return indexedSurface{
		width:  width,
		height: height,
		pixels: make([]byte, width*height),
	}
}

type indexedSprite struct {
	width            int
	height           int
	anchorX          int
	anchorY          int
	bitsPerPixel     uint8
	palette          []byte
	pixels           []byte
	transparentIndex int
}

// decodeIndexedSprite validates and decodes the complete sprite before returning.
// Type 8 color values are intentionally retained as raw bytes, including values
// above 181; mapping those values for presentation remains unresolved.
func decodeIndexedSprite(data []byte) (indexedSprite, error) {
	if len(data) < 5 {
		return indexedSprite{}, errTruncatedSprite
	}

	sprite := indexedSprite{
		width:            int(data[1]),
		height:           int(data[2]),
		anchorX:          int(int8(data[3])),
		anchorY:          int(int8(data[4])),
		transparentIndex: -1,
	}
	if sprite.width == 0 || sprite.height == 0 {
		return indexedSprite{}, errInvalidSpriteDimensions
	}

	paletteOffset := 5
	pixelOffset := 0
	switch data[0] {
	case 2:
		sprite.bitsPerPixel = 1
		pixelOffset = 6
	case 5:
		sprite.bitsPerPixel = 1
		pixelOffset = 7
	case 6:
		sprite.bitsPerPixel = 2
		pixelOffset = 9
	case 7:
		sprite.bitsPerPixel = 4
		pixelOffset = 21
	case 8:
		sprite.bitsPerPixel = 8
		pixelOffset = 5
	default:
		return indexedSprite{}, errInvalidSpriteType
	}

	pixelCount := sprite.width * sprite.height
	requiredPixelBytes := (pixelCount*int(sprite.bitsPerPixel) + 7) / 8
	if len(data) < pixelOffset+requiredPixelBytes {
		return indexedSprite{}, errTruncatedSprite
	}

	if data[0] != 8 {
		paletteSize := 1 << sprite.bitsPerPixel
		sprite.palette = make([]byte, paletteSize)
		if data[0] == 2 {
			sprite.palette[0] = data[paletteOffset] >> 4
			sprite.palette[1] = data[paletteOffset] & 0x0f
		} else {
			copy(sprite.palette, data[paletteOffset:pixelOffset])
		}
		if sprite.palette[paletteSize-1] == 4 {
			sprite.transparentIndex = paletteSize - 1
		}
	}

	sprite.pixels = make([]byte, pixelCount)
	packed := data[pixelOffset : pixelOffset+requiredPixelBytes]
	if sprite.bitsPerPixel == 8 {
		copy(sprite.pixels, packed)
		sprite.transparentIndex = 4
		return sprite, nil
	}

	mask := byte((1 << sprite.bitsPerPixel) - 1)
	for index := range sprite.pixels {
		bitOffset := index * int(sprite.bitsPerPixel)
		shift := 8 - int(sprite.bitsPerPixel) - bitOffset%8
		sprite.pixels[index] = (packed[bitOffset/8] >> shift) & mask
	}
	return sprite, nil
}

func rasterizeIndexedSprite(surface *indexedSurface, data []byte, x, y int) error {
	sprite, err := decodeIndexedSprite(data)
	if err != nil {
		return err
	}

	originX := x - sprite.anchorX
	originY := y - sprite.anchorY
	for sourceY := 0; sourceY < sprite.height; sourceY++ {
		destinationY := originY + sourceY
		if destinationY < 0 || destinationY >= surface.height {
			continue
		}
		for sourceX := 0; sourceX < sprite.width; sourceX++ {
			destinationX := originX + sourceX
			if destinationX < 0 || destinationX >= surface.width {
				continue
			}
			index := sprite.pixels[sourceY*sprite.width+sourceX]
			if int(index) == sprite.transparentIndex {
				continue
			}
			color := index
			if sprite.palette != nil {
				color = sprite.palette[index]
			}
			surface.pixels[destinationY*surface.width+destinationX] = color
		}
	}
	return nil
}
