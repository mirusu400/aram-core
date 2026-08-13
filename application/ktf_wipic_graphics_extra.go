package application

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"

	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	ktfWIPICMaxPixelTransfer = int64(32 << 20)
	ktfWIPICMaxPolygonPoints = 4096
)

func readKTFWIPICParameters(
	runtime *ktfRuntime,
	count int,
	operation string,
) ([]uint32, error) {
	values := make([]uint32, count)
	for index := range values {
		value, err := runtime.parameter(uint32(index))
		if err != nil {
			return nil, fmt.Errorf(
				"read KTF WIPI-C %s parameter %d: %w",
				operation,
				index,
				err,
			)
		}
		values[index] = value
	}
	return values, nil
}

// paintWIPICImageFrame converts a decoded shared RGBA surface into the
// provider-private RGB565 framebuffer that native KTF clients dereference.
// paintWIPICImageFrame returns the RGB565 transparent color key for the frame,
// or -1 when it draws fully opaque. The key is the top-left pixel when that
// pixel is in the magenta family color-keyed bitmaps reserve for transparency.
func (r *ktfRuntime) paintWIPICImageFrame(
	framebufferHandle uint32,
	surface shared.ServiceID,
) (int32, error) {
	framebuffer := r.wipicFramebuffers[framebufferHandle]
	if framebuffer == nil {
		return -1, fmt.Errorf(
			"KTF WIPI-C image framebuffer 0x%08x is unavailable",
			framebufferHandle,
		)
	}
	descriptor, err := r.services.Graphics.Descriptor(r.serviceOwner, surface)
	if err != nil {
		return -1, err
	}
	rgba, err := r.services.Graphics.RGBA(r.serviceOwner, surface)
	if err != nil {
		return -1, err
	}
	width, height := int(descriptor.Width), int(descriptor.Height)
	if width <= 0 || height <= 0 ||
		uint64(width)*uint64(height)*4 > uint64(len(rgba)) {
		return -1, errors.New("KTF WIPI-C decoded image surface is malformed")
	}
	pixels := make([]byte, framebuffer.stride*framebuffer.height)
	width = min(width, framebuffer.width)
	height = min(height, framebuffer.height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*int(descriptor.Width) + x) * 4
			alpha := uint32(rgba[offset+3])
			red := uint32(rgba[offset+0]) * alpha / 0xff
			green := uint32(rgba[offset+1]) * alpha / 0xff
			blue := uint32(rgba[offset+2]) * alpha / 0xff
			binary.LittleEndian.PutUint16(
				pixels[y*framebuffer.stride+x*2:],
				ktfWIPICRGB565(red, green, blue),
			)
		}
	}
	if err := r.cpu.WriteMemory(framebuffer.pixels, pixels); err != nil {
		return -1, err
	}
	transparentKey := int32(-1)
	if width > 0 && height > 0 {
		if corner := binary.LittleEndian.Uint16(pixels); ktfIsColorKeyMagenta565(corner) {
			transparentKey = int32(corner)
		}
	}
	return transparentKey, r.commitKTFWIPICFramebuffer(framebufferHandle)
}

