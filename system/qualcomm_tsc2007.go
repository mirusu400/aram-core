package system

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var ErrQualcommTSC2007 = errors.New("invalid Qualcomm TSC2007 operation")

// QualcommTSC2007Profile describes the touchscreen wiring used by one board.
// Coordinates accepted from the host are panel coordinates. The controller
// publishes calibrated 12-bit samples and asserts the profiled GPIO group when
// the pen first touches the panel.
type QualcommTSC2007Profile struct {
	Width, Height  uint16
	PenInputOffset uint32
	PenInputMask   uint32
	InterruptGroup QualcommGPIOInterruptGroupProfile
	InterruptMask  uint32
}

func (p QualcommTSC2007Profile) validate() error {
	group := p.InterruptGroup
	if p.Width == 0 || p.Height == 0 ||
		p.PenInputOffset%4 != 0 || p.PenInputOffset >= QualcommSecondaryClockWindowSize ||
		p.PenInputMask == 0 || p.PenInputMask&(p.PenInputMask-1) != 0 ||
		p.InterruptMask == 0 || p.InterruptMask&(p.InterruptMask-1) != 0 ||
		group.InterruptSource >= 64 {
		return ErrQualcommTSC2007
	}
	seen := make(map[uint32]struct{}, 5)
	for _, offset := range []uint32{
		group.ClearOffset, group.EnableOffset, group.DetectOffset,
		group.PolarityOffset, group.StatusOffset,
	} {
		if offset%4 != 0 || offset >= QualcommPrimaryClockWindowSize {
			return ErrQualcommTSC2007
		}
		if _, duplicate := seen[offset]; duplicate {
			return ErrQualcommTSC2007
		}
		seen[offset] = struct{}{}
	}
	return nil
}

func cloneQualcommTSC2007Profile(profile *QualcommTSC2007Profile) *QualcommTSC2007Profile {
	if profile == nil {
		return nil
	}
	cloned := *profile
	return &cloned
}

func fingerprintQualcommTSC2007Profile(profile QualcommTSC2007Profile) [sha256.Size]byte {
	var encoded bytes.Buffer
	_ = binary.Write(&encoded, binary.LittleEndian, profile.Width)
	_ = binary.Write(&encoded, binary.LittleEndian, profile.Height)
	_ = binary.Write(&encoded, binary.LittleEndian, profile.PenInputOffset)
	_ = binary.Write(&encoded, binary.LittleEndian, profile.PenInputMask)
	for _, offset := range []uint32{
		profile.InterruptGroup.ClearOffset,
		profile.InterruptGroup.EnableOffset,
		profile.InterruptGroup.DetectOffset,
		profile.InterruptGroup.PolarityOffset,
		profile.InterruptGroup.StatusOffset,
	} {
		_ = binary.Write(&encoded, binary.LittleEndian, offset)
	}
	_ = encoded.WriteByte(profile.InterruptGroup.InterruptSource)
	if profile.InterruptGroup.UseVectoredController {
		_ = encoded.WriteByte(1)
	} else {
		_ = encoded.WriteByte(0)
	}
	_ = binary.Write(&encoded, binary.LittleEndian, profile.InterruptMask)
	return sha256.Sum256(encoded.Bytes())
}

// QualcommTSC2007 models the pen-detect GPIO and the coordinate-producing part
// of the serial touchscreen controller. The exact-build firmware reaches the
// latter through two HLE call gates because its GPIO-bit-banged I2C engine is a
// timing-sensitive software peripheral rather than a distinct MMIO device.
type QualcommTSC2007 struct {
	profile     QualcommTSC2007Profile
	fingerprint [sha256.Size]byte
	driver      qualcommGPIOInterruptSourceDriver

	x, y     uint16
	pressed  bool
	command  uint8
	enable   uint32
	detect   uint32
	polarity uint32
	status   uint32

	penEvents       uint64
	interruptPulses uint64
	commandWrites   uint64
	sampleReads     uint64
}

// QualcommTSC2007Activity exposes host-side diagnostic counters. The counters
// do not affect guest-visible state and intentionally are not serialized.
type QualcommTSC2007Activity struct {
	PenEvents       uint64
	InterruptPulses uint64
	CommandWrites   uint64
	SampleReads     uint64
	LastCommand     uint8
}

