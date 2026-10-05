package system

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const QualcommIndirectRegisterBusWindowSize uint32 = 0x10

var ErrQualcommIndirectRegisterBus = errors.New("unsupported Qualcomm indirect register bus access")

// QualcommIndirectRegisterBus models the MSM external-register aperture used by
// firmware which cannot access the target register address directly. A command,
// two address halfwords, and one mixed-width data word form a single transaction
// port. Target words remain independent; real hardware does not alias the data
// from the last selected address into every other target register.
type QualcommIndirectRegisterBus struct {
	control     uint16
	addressLow  uint16
	addressHigh uint16
	words       map[uint32]uint32
	statusWords map[uint32]struct{}
}

func NewQualcommIndirectRegisterBus(statusAddresses ...uint32) (*QualcommIndirectRegisterBus, error) {
	statusWords := make(map[uint32]struct{}, len(statusAddresses))
	for _, address := range statusAddresses {
		if address%4 != 0 {
			return nil, fmt.Errorf("create Qualcomm indirect register bus status %#x: %w", address, ErrQualcommIndirectRegisterBus)
		}
		if _, duplicate := statusWords[address]; duplicate {
			return nil, fmt.Errorf("create Qualcomm indirect register bus duplicate status %#x: %w", address, ErrQualcommIndirectRegisterBus)
		}
		statusWords[address] = struct{}{}
	}
	d := &QualcommIndirectRegisterBus{statusWords: statusWords}
	_ = d.Reset()
	return d, nil
}

func (d *QualcommIndirectRegisterBus) Reset() error {
	d.control, d.addressLow, d.addressHigh = 0, 0, 0
	d.words = make(map[uint32]uint32)
	return nil
}

func (d *QualcommIndirectRegisterBus) selectedAddress() uint32 {
	return uint32(d.addressLow) | uint32(d.addressHigh)<<16
}

func (d *QualcommIndirectRegisterBus) Read(offset uint32, width Width) (uint32, error) {
	switch {
	case offset == 0 && width == Width16:
		// Bit zero is the hardware busy flag. Transactions complete
		// synchronously in the emulator, so it always reads deasserted.
		return uint32(d.control &^ 1), nil
	case offset == 8 && width == Width16:
		return uint32(d.addressLow), nil
	case offset == 10 && width == Width16:
		return uint32(d.addressHigh), nil
	case offset == 12 && width == Width32:
		return d.words[d.selectedAddress()], nil
	case (offset == 12 || offset == 14) && width == Width16:
		shift := (offset - 12) * 8
		return d.words[d.selectedAddress()] >> shift & 0xffff, nil
	default:
		return 0, fmt.Errorf("read Qualcomm indirect register bus offset %#x width %d: %w", offset, width, ErrQualcommIndirectRegisterBus)
	}
}

func (d *QualcommIndirectRegisterBus) Write(offset uint32, width Width, value uint32) error {
	switch {
	case offset == 0 && width == Width16:
		d.control = uint16(value) &^ 1
		return nil
	case offset == 8 && width == Width16:
		d.addressLow = uint16(value)
		return nil
	case offset == 10 && width == Width16:
		d.addressHigh = uint16(value)
		return nil
	case offset == 12 && width == Width32:
		address := d.selectedAddress()
		if _, status := d.statusWords[address]; !status {
			d.words[address] = value
		}
		return nil
	case (offset == 12 || offset == 14) && width == Width16:
		address := d.selectedAddress()
		if _, status := d.statusWords[address]; status {
			return nil
		}
		shift := (offset - 12) * 8
		mask := uint32(0xffff) << shift
		d.words[address] = d.words[address]&^mask | value<<shift&mask
		return nil
	default:
		return fmt.Errorf("write Qualcomm indirect register bus offset %#x width %d: %w", offset, width, ErrQualcommIndirectRegisterBus)
	}
}

// AssertStatus publishes a hardware-owned status bit at a profiled target word.
func (d *QualcommIndirectRegisterBus) AssertStatus(address, mask uint32) error {
	if d == nil || mask == 0 {
		return ErrQualcommIndirectRegisterBus
	}
	if _, status := d.statusWords[address]; !status {
		return fmt.Errorf("assert Qualcomm indirect register bus status %#x: %w", address, ErrQualcommIndirectRegisterBus)
	}
	d.words[address] |= mask
	return nil
}

