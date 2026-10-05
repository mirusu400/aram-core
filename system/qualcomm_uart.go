package system

import "fmt"

// QualcommLegacyUARTReceiveData describes one deterministic receive FIFO. It is
// used for peripherals such as a smart card which answer the guest's initial
// receiver-enable command before the guest has transmitted a byte.
type QualcommLegacyUARTReceiveData struct {
	Controller                uint32
	InterruptSource           uint8
	UseVectoredController     bool
	VectoredGroupStatusOffset uint32
	VectoredGroupMask         uint32
	DelayInstructions         uint64
	ReceiveCommand            uint32
	ActivationCommand         uint32
	// ReceiveFIFOOffset selects the read-side receive aperture. Legacy UART
	// blocks expose RX at +0x0c, while UARTDM aliases its command register at
	// +0x10 on writes and its receive FIFO at the same offset on reads.
	// Zero retains the legacy +0x0c layout.
	ReceiveFIFOOffset     uint32
	PulseReceiveInterrupt bool
	EchoTransmit          bool
	T0Card                bool
	TransmitFrameBytes    uint32
	TransmitResponse      []byte
	Data                  []byte
}

func (d QualcommLegacyUARTReceiveData) receiveCommand() uint32 {
	if d.ReceiveCommand != 0 {
		return d.ReceiveCommand
	}
	return qualcommLegacyUARTCommandRXEnable
}

func (d QualcommLegacyUARTReceiveData) receiveFIFOOffset() uint32 {
	if d.ReceiveFIFOOffset != 0 {
		return d.ReceiveFIFOOffset
	}
	return qualcommLegacyUARTFIFOOffset
}

const (
	qualcommLegacyUARTWindowSize = uint32(0x3c)

	qualcommLegacyUARTStatusOffset = uint32(0x08)
	qualcommLegacyUARTFIFOOffset   = uint32(0x0c)
	qualcommLegacyUARTMISROffset   = uint32(0x10)
	qualcommLegacyUARTISROffset    = uint32(0x14)

	qualcommLegacyUARTStatusRXReady = uint32(1 << 0)
	qualcommLegacyUARTStatusTXReady = uint32(1 << 2)
	qualcommLegacyUARTStatusTXEmpty = uint32(1 << 3)

	qualcommLegacyUARTInterruptRXStale = uint32(1 << 3)

	qualcommLegacyUARTCommandRXEnable     = uint32(1 << 0)
	qualcommLegacyUARTCommandResetRXStale = uint32(1 << 4)
)

var qualcommLegacyUARTHalfwordRegisterOffsets = [...]uint32{
	0x00, 0x04, 0x08,
	0x10, 0x14, 0x18, 0x1c,
	0x20, 0x24, 0x28, 0x2c,
	0x30, 0x34, 0x38,
}

func (d *QualcommBootControl) legacyUARTOffset(offset uint32) (uint32, uint32, bool) {
	for base := range d.legacyUARTControllers {
		if offset >= base && offset < base+qualcommLegacyUARTWindowSize {
			return base, offset - base, true
		}
	}
	return 0, 0, false
}

func (d *QualcommBootControl) legacyUARTReceiveProfile(
	controller uint32,
) (QualcommLegacyUARTReceiveData, bool) {
	for _, receive := range d.legacyUARTReceiveData {
		if receive.Controller == controller {
			return receive, true
		}
	}
	return QualcommLegacyUARTReceiveData{}, false
}

func (d *QualcommBootControl) legacyUARTRawInterruptStatus(controller uint32) uint32 {
	if len(d.legacyUARTReceiveQueues[controller]) == 0 ||
		!d.legacyUARTReceiveIRQPending[controller] {
		return 0
	}
	// A finite scripted response is complete as soon as it is published, so
	// expose RXSTALE rather than waiting for the receive watermark. This is the
	// condition used by the Qualcomm driver to drain a short ATR or APDU reply.
	return qualcommLegacyUARTInterruptRXStale
}