func NewQualcommTSC2007(profile QualcommTSC2007Profile) (*QualcommTSC2007, error) {
	if err := profile.validate(); err != nil {
		return nil, fmt.Errorf("create Qualcomm TSC2007: %w", err)
	}
	d := &QualcommTSC2007{
		profile:     profile,
		fingerprint: fingerprintQualcommTSC2007Profile(profile),
	}
	_ = d.Reset()
	return d, nil
}

func (d *QualcommTSC2007) AttachInterruptControllers(
	interruptController *QualcommInterruptController,
	vectoredInterruptController *QualcommVectoredInterruptController,
) error {
	if d == nil || d.driver != nil {
		return fmt.Errorf("attach Qualcomm TSC2007 interrupt: %w", ErrQualcommTSC2007)
	}
	if d.profile.InterruptGroup.UseVectoredController {
		if vectoredInterruptController == nil {
			return fmt.Errorf("attach Qualcomm TSC2007 VIC interrupt: %w", ErrQualcommTSC2007)
		}
		d.driver = vectoredInterruptController
	} else {
		if interruptController == nil {
			return fmt.Errorf("attach Qualcomm TSC2007 interrupt controller: %w", ErrQualcommTSC2007)
		}
		d.driver = interruptController
	}
	return nil
}

func (d *QualcommTSC2007) Reset() error {
	d.x, d.y = 0, 0
	d.pressed = false
	d.command = 0
	d.enable, d.detect, d.polarity, d.status = 0, 0, 0, 0
	d.penEvents, d.interruptPulses = 0, 0
	d.commandWrites, d.sampleReads = 0, 0
	return nil
}

// SetTouch changes the host pen position and contact state.
func (d *QualcommTSC2007) SetTouch(x, y int, pressed bool) error {
	if d == nil || x < 0 || y < 0 || x >= int(d.profile.Width) || y >= int(d.profile.Height) {
		return fmt.Errorf("touch coordinate (%d,%d): %w", x, y, ErrQualcommTSC2007)
	}
	wasPressed := d.pressed
	d.x, d.y, d.pressed = uint16(x), uint16(y), pressed
	if wasPressed || !pressed {
		return nil
	}
	d.penEvents++
	d.status |= d.profile.InterruptMask
	return d.signalInterrupt()
}

// WriteCommand accepts the command byte passed to the firmware's one-byte I2C
// write wrapper. The following read returns the matching conversion result.
func (d *QualcommTSC2007) WriteCommand(command uint8) {
	d.command = command
	d.commandWrites++
}

// ReadSample returns a 12-bit conversion in the representation expected by the
// firmware wrapper after it has unpacked the TSC2007's two response bytes.
func (d *QualcommTSC2007) ReadSample() uint16 {
	d.sampleReads++
	const (
		xMinimum = uint32(330)
		xMaximum = uint32(3739)
		yMinimum = uint32(190)
		yMaximum = uint32(3902)
	)
	switch d.command & 0xf0 {
	case 0x80, 0xc0:
		span := xMaximum - xMinimum
		return uint16(xMinimum + uint32(d.x)*span/uint32(max(1, int(d.profile.Width)-1)))
	case 0x90, 0xd0:
		span := yMaximum - yMinimum
		return uint16(yMaximum - uint32(d.y)*span/uint32(max(1, int(d.profile.Height)-1)))
	default:
		return 0
	}
}

// Activity returns diagnostic traffic observed since the last reset.
func (d *QualcommTSC2007) Activity() QualcommTSC2007Activity {
	if d == nil {
		return QualcommTSC2007Activity{}
	}
	return QualcommTSC2007Activity{
		PenEvents:       d.penEvents,
		InterruptPulses: d.interruptPulses,
		CommandWrites:   d.commandWrites,
		SampleReads:     d.sampleReads,
		LastCommand:     d.command,
	}
}

func (d *QualcommTSC2007) ObserveGPIORead(offset, value uint32) uint32 {
	if offset != d.profile.PenInputOffset {
		return value
	}
	if d.pressed {
		return value &^ d.profile.PenInputMask
	}
	return value | d.profile.PenInputMask
}

