package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

var ErrSamsungMGPMMIO = errors.New("unsupported Samsung MGP control access")

const (
	samsungMGPStateVersion        = uint32(1)
	samsungMGPMaxPendingResponses = 64
)

// SamsungMGPControlConfig describes the small ARM7/MGP boot handshake exposed
// beside the companion processor's code and shared-memory aperture. The host
// asserts the release register, uploads the image, clears the register, and
// waits for the companion to publish a nonzero ready byte in shared memory.
type SamsungMGPControlConfig struct {
	Size                      uint32
	ReleaseOffset             uint32
	ReadyAddress              uint32
	ReadyValue                uint8
	ResponseDelayInstructions uint64
}

func (c SamsungMGPControlConfig) validate() error {
	if c.Size == 0 || c.Size%uint32(Width16) != 0 ||
		uint64(c.Size) > uint64(int(^uint(0)>>1)) ||
		c.ReleaseOffset%uint32(Width16) != 0 ||
		uint64(c.ReleaseOffset)+uint64(Width16) > uint64(c.Size) ||
		c.ReadyValue == 0 || c.ResponseDelayInstructions == 0 {
		return fmt.Errorf("invalid Samsung MGP control configuration")
	}
	return nil
}

// SamsungMGPControl retains the MGP's halfword control registers and models
// only its evidenced boot-completion side effect. The delayed write runs after
// a CPU slice, outside the bus lock held by the release-register write.
type SamsungMGPControl struct {
	bus                   *Bus
	config                SamsungMGPControlConfig
	data                  []byte
	pendingResponseDelays []uint64
}

func NewSamsungMGPControl(
	bus *Bus,
	config SamsungMGPControlConfig,
) (*SamsungMGPControl, error) {
	if bus == nil {
		return nil, fmt.Errorf("Samsung MGP control requires a physical bus")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &SamsungMGPControl{
		bus:    bus,
		config: config,
		data:   make([]byte, int(config.Size)),
	}, nil
}

func (d *SamsungMGPControl) Reset() error {
	clear(d.data)
	d.pendingResponseDelays = nil
	return nil
}

func (d *SamsungMGPControl) validAccess(offset uint32, width Width) bool {
	return width == Width16 && offset%uint32(Width16) == 0 &&
		uint64(offset)+uint64(Width16) <= uint64(len(d.data))
}

func (d *SamsungMGPControl) Read(offset uint32, width Width) (uint32, error) {
	if !d.validAccess(offset, width) {
		return 0, fmt.Errorf("%w: read%d at 0x%x", ErrSamsungMGPMMIO, width*8, offset)
	}
	return uint32(binary.LittleEndian.Uint16(d.data[int(offset) : int(offset)+2])), nil
}

func (d *SamsungMGPControl) Write(offset uint32, width Width, value uint32) error {
	if !d.validAccess(offset, width) || value > uint32(^uint16(0)) {
		return fmt.Errorf(
			"%w: write%d value 0x%x at 0x%x",
			ErrSamsungMGPMMIO,
			width*8,
			value,
			offset,
		)
	}
	start := int(offset)
	previous := binary.LittleEndian.Uint16(d.data[start : start+2])
	released := offset == d.config.ReleaseOffset && previous != 0 && uint16(value) == 0
	if released && len(d.pendingResponseDelays) >= samsungMGPMaxPendingResponses {
		return fmt.Errorf("Samsung MGP pending response queue is full")
	}
	binary.LittleEndian.PutUint16(d.data[start:start+2], uint16(value))
	if released {
		d.pendingResponseDelays = append(
			d.pendingResponseDelays,
			d.config.ResponseDelayInstructions,
		)
	}
	return nil
}

// Advance completes at most one response per execution slice so repeated
// release cycles remain serialized through the companion's single ready byte.
func (d *SamsungMGPControl) Advance(retiredInstructions uint64) error {
	if retiredInstructions == 0 || len(d.pendingResponseDelays) == 0 {
		return nil
	}
	if retiredInstructions < d.pendingResponseDelays[0] {
		d.pendingResponseDelays[0] -= retiredInstructions
		return nil
	}
	ready := []byte{d.config.ReadyValue}
	if err := d.bus.Write(d.config.ReadyAddress, ready, cpu.PermissionWrite); err != nil {
		return fmt.Errorf("publish Samsung MGP ready byte: %w", err)
	}
	d.pendingResponseDelays = d.pendingResponseDelays[1:]
	return nil
}

func (d *SamsungMGPControl) SaveState() ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("SMGP")
	_ = binary.Write(&output, binary.LittleEndian, samsungMGPStateVersion)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.data)))
	_ = binary.Write(&output, binary.LittleEndian, d.config.ReleaseOffset)
	_ = binary.Write(&output, binary.LittleEndian, d.config.ReadyAddress)
	_ = output.WriteByte(d.config.ReadyValue)
	_, _ = output.Write([]byte{0, 0, 0})
	_ = binary.Write(&output, binary.LittleEndian, d.config.ResponseDelayInstructions)
	_, _ = output.Write(d.data)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(d.pendingResponseDelays)))
	for _, remaining := range d.pendingResponseDelays {
		_ = binary.Write(&output, binary.LittleEndian, remaining)
	}
	return output.Bytes(), nil
}

