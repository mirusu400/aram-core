package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

const (
	dcsExitSleepMode       = 0x11
	dcsEnterSleepMode      = 0x10
	dcsSetDisplayOff       = 0x28
	dcsSetDisplayOn        = 0x29
	dcsSetColumnAddress    = 0x2a
	dcsSetPageAddress      = 0x2b
	dcsWriteMemoryStart    = 0x2c
	dcsSetAddressMode      = 0x36
	dcsSetPixelFormat      = 0x3a
	dcsWriteMemoryContinue = 0x3c

	dcsAddressModePageReverse   = 1 << 7
	dcsAddressModeColumnReverse = 1 << 6
	dcsAddressModeExchangeAxes  = 1 << 5
	dcsAddressModeBGR           = 1 << 3
	dcsPixelFormatDBIRGB565     = 0x05
	dcsPixelFormatRGB565        = 0x55
)

var ErrDCSPanel = errors.New("invalid DCS panel stream")

type DCSPanelConfig struct {
	Width             uint16
	Height            uint16
	NativeAddressMode uint8
}

// DCSPanelController decodes the display-command subset shared by parallel
// DBI-style RGB565 panels. Vendor initialization commands are accepted and
// ignored; address windows, orientation, power state, and memory writes are
// modeled explicitly.
type DCSPanelController struct {
	width             uint16
	height            uint16
	nativeAddressMode uint8
	currentCommand    uint16
	parameters        [4]uint8
	parameterCount    uint8
	columnStart       uint16
	columnEnd         uint16
	pageStart         uint16
	pageEnd           uint16
	cursorColumn      uint16
	cursorPage        uint16
	addressMode       uint8
	pixelFormat       uint8
	sleepOut          bool
	displayOn         bool
	memoryWritePixels uint32
	pixelWrites       uint64
	updateSequence    uint64
	pixels            []uint16
}

func NewDCSPanelController(config DCSPanelConfig) (*DCSPanelController, error) {
	pixelCount, err := validateDCSPanelConfig(config)
	if err != nil {
		return nil, err
	}
	controller := &DCSPanelController{
		width:             config.Width,
		height:            config.Height,
		nativeAddressMode: config.NativeAddressMode,
		pixels:            make([]uint16, int(pixelCount)),
	}
	_ = controller.Reset()
	return controller, nil
}

func validateDCSPanelConfig(config DCSPanelConfig) (uint64, error) {
	const maximumPixels = 1 << 24
	pixelCount := uint64(config.Width) * uint64(config.Height)
	if config.Width == 0 || config.Height == 0 || pixelCount > maximumPixels {
		return 0, fmt.Errorf("%w: invalid dimensions %dx%d", ErrDCSPanel, config.Width, config.Height)
	}
	return pixelCount, nil
}

func (p *DCSPanelController) Reset() error {
	p.currentCommand = 0
	p.parameters = [4]uint8{}
	p.parameterCount = 0
	p.columnStart = 0
	p.columnEnd = p.width - 1
	p.pageStart = 0
	p.pageEnd = p.height - 1
	p.cursorColumn = 0
	p.cursorPage = 0
	p.addressMode = 0
	p.pixelFormat = 0
	p.sleepOut = false
	p.displayOn = false
	p.memoryWritePixels = 0
	p.pixelWrites = 0
	p.updateSequence = 0
	clear(p.pixels)
	return nil
}

func (p *DCSPanelController) WriteCommand(command uint16) error {
	p.finishMemoryWrite()
	p.currentCommand = command
	p.parameters = [4]uint8{}
	p.parameterCount = 0
	p.memoryWritePixels = 0
	switch command {
	case dcsEnterSleepMode:
		p.sleepOut = false
	case dcsExitSleepMode:
		p.sleepOut = true
	case dcsSetDisplayOff:
		p.displayOn = false
	case dcsSetDisplayOn:
		p.displayOn = true
	case dcsWriteMemoryStart:
		p.cursorColumn = p.columnStart
		p.cursorPage = p.pageStart
	case dcsWriteMemoryContinue:
		// Continue from the cursor left by the previous memory-write command.
	}
	return nil
}

