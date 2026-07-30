package debugkit

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
)

type ScreenReport struct {
	MinX           int    `json:"min_x"`
	MinY           int    `json:"min_y"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	RGBASHA256     string `json:"rgba_sha256"`
	NonBlackPixels uint64 `json:"non_black_pixels"`
	VisiblePixels  uint64 `json:"visible_pixels"`
}

type Pixel struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
	A uint8 `json:"a"`
}

func InspectScreen(frame image.Image) ScreenReport {
	if frame == nil {
		return ScreenReport{}
	}
	bounds := frame.Bounds()
	digest := sha256.New()
	var geometry [16]byte
	binary.LittleEndian.PutUint64(geometry[0:8], uint64(bounds.Dx()))
	binary.LittleEndian.PutUint64(geometry[8:16], uint64(bounds.Dy()))
	_, _ = digest.Write(geometry[:])

	var nonBlack, visible uint64
	var rgba [4]byte
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := pixelAt(frame, x, y)
			rgba = [4]byte{pixel.R, pixel.G, pixel.B, pixel.A}
			_, _ = digest.Write(rgba[:])
			if pixel.R != 0 || pixel.G != 0 || pixel.B != 0 {
				nonBlack++
			}
			if pixel.A != 0 {
				visible++
			}
		}
	}
	return ScreenReport{
		MinX:           bounds.Min.X,
		MinY:           bounds.Min.Y,
		Width:          bounds.Dx(),
		Height:         bounds.Dy(),
		RGBASHA256:     hex.EncodeToString(digest.Sum(nil)),
		NonBlackPixels: nonBlack,
		VisiblePixels:  visible,
	}
}

func pixelAt(frame image.Image, x, y int) Pixel {
	rgba := color.RGBAModel.Convert(frame.At(x, y)).(color.RGBA)
	return Pixel{R: rgba.R, G: rgba.G, B: rgba.B, A: rgba.A}
}

func WritePNG(path string, frame image.Image) error {
	if frame == nil {
		return fmt.Errorf("write screenshot: framebuffer is nil")
	}
	if err := writeFile(path, func(output io.Writer) error {
		if err := png.Encode(output, frame); err != nil {
			return fmt.Errorf("encode PNG: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("write screenshot %q: %w", path, err)
	}
	return nil
}