func ktfWIPICGraphicsCopyArea(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values, err := readKTFWIPICParameters(runtime, 8, "copy-area")
	if err != nil {
		return 0, err
	}
	state, err := runtime.wipicGraphicsContext(values[7])
	if err != nil {
		return 0, err
	}
	if err := runtime.blitWIPICFramebuffer(
		values[0],
		values[0],
		int64(int32(values[1]))+int64(state.offsetX),
		int64(int32(values[2]))+int64(state.offsetY),
		int64(int32(values[3])),
		int64(int32(values[4])),
		int64(int32(values[5])),
		int64(int32(values[6])),
		state,
	); err != nil {
		return 0, err
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

func ktfWIPICGraphicsDrawArc(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.drawWIPICArc(false)
}

func ktfWIPICGraphicsFillArc(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.drawWIPICArc(true)
}

func (r *ktfRuntime) drawWIPICArc(fill bool) (uint32, error) {
	values, err := readKTFWIPICParameters(r, 8, "arc")
	if err != nil {
		return 0, err
	}
	framebuffer := r.wipicFramebuffers[values[0]]
	if framebuffer == nil {
		return 0, nil
	}
	state, err := r.wipicGraphicsContext(values[7])
	if err != nil {
		return 0, err
	}
	width := int64(int32(values[3]))
	height := int64(int32(values[4]))
	sweep := int(int32(values[6]))
	if width <= 0 || height <= 0 || sweep == 0 ||
		width > int64(framebuffer.width)*2 ||
		height > int64(framebuffer.height)*2 {
		return 0, nil
	}
	x := int64(int32(values[1])) + int64(state.offsetX)
	y := int64(int32(values[2])) + int64(state.offsetY)
	left, top, right, bottom := ktfWIPICVisibleBounds(framebuffer, state)
	left = max(left, x)
	top = max(top, y)
	right = min(right, x+width)
	bottom = min(bottom, y+height)
	if left >= right || top >= bottom {
		return 0, nil
	}
	radiusX, radiusY := width/2, height/2
	if radiusX <= 0 || radiusY <= 0 {
		for row := top; row < bottom; row++ {
			for column := left; column < right; column++ {
				if err := r.writeWIPICPixel(
					values[0],
					int(column),
					int(row),
					state,
				); err != nil {
					return 0, err
				}
			}
		}
		return 0, r.commitKTFWIPICFramebuffer(values[0])
	}
	centerX, centerY := x+radiusX, y+radiusY
	rxSquared := radiusX * radiusX
	rySquared := radiusY * radiusY
	radiusProduct := rxSquared * rySquared
	threshold := rxSquared*radiusY + rySquared*radiusX
	start := int(int32(values[5]))
	for row := top; row < bottom; row++ {
		for column := left; column < right; column++ {
			dx, dy := column-centerX, row-centerY
			distance := dx*dx*rySquared + dy*dy*rxSquared
			inside := distance <= radiusProduct
			if !fill {
				delta := distance - radiusProduct
				if delta < 0 {
					delta = -delta
				}
				inside = delta <= threshold
			}
			if inside && pointInWIPIArc(int(dx), int(dy), start, sweep) {
				if err := r.writeWIPICPixel(
					values[0],
					int(column),
					int(row),
					state,
				); err != nil {
					return 0, err
				}
			}
		}
	}
	return 0, r.commitKTFWIPICFramebuffer(values[0])
}

func ktfWIPICGraphicsGetRGBPixels(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values, err := readKTFWIPICParameters(runtime, 7, "get-RGB-pixels")
	if err != nil {
		return 0, err
	}
	framebuffer := runtime.wipicFramebuffers[values[0]]
	width, height := int64(int32(values[3])), int64(int32(values[4]))
	pitch := int64(int32(values[6]))
	if pitch <= 0 {
		pitch = width
	}
	if framebuffer == nil || width <= 0 || height <= 0 ||
		width > int64(framebuffer.width)*2 ||
		height > int64(framebuffer.height)*2 ||
		!validKTFWIPICRGBTransfer(values[5], width, height, pitch) {
		return 0, nil
	}
	x, y := int64(int32(values[1])), int64(int32(values[2]))
	firstColumn := max(int64(0), -x)
	lastColumn := min(width, int64(framebuffer.width)-x)
	firstRow := max(int64(0), -y)
	lastRow := min(height, int64(framebuffer.height)-y)
	if firstColumn >= lastColumn || firstRow >= lastRow {
		return 0, nil
	}
	columns, rows := int(lastColumn-firstColumn), int(lastRow-firstRow)
	native := make([]byte, columns*rows*2)
	for row := 0; row < rows; row++ {
		address := framebuffer.pixels + uint32(
			(y+firstRow+int64(row))*int64(framebuffer.stride)+
				(x+firstColumn)*2,
		)
		if err := runtime.cpu.ReadMemory(
			address,
			native[row*columns*2:(row+1)*columns*2],
		); err != nil {
			return 0, err
		}
	}
	converted := make([]byte, columns*4)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			pixel := binary.LittleEndian.Uint16(
				native[(row*columns+column)*2:],
			)
			red, green, blue := ktfWIPICRGBFrom565(pixel)
			binary.LittleEndian.PutUint32(
				converted[column*4:],
				red<<16|green<<8|blue,
			)
		}
		outputIndex := (firstRow+int64(row))*pitch + firstColumn
		if err := runtime.cpu.WriteMemory(
			values[5]+uint32(outputIndex*4),
			converted,
		); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func ktfWIPICGraphicsSetRGBPixels(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values, err := readKTFWIPICParameters(runtime, 8, "set-RGB-pixels")
	if err != nil {
		return 0, err
	}
	framebuffer := runtime.wipicFramebuffers[values[0]]
	width, height := int64(int32(values[3])), int64(int32(values[4]))
	pitch := int64(int32(values[6]))
	if pitch <= 0 {
		pitch = width
	}
	if framebuffer == nil || width <= 0 || height <= 0 ||
		width > int64(framebuffer.width)*2 ||
		height > int64(framebuffer.height)*2 ||
		!validKTFWIPICRGBTransfer(values[5], width, height, pitch) {
		return 0, nil
	}
	state, err := runtime.wipicGraphicsContext(values[7])
	if err != nil {
		return 0, err
	}
	x := int64(int32(values[1])) + int64(state.offsetX)
	y := int64(int32(values[2])) + int64(state.offsetY)
	visibleLeft, visibleTop, visibleRight, visibleBottom :=
		ktfWIPICVisibleBounds(framebuffer, state)
	firstColumn := max(int64(0), visibleLeft-x)
	lastColumn := min(width, visibleRight-x)
	firstRow := max(int64(0), visibleTop-y)
	lastRow := min(height, visibleBottom-y)
	if firstColumn >= lastColumn || firstRow >= lastRow {
		return 0, nil
	}
	columns, rows := int(lastColumn-firstColumn), int(lastRow-firstRow)
	source := make([]byte, columns*rows*4)
	for row := 0; row < rows; row++ {
		inputIndex := (firstRow+int64(row))*pitch + firstColumn
		if err := runtime.cpu.ReadMemory(
			values[5]+uint32(inputIndex*4),
			source[row*columns*4:(row+1)*columns*4],
		); err != nil {
			return 0, err
		}
	}
	native := make([]byte, columns*2)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			pixel := binary.LittleEndian.Uint32(
				source[(row*columns+column)*4:],
			)
			binary.LittleEndian.PutUint16(
				native[column*2:],
				ktfWIPICRGB565(pixel>>16, pixel>>8, pixel),
			)
		}
		address := framebuffer.pixels + uint32(
			(y+firstRow+int64(row))*int64(framebuffer.stride)+
				(x+firstColumn)*2,
		)
		if err := runtime.cpu.WriteMemory(address, native); err != nil {
			return 0, err
		}
	}
	return 0, runtime.commitKTFWIPICFramebuffer(values[0])
}