// ClearStatus removes hardware-owned status bits after the guest acknowledges
// the corresponding external interrupt.
func (d *QualcommIndirectRegisterBus) ClearStatus(address, mask uint32) error {
	if d == nil || mask == 0 {
		return ErrQualcommIndirectRegisterBus
	}
	if _, status := d.statusWords[address]; !status {
		return fmt.Errorf("clear Qualcomm indirect register bus status %#x: %w", address, ErrQualcommIndirectRegisterBus)
	}
	d.words[address] &^= mask
	return nil
}

func (d *QualcommIndirectRegisterBus) SaveState() ([]byte, error) {
	addresses := make([]uint32, 0, len(d.words))
	for address := range d.words {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i] < addresses[j] })
	statuses := make([]uint32, 0, len(d.statusWords))
	for address := range d.statusWords {
		statuses = append(statuses, address)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i] < statuses[j] })

	var output bytes.Buffer
	output.WriteString("QIRB")
	_ = binary.Write(&output, binary.LittleEndian, uint32(1))
	_ = binary.Write(&output, binary.LittleEndian, d.control)
	_ = binary.Write(&output, binary.LittleEndian, d.addressLow)
	_ = binary.Write(&output, binary.LittleEndian, d.addressHigh)
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(statuses)))
	for _, address := range statuses {
		_ = binary.Write(&output, binary.LittleEndian, address)
	}
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(addresses)))
	for _, address := range addresses {
		_ = binary.Write(&output, binary.LittleEndian, address)
		_ = binary.Write(&output, binary.LittleEndian, d.words[address])
	}
	return output.Bytes(), nil
}

func (d *QualcommIndirectRegisterBus) LoadState(state []byte) error {
	reader := bytes.NewReader(state)
	var magic [4]byte
	var version uint32
	var control, addressLow, addressHigh uint16
	if _, err := io.ReadFull(reader, magic[:]); err != nil || string(magic[:]) != "QIRB" ||
		binary.Read(reader, binary.LittleEndian, &version) != nil || version != 1 ||
		binary.Read(reader, binary.LittleEndian, &control) != nil ||
		binary.Read(reader, binary.LittleEndian, &addressLow) != nil ||
		binary.Read(reader, binary.LittleEndian, &addressHigh) != nil || control&1 != 0 {
		return ErrInvalidState
	}
	var statusCount uint32
	if binary.Read(reader, binary.LittleEndian, &statusCount) != nil || uint64(statusCount)*4 > uint64(reader.Len()) {
		return ErrInvalidState
	}
	statuses := make(map[uint32]struct{}, statusCount)
	for index := uint32(0); index < statusCount; index++ {
		var address uint32
		if binary.Read(reader, binary.LittleEndian, &address) != nil || address%4 != 0 {
			return ErrInvalidState
		}
		if _, duplicate := statuses[address]; duplicate {
			return ErrInvalidState
		}
		statuses[address] = struct{}{}
	}
	if len(statuses) != len(d.statusWords) {
		return ErrInvalidState
	}
	for address := range statuses {
		if _, known := d.statusWords[address]; !known {
			return ErrInvalidState
		}
	}
	var wordCount uint32
	if binary.Read(reader, binary.LittleEndian, &wordCount) != nil || uint64(wordCount)*8 != uint64(reader.Len()) {
		return ErrInvalidState
	}
	words := make(map[uint32]uint32, wordCount)
	for index := uint32(0); index < wordCount; index++ {
		var address, value uint32
		if binary.Read(reader, binary.LittleEndian, &address) != nil ||
			binary.Read(reader, binary.LittleEndian, &value) != nil {
			return ErrInvalidState
		}
		if _, duplicate := words[address]; duplicate {
			return ErrInvalidState
		}
		words[address] = value
	}
	d.control, d.addressLow, d.addressHigh = control, addressLow, addressHigh
	d.words = words
	return nil
}

var (
	_ Device         = (*QualcommIndirectRegisterBus)(nil)
	_ StatefulDevice = (*QualcommIndirectRegisterBus)(nil)
)