func legacyUARTT0CommandHasOutgoingData(instruction byte) bool {
	switch instruction {
	case 0x10, // TERMINAL PROFILE
		0x20, // VERIFY
		0x24, // CHANGE CHV
		0x26, // DISABLE CHV
		0x28, // ENABLE CHV
		0x2c, // UNBLOCK CHV
		0x32, // INCREASE
		0x88, // RUN GSM ALGORITHM / AUTHENTICATE
		0xa2, // SEEK
		0xa4, // SELECT
		0xd6, // UPDATE BINARY
		0xdc: // UPDATE RECORD
		return true
	default:
		return false
	}
}

func validLegacyUARTT0TransmitBuffer(
	receive QualcommLegacyUARTReceiveData,
	buffer []byte,
) bool {
	if !receive.T0Card {
		return len(buffer) == 0
	}
	if len(buffer) < 5 {
		return true
	}
	length := int(buffer[4])
	if length == 0 {
		length = 256
	}
	return legacyUARTT0CommandHasOutgoingData(buffer[1]) && len(buffer) < 5+length
}

var legacyUARTT0MasterFileFCP = []byte{
	0x62, 0x26,
	0x82, 0x02, 0x78, 0x21,
	0x83, 0x02, 0x3f, 0x00,
	0xa5, 0x03, 0x80, 0x01, 0x71,
	0x8a, 0x01, 0x05,
	0x8b, 0x03, 0x2f, 0x06, 0x01,
	0xc6, 0x0f, 0x90, 0x01, 0x70,
	0x83, 0x01, 0x01,
	0x83, 0x01, 0x81,
	0x83, 0x01, 0x0a,
	0x83, 0x01, 0x0b,
}

var legacyUARTT0ICCIDFCP = []byte{
	0x62, 0x19,
	0x82, 0x02, 0x41, 0x21,
	0x83, 0x02, 0x2f, 0xe2,
	0xa5, 0x03, 0x80, 0x01, 0x71,
	0x8a, 0x01, 0x05,
	0x8b, 0x03, 0x2f, 0x06, 0x01,
	0x80, 0x02, 0x00, 0x0a,
}

var legacyUARTT0DirectoryFCP = []byte{
	0x62, 0x1f,
	0x82, 0x05, 0x42, 0x21, 0x00, 0x26, 0x02,
	0x83, 0x02, 0x2f, 0x00,
	0xa5, 0x03, 0x80, 0x01, 0x71,
	0x8a, 0x01, 0x05,
	0x8b, 0x03, 0x2f, 0x06, 0x01,
	0x80, 0x02, 0x00, 0x4c,
	0x88, 0x01, 0xf0,
}

var legacyUARTT0AccessRuleFCP = []byte{
	0x62, 0x15,
	0x82, 0x05, 0x42, 0x21, 0x00, 0x20, 0x01,
	0x83, 0x02, 0x2f, 0x06,
	0x8a, 0x01, 0x05,
	0x80, 0x02, 0x00, 0x20,
	0x88, 0x01, 0x06,
}

var legacyUARTT0USIMAID = []byte{
	0xa0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xff,
	0xff, 0xff, 0xff, 0x89, 0x07, 0x09, 0x00, 0x00,
}

func legacyUARTT0ApplicationFCP() []byte {
	response := []byte{
		0x62, 0x34,
		0x82, 0x02, 0x78, 0x21,
		0x84, 0x10,
	}
	response = append(response, legacyUARTT0USIMAID...)
	return append(response,
		0xa5, 0x03, 0x80, 0x01, 0x71,
		0x8a, 0x01, 0x05,
		0x8b, 0x03, 0x6f, 0x06, 0x01,
		0xc6, 0x0f, 0x90, 0x01, 0x70,
		0x83, 0x01, 0x01,
		0x83, 0x01, 0x81,
		0x83, 0x01, 0x0a,
		0x83, 0x01, 0x0b,
	)
}

func legacyUARTT0TransparentFCP(file uint16, size uint16) []byte {
	return []byte{
		0x62, 0x1c,
		0x82, 0x02, 0x41, 0x21,
		0x83, 0x02, byte(file >> 8), byte(file),
		0xa5, 0x03, 0xc0, 0x01, 0x40,
		0x8a, 0x01, 0x05,
		0x8b, 0x03, 0x6f, 0x06, 0x01,
		0x80, 0x02, byte(size >> 8), byte(size),
		0x88, 0x01, byte(file) << 3,
	}
}