func (p *DCSPanelController) WriteData(value uint16) error {
	switch p.currentCommand {
	case dcsSetColumnAddress, dcsSetPageAddress:
		if value > 0xff || p.parameterCount >= uint8(len(p.parameters)) {
			return fmt.Errorf(
				"%w: command 0x%x parameter %d value 0x%x",
				ErrDCSPanel,
				p.currentCommand,
				p.parameterCount,
				value,
			)
		}
		p.parameters[p.parameterCount] = uint8(value)
		p.parameterCount++
		if p.parameterCount == uint8(len(p.parameters)) {
			start := uint16(p.parameters[0])<<8 | uint16(p.parameters[1])
			end := uint16(p.parameters[2])<<8 | uint16(p.parameters[3])
			if start > end || p.currentCommand == dcsSetColumnAddress && end >= p.width ||
				p.currentCommand == dcsSetPageAddress && end >= p.height {
				return fmt.Errorf(
					"%w: command 0x%x window %d..%d exceeds %dx%d",
					ErrDCSPanel,
					p.currentCommand,
					start,
					end,
					p.width,
					p.height,
				)
			}
			if p.currentCommand == dcsSetColumnAddress {
				p.columnStart, p.columnEnd = start, end
			} else {
				p.pageStart, p.pageEnd = start, end
			}
		}
		return nil
	case dcsSetAddressMode:
		if value > 0xff || p.parameterCount != 0 {
			return fmt.Errorf("%w: address-mode value 0x%x", ErrDCSPanel, value)
		}
		p.addressMode = uint8(value)
		p.parameterCount = 1
		return nil
	case dcsSetPixelFormat:
		if value > 0xff || p.parameterCount != 0 {
			return fmt.Errorf("%w: pixel-format value 0x%x", ErrDCSPanel, value)
		}
		p.pixelFormat = uint8(value)
		p.parameterCount = 1
		return nil
	case dcsWriteMemoryStart, dcsWriteMemoryContinue:
		return p.writePixel(value)
	default:
		// Vendor commands are controller-specific and do not change the common
		// DCS framebuffer contract.
		return nil
	}
}

func (p *DCSPanelController) writePixel(value uint16) error {
	if p.pixelFormat != dcsPixelFormatDBIRGB565 && p.pixelFormat != dcsPixelFormatRGB565 {
		return fmt.Errorf("%w: memory write with pixel format 0x%x", ErrDCSPanel, p.pixelFormat)
	}
	x, y, ok := p.physicalCoordinate(p.cursorColumn, p.cursorPage)
	if !ok {
		return fmt.Errorf(
			"%w: transformed coordinate %d,%d for mode 0x%x",
			ErrDCSPanel,
			p.cursorColumn,
			p.cursorPage,
			p.addressMode,
		)
	}
	if p.effectiveAddressMode()&dcsAddressModeBGR != 0 {
		value = swapRGB565RedBlue(value)
	}
	p.pixels[int(y)*int(p.width)+int(x)] = value
	p.pixelWrites++
	p.memoryWritePixels++
	if p.cursorColumn < p.columnEnd {
		p.cursorColumn++
	} else {
		p.cursorColumn = p.columnStart
		if p.cursorPage < p.pageEnd {
			p.cursorPage++
		} else {
			p.cursorPage = p.pageStart
		}
	}
	return nil
}

func (p *DCSPanelController) physicalCoordinate(column, page uint16) (uint16, uint16, bool) {
	mode := p.effectiveAddressMode()
	x, y := column, page
	if mode&dcsAddressModeExchangeAxes != 0 {
		x, y = y, x
	}
	if x >= p.width || y >= p.height {
		return 0, 0, false
	}
	if mode&dcsAddressModeColumnReverse != 0 {
		x = p.width - 1 - x
	}
	if mode&dcsAddressModePageReverse != 0 {
		y = p.height - 1 - y
	}
	return x, y, true
}