func (d *QualcommTSC2007) readPrimaryGPIORegister(offset uint32) (uint32, bool) {
	group := d.profile.InterruptGroup
	switch offset {
	case group.ClearOffset:
		return 0, true
	case group.EnableOffset:
		return d.enable, true
	case group.DetectOffset:
		return d.detect, true
	case group.PolarityOffset:
		return d.polarity, true
	case group.StatusOffset:
		return d.status, true
	default:
		return 0, false
	}
}

func (d *QualcommTSC2007) writePrimaryGPIORegister(offset, value uint32) (bool, error) {
	group := d.profile.InterruptGroup
	switch offset {
	case group.ClearOffset:
		d.status &^= value
		return true, nil
	case group.EnableOffset:
		wasPending := d.status&d.enable != 0
		newlyEnabled := value &^ d.enable
		d.enable = value
		if d.pressed && newlyEnabled&d.profile.InterruptMask != 0 &&
			d.detect&d.profile.InterruptMask == 0 {
			lineHigh := false
			activeHigh := d.polarity&d.profile.InterruptMask != 0
			if lineHigh == activeHigh {
				d.status |= d.profile.InterruptMask
			}
		}
		if !wasPending && d.status&d.enable != 0 {
			return true, d.signalInterrupt()
		}
		return true, nil
	case group.DetectOffset:
		d.detect = value
		return true, nil
	case group.PolarityOffset:
		d.polarity = value
		return true, nil
	default:
		return false, nil
	}
}

func (d *QualcommTSC2007) signalInterrupt() error {
	if d.status&d.enable == 0 {
		return nil
	}
	if d.driver == nil {
		return fmt.Errorf("signal Qualcomm TSC2007 interrupt: %w", ErrQualcommTSC2007)
	}
	if err := d.driver.PulseSource(d.profile.InterruptGroup.InterruptSource); err != nil {
		return fmt.Errorf("pulse Qualcomm TSC2007 interrupt: %w", err)
	}
	d.interruptPulses++
	return nil
}

func (d *QualcommTSC2007) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("QTSC")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_, _ = output.Write(d.fingerprint[:])
	_ = binary.Write(&output, binary.LittleEndian, d.x)
	_ = binary.Write(&output, binary.LittleEndian, d.y)
	if d.pressed {
		_ = output.WriteByte(1)
	} else {
		_ = output.WriteByte(0)
	}
	_ = output.WriteByte(d.command)
	_ = binary.Write(&output, binary.LittleEndian, d.enable)
	_ = binary.Write(&output, binary.LittleEndian, d.detect)
	_ = binary.Write(&output, binary.LittleEndian, d.polarity)
	_ = binary.Write(&output, binary.LittleEndian, d.status)
	return output.Bytes(), nil
}

func (d *QualcommTSC2007) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	var fingerprint [sha256.Size]byte
	var x, y uint16
	var pressed, command byte
	var enable, detect, polarity, status uint32
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QTSC" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 {
		return ErrInvalidState
	}
	if _, err := io.ReadFull(reader, fingerprint[:]); err != nil || fingerprint != d.fingerprint ||
		binary.Read(reader, binary.LittleEndian, &x) != nil ||
		binary.Read(reader, binary.LittleEndian, &y) != nil ||
		binary.Read(reader, binary.LittleEndian, &pressed) != nil || pressed > 1 ||
		binary.Read(reader, binary.LittleEndian, &command) != nil ||
		binary.Read(reader, binary.LittleEndian, &enable) != nil ||
		binary.Read(reader, binary.LittleEndian, &detect) != nil ||
		binary.Read(reader, binary.LittleEndian, &polarity) != nil ||
		binary.Read(reader, binary.LittleEndian, &status) != nil || reader.Len() != 0 ||
		x >= d.profile.Width || y >= d.profile.Height {
		return ErrInvalidState
	}
	d.x, d.y, d.pressed, d.command = x, y, pressed == 1, command
	d.enable, d.detect, d.polarity, d.status = enable, detect, polarity, status
	return nil
}

var _ QualcommGPIOReadObserver = (*QualcommTSC2007)(nil)