// legacyUARTT0FileData returns a coherent Korean SK Telecom USIM identity for
// the subscriber files read by the Samsung MSM6280 firmware family.  Keeping
// these values together also makes the FCP size and READ BINARY contents agree;
// several Samsung GSDI builds reject a card whose EFAD length/MNC encoding does
// not match EFIMSI even though the transport itself completed successfully.
func legacyUARTT0FileData(file uint16) ([]byte, bool) {
	switch file {
	case 0x2fe2: // EFICCID: 8982051508130128888F (era-appropriate SK Telecom format).
		return []byte{
			0x98, 0x28, 0x50, 0x51, 0x80,
			0x31, 0x10, 0x82, 0x88, 0xf8,
		}, true
	case 0x6f07: // EFIMSI: 450051234567890 (MCC 450, MNC 05).
		return []byte{
			0x08, 0x94, 0x05, 0x50, 0x21,
			0x43, 0x65, 0x87, 0x09,
		}, true
	case 0x6fad: // EFAD: normal operation and a two-digit MNC.
		return []byte{0x00, 0x00, 0x00, 0x02}, true
	case 0x6f38: // EFUST: advertise the services queried by these firmwares.
		return []byte{
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		}, true
	case 0x6f78: // EFACC: access class 0.
		return []byte{0x00, 0x01}, true
	case 0x6f31: // EFHPPLMN: search the home network every ten minutes.
		return []byte{0x0a}, true
	case 0x6f7e: // EFLOCI: registered on SKT PLMN 450-05, LAC 1.
		return []byte{
			0xff, 0xff, 0xff, 0xff,
			0x54, 0xf0, 0x50, 0x00, 0x01,
			0xff, 0x00,
		}, true
	case 0x6f73: // EFPSLOCI: no P-TMSI yet, on SKT routing area 1.
		return []byte{
			0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff,
			0x54, 0xf0, 0x50, 0x00, 0x01, 0x01,
			0x00,
		}, true
	case 0x6f08, 0x6f09: // EFKeys / EFKeysPS: authentication not yet run.
		return []byte{
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff,
		}, true
	case 0x6f7b: // EFFPLMN: no forbidden PLMNs.
		return []byte{
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		}, true
	case 0x6f5b: // EFSTART-HFN.
		return []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, true
	case 0x6f5c: // EFTHRESHOLD.
		return []byte{0x00, 0x00, 0x00}, true
	default:
		return nil, false
	}
}

func legacyUARTT0FileAbsent(file uint16) bool {
	switch file {
	case 0x6f2c, // EFDCK
		0x6f32, // EFCNL
		0x6f3e, // EFGID1
		0x6f3f: // EFGID2
		return true
	default:
		return false
	}
}

func legacyUARTT0FileFCP(file uint16) []byte {
	switch file {
	case 0x3f00:
		return legacyUARTT0MasterFileFCP
	case 0x2fe2:
		return legacyUARTT0ICCIDFCP
	case 0x2f00:
		return legacyUARTT0DirectoryFCP
	case 0x2f06:
		return legacyUARTT0AccessRuleFCP
	case 0x7fff:
		return legacyUARTT0ApplicationFCP()
	case 0x6f16:
		return legacyUARTT0TransparentFCP(file, 16)
	default:
		if data, ok := legacyUARTT0FileData(file); ok {
			return legacyUARTT0TransparentFCP(file, uint16(len(data)))
		}
		return legacyUARTT0TransparentFCP(file, 32)
	}
}

func legacyUARTT0FileSize(file uint16) int {
	switch file {
	case 0x6f16:
		return 16
	default:
		if data, ok := legacyUARTT0FileData(file); ok {
			return len(data)
		}
		return 32
	}
}

func legacyUARTT0SelectedFile(frame []byte) uint16 {
	if len(frame) < 7 || frame[1] != 0xa4 {
		return 0
	}
	if frame[2] == 0x04 {
		return 0x7fff
	}
	return uint16(frame[len(frame)-2])<<8 | uint16(frame[len(frame)-1])
}

