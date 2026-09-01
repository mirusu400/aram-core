package system

import "fmt"

const (
	qualcommLegacyUARTWindowSize = uint32(0x3c)

	qualcommLegacyUARTStatusOffset = uint32(0x08)
	qualcommLegacyUARTFIFOOffset   = uint32(0x0c)
	qualcommLegacyUARTMISROffset   = uint32(0x10)
	qualcommLegacyUARTISROffset    = uint32(0x14)

	qualcommLegacyUARTStatusRXReady = uint32(1 << 0)
	qualcommLegacyUARTStatusTXReady = uint32(1 << 2)
	qualcommLegacyUARTStatusTXEmpty = uint32(1 << 3)
)

var qualcommLegacyUARTHalfwordRegisterOffsets = [...]uint32{
	0x00, 0x04, 0x08,
	0x10, 0x14, 0x18, 0x1c,
	0x20, 0x24, 0x28, 0x2c,
	0x30, 0x34, 0x38,
}

func (d *QualcommBootControl) legacyUARTOffset(offset uint32) (uint32, bool) {
	for base := range d.legacyUARTControllers {
		if offset >= base && offset < base+qualcommLegacyUARTWindowSize {
			return offset - base, true
		}
	}
	return 0, false
}

func (d *QualcommBootControl) readLegacyUART(offset uint32, width Width) (uint32, bool, error) {
	relative, configured := d.legacyUARTOffset(offset)
	if !configured {
		return 0, false, nil
	}
	_, wordConfigured := d.mixedWidthOffsets[offset]
	switch relative {
	case qualcommLegacyUARTStatusOffset:
		if width != Width8 && width != Width16 && (width != Width32 || !wordConfigured) {
			return 0, true, fmt.Errorf(
				"%w: legacy UART status read%d at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				offset,
			)
		}
		// The deterministic offline endpoint has no receive data. Its
		// transmitter consumes bytes immediately and remains ready/empty.
		return qualcommLegacyUARTStatusTXReady | qualcommLegacyUARTStatusTXEmpty, true, nil
	case qualcommLegacyUARTFIFOOffset:
		if width != Width8 {
			return 0, true, fmt.Errorf(
				"%w: legacy UART FIFO read%d at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				offset,
			)
		}
		return 0, true, nil
	case qualcommLegacyUARTMISROffset, qualcommLegacyUARTISROffset:
		if width == Width8 || width == Width16 || width == Width32 && wordConfigured {
			return 0, true, nil
		}
		return 0, true, fmt.Errorf(
			"%w: legacy UART interrupt-status read%d at 0x%x",
			ErrQualcommBootControlMMIO,
			width*8,
			offset,
		)
	default:
		return 0, false, nil
	}
}

func (d *QualcommBootControl) writeLegacyUART(offset uint32, width Width, value uint32) (bool, error) {
	relative, configured := d.legacyUARTOffset(offset)
	if !configured || relative != qualcommLegacyUARTFIFOOffset {
		return false, nil
	}
	if width != Width8 || value > 0xff {
		return true, fmt.Errorf(
			"%w: legacy UART FIFO write%d value 0x%x at 0x%x",
			ErrQualcommBootControlMMIO,
			width*8,
			value,
			offset,
		)
	}
	// No host endpoint is attached yet. Accepting the byte models an empty
	// transmit FIFO without inventing receive data or an external peer.
	return true, nil
}