func (d *SamsungMGPControl) LoadState(state []byte) error {
	const headerSize = 32
	if len(state) < headerSize+4 || string(state[:4]) != "SMGP" ||
		binary.LittleEndian.Uint32(state[4:8]) != samsungMGPStateVersion ||
		binary.LittleEndian.Uint32(state[8:12]) != uint32(len(d.data)) ||
		binary.LittleEndian.Uint32(state[12:16]) != d.config.ReleaseOffset ||
		binary.LittleEndian.Uint32(state[16:20]) != d.config.ReadyAddress ||
		state[20] != d.config.ReadyValue || state[21] != 0 || state[22] != 0 || state[23] != 0 ||
		binary.LittleEndian.Uint64(state[24:32]) != d.config.ResponseDelayInstructions {
		return ErrInvalidState
	}
	dataEnd := headerSize + len(d.data)
	if dataEnd+4 > len(state) {
		return ErrInvalidState
	}
	count := binary.LittleEndian.Uint32(state[dataEnd : dataEnd+4])
	if count > samsungMGPMaxPendingResponses ||
		uint64(dataEnd)+4+uint64(count)*8 != uint64(len(state)) {
		return ErrInvalidState
	}
	pending := make([]uint64, count)
	offset := dataEnd + 4
	for index := range pending {
		remaining := binary.LittleEndian.Uint64(state[offset : offset+8])
		if remaining == 0 || remaining > d.config.ResponseDelayInstructions {
			return ErrInvalidState
		}
		pending[index] = remaining
		offset += 8
	}
	copy(d.data, state[headerSize:dataEnd])
	d.pendingResponseDelays = pending
	return nil
}

// LoadStateSubset migrates the passive halfword register window used by older
// diagnostic snapshots. Shared RAM remains its separately serialized region;
// no response is inferred from a snapshot that predates the active model.
func (d *SamsungMGPControl) LoadStateSubset(state []byte) error {
	if len(state) >= 4 && string(state[:4]) == "SMGP" {
		return d.LoadState(state)
	}
	if len(state) != 16+len(d.data) || string(state[:4]) != "LRWN" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || Width(state[8]) != Width16 ||
		state[9] != 0 || state[10] != 0 || state[11] != 0 ||
		binary.LittleEndian.Uint32(state[12:16]) != uint32(len(d.data)) {
		return ErrInvalidState
	}
	copy(d.data, state[16:])
	d.pendingResponseDelays = nil
	return nil
}

var (
	_ Device               = (*SamsungMGPControl)(nil)
	_ ClockedDevice        = (*SamsungMGPControl)(nil)
	_ StatefulDevice       = (*SamsungMGPControl)(nil)
	_ SubsetStatefulDevice = (*SamsungMGPControl)(nil)
)