func legacyUARTT0DirectoryRecord() []byte {
	record := []byte{0x61, 0x12, 0x4f, 0x10}
	record = append(record, legacyUARTT0USIMAID...)
	for len(record) < 38 {
		record = append(record, 0xff)
	}
	return record
}

func (d *QualcommBootControl) legacyUARTT0ResponseData(
	controller uint32,
	frame []byte,
	length int,
) []byte {
	response := make([]byte, length)
	if len(frame) < 2 {
		return response
	}
	switch frame[1] {
	case 0xb0: // READ BINARY
		if data, ok := legacyUARTT0FileData(
			d.legacyUARTT0SelectedFiles[controller],
		); ok {
			offset := 0
			if len(frame) >= 4 {
				offset = int(frame[2])<<8 | int(frame[3])
			}
			if offset < len(data) {
				copy(response, data[offset:])
			}
		}
	case 0xb2: // READ RECORD
		if d.legacyUARTT0SelectedFiles[controller] == 0x2f00 {
			if len(frame) >= 3 && frame[2] == 1 {
				copy(response, legacyUARTT0DirectoryRecord())
			} else {
				for index := range response {
					response[index] = 0xff
				}
			}
		}
	case 0xc0:
		copy(response, d.legacyUARTT0PendingResponses[controller])
		delete(d.legacyUARTT0PendingResponses, controller)
	case 0xf2: // STATUS
		copy(response, legacyUARTT0ApplicationFCP())
	}
	return response
}

func (d *QualcommBootControl) appendLegacyUARTT0Status(
	controller uint32,
	frame []byte,
) {
	status := []byte{0x90, 0x00}
	// ISO 7816 SELECT encodes the requested response template in P2 bits 3-2.
	// 0x0c explicitly requests no response data, so only return the status
	// words.  Treating those bits as the low two bits incorrectly advertised an
	// FCP with 61xx and leaves Samsung's UIM task waiting for a response it did
	// not request.
	if len(frame) >= 4 && frame[1] == 0xa4 && frame[3]&0x0c != 0x0c {
		selected := legacyUARTT0SelectedFile(frame)
		if legacyUARTT0FileAbsent(selected) {
			delete(d.legacyUARTT0PendingResponses, controller)
			d.legacyUARTT0SelectedFiles[controller] = 0
			status = []byte{0x6a, 0x82}
			d.legacyUARTReceiveQueues[controller] = append(
				d.legacyUARTReceiveQueues[controller],
				status...,
			)
			return
		}
		d.legacyUARTT0SelectedFiles[controller] = selected
		response := append([]byte(nil), legacyUARTT0FileFCP(selected)...)
		d.legacyUARTT0PendingResponses[controller] = response
		status = []byte{0x61, byte(len(response))}
	} else if len(frame) >= 2 && frame[1] == 0xa4 {
		selected := legacyUARTT0SelectedFile(frame)
		if legacyUARTT0FileAbsent(selected) {
			d.legacyUARTT0SelectedFiles[controller] = 0
			status = []byte{0x6a, 0x82}
		} else {
			d.legacyUARTT0SelectedFiles[controller] = selected
		}
		delete(d.legacyUARTT0PendingResponses, controller)
	}
	d.legacyUARTReceiveQueues[controller] = append(
		d.legacyUARTReceiveQueues[controller],
		status...,
	)
}