func validKTFWIPICRGBTransfer(
	address uint32,
	width, height, pitch int64,
) bool {
	if address == 0 || width <= 0 || height <= 0 || pitch < width {
		return false
	}
	pixels := (height-1)*pitch + width
	if pixels <= 0 || pixels > ktfWIPICMaxPixelTransfer/4 {
		return false
	}
	return uint64(address)+uint64(pixels*4) <= uint64(^uint32(0))+1
}

// wipicImageColorKey derives a color-keyed image's transparent RGB565 value
// from the top-left pixel of its already-painted framebuffer, or -1 when that
// corner is not in the magenta family. It lets restored images recover their
// key without serializing it.
func (r *ktfRuntime) wipicImageColorKey(framebufferHandle uint32) int32 {
	framebuffer := r.wipicFramebuffers[framebufferHandle]
	if framebuffer == nil {
		return -1
	}
	var encoded [2]byte
	if err := r.cpu.ReadMemory(framebuffer.pixels, encoded[:]); err != nil {
		return -1
	}
	if corner := binary.LittleEndian.Uint16(encoded[:]); ktfIsColorKeyMagenta565(corner) {
		return int32(corner)
	}
	return -1
}

// ktfIsColorKeyMagenta565 reports whether an RGB565 value is in the bright
// magenta family that KTF color-keyed bitmaps use for their transparent
// background. Keying only this family, rather than any image's corner pixel,
// keeps ordinary opaque bitmaps from punching holes where a real color happens
// to repeat, while tolerating the exact shade each title picks (0xf81f, the
// 0xf816 이노티아 uses, and similar).
func ktfIsColorKeyMagenta565(v uint16) bool {
	red := (v >> 11) & 0x1f
	green := (v >> 5) & 0x3f
	blue := v & 0x1f
	return red >= 28 && green <= 4 && blue >= 16
}

func ktfWIPICRGB565(red, green, blue uint32) uint16 {
	return uint16(red&0xff)>>3<<11 |
		uint16(green&0xff)>>2<<5 |
		uint16(blue&0xff)>>3
}