func (p *DCSPanelController) effectiveAddressMode() uint8 {
	return p.addressMode ^ p.nativeAddressMode
}

func swapRGB565RedBlue(value uint16) uint16 {
	return value&0x07e0 | value>>11 | (value&0x001f)<<11
}

func (p *DCSPanelController) finishMemoryWrite() {
	if (p.currentCommand == dcsWriteMemoryStart || p.currentCommand == dcsWriteMemoryContinue) &&
		p.memoryWritePixels != 0 {
		p.updateSequence++
	}
}

func (p *DCSPanelController) Dimensions() (width, height uint16) {
	return p.width, p.height
}

func (p *DCSPanelController) AddressWindow() (columnStart, columnEnd, pageStart, pageEnd uint16) {
	return p.columnStart, p.columnEnd, p.pageStart, p.pageEnd
}

func (p *DCSPanelController) PowerState() (sleepOut, displayOn bool) {
	return p.sleepOut, p.displayOn
}

func (p *DCSPanelController) FormatState() (addressMode, pixelFormat uint8) {
	return p.addressMode, p.pixelFormat
}

func (p *DCSPanelController) WriteCounts() (pixels, updates uint64) {
	updates = p.updateSequence
	if (p.currentCommand == dcsWriteMemoryStart || p.currentCommand == dcsWriteMemoryContinue) &&
		p.memoryWritePixels != 0 {
		updates++
	}
	return p.pixelWrites, updates
}

func (p *DCSPanelController) FrameRGB565() []uint16 {
	return append([]uint16(nil), p.pixels...)
}

func (p *DCSPanelController) FrameRGBA() *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, int(p.width), int(p.height)))
	for y := 0; y < int(p.height); y++ {
		for x := 0; x < int(p.width); x++ {
			value := p.pixels[y*int(p.width)+x]
			red := uint8(value >> 11)
			green := uint8(value >> 5 & 0x3f)
			blue := uint8(value & 0x1f)
			frame.SetRGBA(x, y, color.RGBA{
				R: red<<3 | red>>2,
				G: green<<2 | green>>4,
				B: blue<<3 | blue>>2,
				A: 0xff,
			})
		}
	}
	return frame
}

func (p *DCSPanelController) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("DCSP")
	_ = binary.Write(&output, binary.LittleEndian, uint32(2))
	_ = binary.Write(&output, binary.LittleEndian, p.width)
	_ = binary.Write(&output, binary.LittleEndian, p.height)
	_ = output.WriteByte(p.nativeAddressMode)
	_ = binary.Write(&output, binary.LittleEndian, p.currentCommand)
	_ = output.WriteByte(p.parameterCount)
	_, _ = output.Write(p.parameters[:])
	for _, value := range []uint16{
		p.columnStart, p.columnEnd, p.pageStart, p.pageEnd,
		p.cursorColumn, p.cursorPage,
	} {
		_ = binary.Write(&output, binary.LittleEndian, value)
	}
	_ = output.WriteByte(p.addressMode)
	_ = output.WriteByte(p.pixelFormat)
	flags := uint8(0)
	if p.sleepOut {
		flags |= 1
	}
	if p.displayOn {
		flags |= 2
	}
	_ = output.WriteByte(flags)
	_ = binary.Write(&output, binary.LittleEndian, p.memoryWritePixels)
	_ = binary.Write(&output, binary.LittleEndian, p.pixelWrites)
	_ = binary.Write(&output, binary.LittleEndian, p.updateSequence)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(p.pixels)))
	for _, pixel := range p.pixels {
		_ = binary.Write(&output, binary.LittleEndian, pixel)
	}
	return output.Bytes(), nil
}