func (d *QualcommBootControl) writeLegacyUARTT0(
	controller uint32,
	value byte,
) {
	frame := append(d.legacyUARTT0TransmitBuffers[controller], value)
	if len(frame) < 5 {
		d.legacyUARTT0TransmitBuffers[controller] = frame
		return
	}
	if len(frame) == 5 {
		instruction := frame[1]
		length := int(frame[4])
		if instruction == 0xb0 {
			offset := int(frame[2])<<8 | int(frame[3])
			remaining := legacyUARTT0FileSize(d.legacyUARTT0SelectedFiles[controller]) - offset
			if remaining <= 0 {
				d.legacyUARTReceiveQueues[controller] = append(
					d.legacyUARTReceiveQueues[controller],
					0x6b, 0x00,
				)
				delete(d.legacyUARTT0TransmitBuffers, controller)
				return
			}
			requested := length
			if requested == 0 {
				requested = 256
			}
			if requested > remaining {
				d.legacyUARTReceiveQueues[controller] = append(
					d.legacyUARTReceiveQueues[controller],
					0x6c, byte(remaining),
				)
				delete(d.legacyUARTT0TransmitBuffers, controller)
				return
			}
		}
		if length == 0 && instruction == 0xf2 {
			// STATUS with Le=0 requests the complete response. Return the exact
			// length through 6C so the terminal retries with a bounded transfer.
			d.legacyUARTReceiveQueues[controller] = append(
				d.legacyUARTReceiveQueues[controller],
				0x6c, byte(len(legacyUARTT0ApplicationFCP())),
			)
			delete(d.legacyUARTT0TransmitBuffers, controller)
			return
		}
		if length == 0 && legacyUARTT0CommandHasOutgoingData(instruction) {
			// T=0 encodes a 256-byte Le as zero, but an outgoing-data command
			// with a zero P3 is a case-1 command and has no transfer phase.
			d.appendLegacyUARTT0Status(controller, frame)
			delete(d.legacyUARTT0TransmitBuffers, controller)
			return
		}
		if length == 0 {
			length = 256
		}
		if !legacyUARTT0CommandHasOutgoingData(instruction) {
			// The instruction ACK transfers all response bytes in one operation.
			// Deterministic blank contents keep subscriber-specific data outside
			// the board profile while preserving the T=0 transport contract.
			d.legacyUARTReceiveQueues[controller] = append(
				d.legacyUARTReceiveQueues[controller],
				instruction,
			)
			d.legacyUARTReceiveQueues[controller] = append(
				d.legacyUARTReceiveQueues[controller],
				d.legacyUARTT0ResponseData(controller, frame, length)...,
			)
			d.appendLegacyUARTT0Status(controller, frame)
			delete(d.legacyUARTT0TransmitBuffers, controller)
			return
		}
		// A T=0 card returns the instruction byte as a procedure byte to ask
		// for the command data in one transfer.
		d.legacyUARTReceiveQueues[controller] = append(
			d.legacyUARTReceiveQueues[controller],
			instruction,
		)
		d.legacyUARTT0TransmitBuffers[controller] = frame
		return
	}

	length := int(frame[4])
	if length == 0 {
		length = 256
	}
	if len(frame) == 5+length {
		d.appendLegacyUARTT0Status(controller, frame)
		delete(d.legacyUARTT0TransmitBuffers, controller)
		return
	}
	if len(frame) > 5+length {
		delete(d.legacyUARTT0TransmitBuffers, controller)
		return
	}
	d.legacyUARTT0TransmitBuffers[controller] = frame
}

func (d *QualcommBootControl) refreshLegacyUARTInterrupt(controller uint32) error {
	receive, configured := d.legacyUARTReceiveProfile(controller)
	if !configured {
		return nil
	}
	raw := d.legacyUARTRawInterruptStatus(controller)
	if len(d.legacyUARTReceiveQueues[controller]) == 0 {
		d.legacyUARTReceiveIRQPending[controller] = false
	}
	if receive.PulseReceiveInterrupt {
		// UARTDM routes a receive-DMA completion pulse through the parent
		// interrupt source while leaving the UART core IMR clear. Grouped VIC
		// children remain asserted until the firmware drains the receive FIFO;
		// flat sources retain the original edge behavior.
		if receive.VectoredGroupMask != 0 {
			return d.vectoredInterruptController.SetGroupedSource(
				receive.VectoredGroupStatusOffset,
				receive.VectoredGroupMask,
				raw != 0,
			)
		}
		if raw == 0 {
			return nil
		}
		if receive.UseVectoredController {
			return d.vectoredInterruptController.PulseSource(receive.InterruptSource)
		}
		return d.interruptController.PulseSource(receive.InterruptSource)
	}
	masked := raw & d.registers[controller+qualcommLegacyUARTISROffset]
	asserted := masked != 0
	if err := d.interruptController.SetSource(receive.InterruptSource, asserted); err != nil {
		return err
	}
	if asserted {
		return nil
	}
	// The UART presents a level interrupt to the legacy QIC. Once its FIFO is
	// drained or the UART interrupt mask is cleared, no edge remains for the
	// guest to acknowledge separately. Drop the QIC's latched status together
	// with the input level so a completed short receive cannot retrigger forever.
	return d.interruptController.acknowledgeSource(receive.InterruptSource)
}