func ktfWIPICRGBFrom565(pixel uint16) (uint32, uint32, uint32) {
	red := uint32(pixel>>11) & 0x1f
	green := uint32(pixel>>5) & 0x3f
	blue := uint32(pixel) & 0x1f
	return red<<3 | red>>2, green<<2 | green>>4, blue<<3 | blue>>2
}

func ktfWIPICGraphicsDecodeNextImage(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	object, err := runtime.parameter(0)
	if err != nil {
		return 0, err
	}
	imageState := runtime.wipicImages[object]
	if imageState == nil {
		return wipiReturnCode(wipiInvalid), nil
	}
	assetID := runtime.wipicAssetServices[object]
	if assetID == 0 {
		return wipiReturnCode(wipiBadFormat), nil
	}
	asset, err := runtime.services.Assets.Info(runtime.serviceOwner, assetID)
	if err != nil {
		return wipiReturnCode(wipiBadFormat), err
	}
	next := imageState.frameIndex + 1
	if len(asset.Frames) <= 1 || next >= uint32(len(asset.Frames)) {
		return wipiReturnCode(wipiImageDone), nil
	}
	transparentKey, err := runtime.paintWIPICImageFrame(
		imageState.framebuffer,
		asset.Frames[next].Surface,
	)
	if err != nil {
		return wipiReturnCode(wipiBadFormat), err
	}
	imageState.transparentKey = transparentKey
	imageState.frameIndex = next
	runtime.tracef(
		"wipic_graphics_decode_next:image=0x%08x:frame=%d/%d",
		object,
		next+1,
		len(asset.Frames),
	)
	if next+1 >= uint32(len(asset.Frames)) {
		return wipiReturnCode(wipiImageDone), nil
	}
	return wipiReturnCode(wipiImageFrameDone), nil
}

func ktfWIPICGraphicsPostEvent(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	values, err := readKTFWIPICParameters(runtime, 4, "post-event")
	if err != nil {
		return 0, err
	}
	data := make([]byte, 16)
	for index, value := range values {
		binary.LittleEndian.PutUint32(data[index*4:], value)
	}
	_, err = runtime.services.Events.Enqueue(shared.Event{
		At:    runtime.services.Clock.Monotonic(),
		Kind:  shared.EventApplication,
		Owner: runtime.serviceOwner,
		Name:  "wipic.graphics",
		Value: int64(int32(values[0])),
		Data:  data,
	})
	if errors.Is(err, shared.ErrLimitExceeded) {
		return wipiReturnCode(wipiNoMemory), nil
	}
	return 0, err
}

func ktfWIPICGraphicsDrawPolygon(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.drawWIPICPolygon(false)
}

func ktfWIPICGraphicsDrawFillPolygon(
	_ context.Context,
	runtime *ktfRuntime,
) (uint32, error) {
	return runtime.drawWIPICPolygon(true)
}

func (r *ktfRuntime) drawWIPICPolygon(fill bool) (uint32, error) {
	values, err := readKTFWIPICParameters(r, 5, "polygon")
	if err != nil {
		return 0, err
	}
	framebuffer := r.wipicFramebuffers[values[0]]
	count := int(int32(values[3]))
	if framebuffer == nil || values[1] == 0 || values[2] == 0 ||
		count <= 0 || count > ktfWIPICMaxPolygonPoints {
		return 0, nil
	}
	state, err := r.wipicGraphicsContext(values[4])
	if err != nil {
		return 0, err
	}
	xCoordinates, err := r.readWIPICCoordinates(
		values[1],
		count,
		int64(state.offsetX),
	)
	if err != nil {
		return 0, err
	}
	yCoordinates, err := r.readWIPICCoordinates(
		values[2],
		count,
		int64(state.offsetY),
	)
	if err != nil {
		return 0, err
	}
	visibleLeft, visibleTop, visibleRight, visibleBottom :=
		ktfWIPICVisibleBounds(framebuffer, state)
	if fill && count >= 3 &&
		visibleLeft < visibleRight && visibleTop < visibleBottom {
		minimumY, maximumY := yCoordinates[0], yCoordinates[0]
		for _, coordinate := range yCoordinates[1:] {
			minimumY = min(minimumY, coordinate)
			maximumY = max(maximumY, coordinate)
		}
		minimumY = max(minimumY, visibleTop)
		maximumY = min(maximumY, visibleBottom-1)
		nodes := make([]int64, 0, count)
		for row := minimumY; row <= maximumY; row++ {
			nodes = nodes[:0]
			previous := count - 1
			for index := 0; index < count; index++ {
				currentY, previousY := yCoordinates[index], yCoordinates[previous]
				if (currentY <= row && previousY > row) ||
					(previousY <= row && currentY > row) {
					position := float64(xCoordinates[index]) +
						float64(row-currentY)*
							float64(xCoordinates[previous]-xCoordinates[index])/
							float64(previousY-currentY)
					nodes = append(nodes, int64(position))
				}
				previous = index
			}
			sort.Slice(nodes, func(left, right int) bool {
				return nodes[left] < nodes[right]
			})
			for index := 0; index+1 < len(nodes); index += 2 {
				start := max(visibleLeft, nodes[index])
				end := min(visibleRight-1, nodes[index+1])
				for column := start; column <= end; column++ {
					if err := r.writeWIPICPixel(
						values[0],
						int(column),
						int(row),
						state,
					); err != nil {
						return 0, err
					}
				}
			}
		}
	}
	if count > 1 {
		for index := 0; index < count; index++ {
			next := (index + 1) % count
			if err := r.drawWIPICLine(
				values[0],
				int(xCoordinates[index]),
				int(yCoordinates[index]),
				int(xCoordinates[next]),
				int(yCoordinates[next]),
				state,
			); err != nil {
				return 0, err
			}
		}
	}
	return 0, r.commitKTFWIPICFramebuffer(values[0])
}