func (p *DCSPanelController) LoadState(state []byte) error {
	const headerSize = 59
	if len(state) < headerSize {
		return ErrInvalidState
	}
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	var width, height, currentCommand uint16
	var nativeAddressMode uint8
	var parameterCount uint8
	var parameters [4]uint8
	var columnStart, columnEnd, pageStart, pageEnd, cursorColumn, cursorPage uint16
	var addressMode, pixelFormat, flags uint8
	var memoryWritePixels uint32
	var pixelWrites, updateSequence uint64
	var pixelCount uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "DCSP" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 2 ||
		binary.Read(reader, binary.LittleEndian, &width) != nil || width != p.width ||
		binary.Read(reader, binary.LittleEndian, &height) != nil || height != p.height ||
		binary.Read(reader, binary.LittleEndian, &nativeAddressMode) != nil ||
		nativeAddressMode != p.nativeAddressMode ||
		binary.Read(reader, binary.LittleEndian, &currentCommand) != nil ||
		binary.Read(reader, binary.LittleEndian, &parameterCount) != nil ||
		parameterCount > uint8(len(parameters)) {
		return ErrInvalidState
	}
	if _, err := io.ReadFull(reader, parameters[:]); err != nil ||
		binary.Read(reader, binary.LittleEndian, &columnStart) != nil ||
		binary.Read(reader, binary.LittleEndian, &columnEnd) != nil ||
		binary.Read(reader, binary.LittleEndian, &pageStart) != nil ||
		binary.Read(reader, binary.LittleEndian, &pageEnd) != nil ||
		binary.Read(reader, binary.LittleEndian, &cursorColumn) != nil ||
		binary.Read(reader, binary.LittleEndian, &cursorPage) != nil ||
		binary.Read(reader, binary.LittleEndian, &addressMode) != nil ||
		binary.Read(reader, binary.LittleEndian, &pixelFormat) != nil ||
		binary.Read(reader, binary.LittleEndian, &flags) != nil || flags&^uint8(3) != 0 ||
		binary.Read(reader, binary.LittleEndian, &memoryWritePixels) != nil ||
		binary.Read(reader, binary.LittleEndian, &pixelWrites) != nil ||
		binary.Read(reader, binary.LittleEndian, &updateSequence) != nil ||
		binary.Read(reader, binary.LittleEndian, &pixelCount) != nil ||
		pixelCount != uint32(len(p.pixels)) || reader.Len() != int(pixelCount)*2 ||
		columnStart > columnEnd || columnEnd >= p.width ||
		pageStart > pageEnd || pageEnd >= p.height ||
		cursorColumn < columnStart || cursorColumn > columnEnd ||
		cursorPage < pageStart || cursorPage > pageEnd ||
		memoryWritePixels != 0 && currentCommand != dcsWriteMemoryStart &&
			currentCommand != dcsWriteMemoryContinue {
		return ErrInvalidState
	}
	pixels := make([]uint16, pixelCount)
	for index := range pixels {
		if binary.Read(reader, binary.LittleEndian, &pixels[index]) != nil {
			return ErrInvalidState
		}
	}
	if reader.Len() != 0 {
		return ErrInvalidState
	}
	p.currentCommand = currentCommand
	p.parameterCount = parameterCount
	p.parameters = parameters
	p.columnStart, p.columnEnd = columnStart, columnEnd
	p.pageStart, p.pageEnd = pageStart, pageEnd
	p.cursorColumn, p.cursorPage = cursorColumn, cursorPage
	p.addressMode = addressMode
	p.pixelFormat = pixelFormat
	p.sleepOut = flags&1 != 0
	p.displayOn = flags&2 != 0
	p.memoryWritePixels = memoryWritePixels
	p.pixelWrites = pixelWrites
	p.updateSequence = updateSequence
	p.pixels = pixels
	return nil
}

var _ ParallelPanelController = (*DCSPanelController)(nil)