func (d *QualcommBootControl) readLegacyUART(offset uint32, width Width) (uint32, bool, error) {
	base, relative, configured := d.legacyUARTOffset(offset)
	if !configured {
		return 0, false, nil
	}
	_, wordConfigured := d.mixedWidthOffsets[offset]
	if receive, ok := d.legacyUARTReceiveProfile(base); ok &&
		relative == receive.receiveFIFOOffset() {
		if width != Width8 && (width != Width32 || !wordConfigured) {
			return 0, true, fmt.Errorf(
				"%w: legacy UART FIFO read%d at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				offset,
			)
		}
		queue := d.legacyUARTReceiveQueues[base]
		if len(queue) == 0 {
			return 0, true, nil
		}
		value := queue[0]
		d.legacyUARTReceiveQueues[base] = queue[1:]
		if len(queue) == 1 {
			d.legacyUARTReceiveIRQPending[base] = false
		}
		if err := d.refreshLegacyUARTInterrupt(base); err != nil {
			return 0, true, err
		}
		return uint32(value), true, nil
	}
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
		status := qualcommLegacyUARTStatusTXReady | qualcommLegacyUARTStatusTXEmpty
		if len(d.legacyUARTReceiveQueues[base]) != 0 {
			status |= qualcommLegacyUARTStatusRXReady
		}
		return status, true, nil
	case qualcommLegacyUARTFIFOOffset:
		// Unscripted UARTs retain an empty read-side FIFO at +0x0c. Scripted
		// endpoints are handled above at their profile-selected aperture.
		_, wordConfigured := d.mixedWidthOffsets[offset]
		if width != Width8 && (width != Width32 || !wordConfigured) {
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
			status := d.legacyUARTRawInterruptStatus(base)
			if relative == qualcommLegacyUARTMISROffset {
				status &= d.registers[base+qualcommLegacyUARTISROffset]
			}
			return status, true, nil
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
	base, relative, configured := d.legacyUARTOffset(offset)
	if !configured {
		return false, nil
	}
	_, wordConfigured := d.mixedWidthOffsets[offset]
	if relative == qualcommLegacyUARTMISROffset {
		receive, receiveConfigured := d.legacyUARTReceiveProfile(base)
		if receiveConfigured && receive.ActivationCommand != 0 &&
			value == receive.ActivationCommand &&
			!d.legacyUARTReceivePublished[base] && d.legacyUARTReceiveDelays[base] == 0 {
			if width != Width8 && width != Width16 && (width != Width32 || !wordConfigured) {
				return true, fmt.Errorf(
					"%w: legacy UART activation command write%d value 0x%x at 0x%x",
					ErrQualcommBootControlMMIO,
					width*8,
					value,
					offset,
				)
			}
			// ISO 7816 reset release is represented by STOP_BREAK. The card starts
			// its ATR after that command; STOP_BREAK itself does not manufacture an
			// empty receive interrupt. A later 0x04 is UART TX enable, not an ATR
			// trigger.
			if receive.DelayInstructions == 0 {
				d.legacyUARTReceiveQueues[base] = append([]byte(nil), receive.Data...)
				d.legacyUARTReceiveIRQPending[base] = len(receive.Data) != 0
				d.legacyUARTReceivePublished[base] = true
				return true, d.refreshLegacyUARTInterrupt(base)
			}
			d.legacyUARTReceiveDelays[base] = receive.DelayInstructions
			return true, nil
		}
		if receiveConfigured && value == receive.receiveCommand() &&
			receive.ActivationCommand == 0 &&
			!d.legacyUARTReceivePublished[base] && d.legacyUARTReceiveDelays[base] == 0 {
			if width != Width8 && width != Width16 && (width != Width32 || !wordConfigured) {
				return true, fmt.Errorf(
					"%w: legacy UART command write%d value 0x%x at 0x%x",
					ErrQualcommBootControlMMIO,
					width*8,
					value,
					offset,
				)
			}
			if receive.DelayInstructions == 0 {
				d.legacyUARTReceiveQueues[base] = append([]byte(nil), receive.Data...)
				d.legacyUARTReceiveIRQPending[base] = len(receive.Data) != 0
				d.legacyUARTReceivePublished[base] = true
				return true, d.refreshLegacyUARTInterrupt(base)
			}
			d.legacyUARTReceiveDelays[base] = receive.DelayInstructions
			return true, nil
		}
		if receiveConfigured && value == qualcommLegacyUARTCommandResetRXStale {
			d.legacyUARTReceiveIRQPending[base] = false
			return true, d.refreshLegacyUARTInterrupt(base)
		}
	}
	if relative == qualcommLegacyUARTISROffset {
		_, receiveConfigured := d.legacyUARTReceiveProfile(base)
		if !receiveConfigured {
			return false, nil
		}
		_, halfwordConfigured := d.halfwordOffsets[offset]
		if width != Width16 && (width != Width32 || !wordConfigured) ||
			width == Width16 && !halfwordConfigured && !wordConfigured ||
			width == Width16 && value > 0xffff {
			return true, fmt.Errorf(
				"%w: legacy UART interrupt-mask write%d value 0x%x at 0x%x",
				ErrQualcommBootControlMMIO,
				width*8,
				value,
				offset,
			)
		}
		if width == Width16 {
			d.registers[offset] = d.registers[offset]&0xffff0000 | value
		} else {
			d.registers[offset] = value
		}
		return true, d.refreshLegacyUARTInterrupt(base)
	}
	if relative != qualcommLegacyUARTFIFOOffset {
		return false, nil
	}
	if width != Width8 && (width != Width32 || !wordConfigured) || value > 0xff {
		return true, fmt.Errorf(
			"%w: legacy UART FIFO write%d value 0x%x at 0x%x",
			ErrQualcommBootControlMMIO,
			width*8,
			value,
			offset,
		)
	}
	// Some single-wire peripherals, notably ISO 7816 smart cards, expose the
	// transmitted character through the receiver. Their drivers use that echo
	// to advance a multi-byte request one character at a time. Keep the default
	// UART sink behavior, but reproduce the physical loopback when the board
	// profile identifies such a transport.
	receive, receiveConfigured := d.legacyUARTReceiveProfile(base)
	if receiveConfigured && d.legacyUARTReceivePublished[base] {
		if receive.EchoTransmit {
			d.legacyUARTReceiveQueues[base] = append(
				d.legacyUARTReceiveQueues[base],
				byte(value),
			)
		}
		if receive.T0Card {
			d.writeLegacyUARTT0(base, byte(value))
			d.legacyUARTReceiveIRQPending[base] =
				len(d.legacyUARTReceiveQueues[base]) != 0
			return true, d.refreshLegacyUARTInterrupt(base)
		}
		if receive.TransmitFrameBytes != 0 {
			d.legacyUARTTransmitCounts[base]++
			if d.legacyUARTTransmitCounts[base] == receive.TransmitFrameBytes {
				d.legacyUARTReceiveQueues[base] = append(
					d.legacyUARTReceiveQueues[base],
					receive.TransmitResponse...,
				)
				d.legacyUARTTransmitCounts[base] = 0
			}
		}
		if receive.EchoTransmit || receive.TransmitFrameBytes != 0 {
			d.legacyUARTReceiveIRQPending[base] =
				len(d.legacyUARTReceiveQueues[base]) != 0
			return true, d.refreshLegacyUARTInterrupt(base)
		}
	}
	// No host endpoint is attached. Accepting the low byte otherwise models an
	// empty transmit FIFO. Some profiled ARM7 code uses a word store for the
	// same byte-wide aperture.
	return true, nil
}