func (r *ktfRuntime) readWIPICCoordinates(
	address uint32,
	count int,
	offset int64,
) ([]int64, error) {
	encoded := make([]byte, count*4)
	if err := r.cpu.ReadMemory(address, encoded); err != nil {
		return nil, err
	}
	result := make([]int64, count)
	for index := range result {
		result[index] = int64(int32(binary.LittleEndian.Uint32(
			encoded[index*4:],
		))) + offset
	}
	return result, nil
}

func ktfWIPICVisibleBounds(
	framebuffer *ktfWIPICFramebuffer,
	state ktfWIPICGraphicsContext,
) (int64, int64, int64, int64) {
	left, top := int64(0), int64(0)
	right, bottom := int64(framebuffer.width), int64(framebuffer.height)
	if state.clipEnabled {
		left = max(left, int64(state.left))
		top = max(top, int64(state.top))
		right = min(right, int64(state.right))
		bottom = min(bottom, int64(state.bottom))
	}
	return left, top, right, bottom
}

// clipWIPICLine applies Liang-Barsky clipping before Bresenham rasterization.
// Besides matching the context rectangle, this bounds work when a client hands
// the provider extreme off-screen coordinates.
func (r *ktfRuntime) clipWIPICLine(
	handle uint32,
	x1, y1, x2, y2 int,
	state ktfWIPICGraphicsContext,
) (int, int, int, int, bool) {
	framebuffer := r.wipicFramebuffers[handle]
	if framebuffer == nil {
		return 0, 0, 0, 0, false
	}
	left, top, right, bottom := ktfWIPICVisibleBounds(framebuffer, state)
	if left >= right || top >= bottom {
		return 0, 0, 0, 0, false
	}
	x0, y0 := float64(x1), float64(y1)
	dx, dy := float64(x2)-x0, float64(y2)-y0
	u1, u2 := 0.0, 1.0
	parameters := [4]float64{-dx, dx, -dy, dy}
	distances := [4]float64{
		x0 - float64(left),
		float64(right-1) - x0,
		y0 - float64(top),
		float64(bottom-1) - y0,
	}
	for index, parameter := range parameters {
		distance := distances[index]
		if parameter == 0 {
			if distance < 0 {
				return 0, 0, 0, 0, false
			}
			continue
		}
		ratio := distance / parameter
		if parameter < 0 {
			u1 = max(u1, ratio)
		} else {
			u2 = min(u2, ratio)
		}
		if u1 > u2 {
			return 0, 0, 0, 0, false
		}
	}
	clampX := func(value int) int {
		return max(int(left), min(int(right-1), value))
	}
	clampY := func(value int) int {
		return max(int(top), min(int(bottom-1), value))
	}
	return clampX(int(math.Round(x0 + u1*dx))),
		clampY(int(math.Round(y0 + u1*dy))),
		clampX(int(math.Round(x0 + u2*dx))),
		clampY(int(math.Round(y0 + u2*dy))),
		true
}
