package system

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ParallelPanelWindowSize = 0x00200000
	ParallelPanelDataOffset = 0x00100000
)

var ErrParallelPanelMMIO = errors.New("unsupported parallel-panel access")

// ParallelPanelInterface models the two-port 16-bit command/data bus used by
// early panel initialization. Panel-controller register semantics and scanout
// remain separate devices layered behind this transport.
type ParallelPanelInterface struct {
	currentCommand uint16
	lastData       uint16
	commandWrites  uint64
	dataWrites     uint64
}

func NewParallelPanelInterface() *ParallelPanelInterface {
	return &ParallelPanelInterface{}
}

func (p *ParallelPanelInterface) Reset() error {
	p.currentCommand = 0
	p.lastData = 0
	p.commandWrites = 0
	p.dataWrites = 0
	return nil
}

func (p *ParallelPanelInterface) Read(offset uint32, width Width) (uint32, error) {
	return 0, fmt.Errorf("%w: read%d at 0x%x", ErrParallelPanelMMIO, width*8, offset)
}

func (p *ParallelPanelInterface) Write(offset uint32, width Width, value uint32) error {
	if width != Width16 {
		return fmt.Errorf("%w: write%d at 0x%x", ErrParallelPanelMMIO, width*8, offset)
	}
	switch offset {
	case 0:
		p.currentCommand = uint16(value)
		p.commandWrites++
	case ParallelPanelDataOffset:
		p.lastData = uint16(value)
		p.dataWrites++
	default:
		return fmt.Errorf("%w: write16 at 0x%x", ErrParallelPanelMMIO, offset)
	}
	return nil
}

func (p *ParallelPanelInterface) CurrentCommand() uint16 {
	return p.currentCommand
}

func (p *ParallelPanelInterface) LastData() uint16 {
	return p.lastData
}

func (p *ParallelPanelInterface) WriteCounts() (commands, data uint64) {
	return p.commandWrites, p.dataWrites
}

func (p *ParallelPanelInterface) SaveState() ([]byte, error) {
	state := make([]byte, 4+4+2+2+8+8)
	copy(state, "PPNL")
	binary.LittleEndian.PutUint32(state[4:8], 1)
	binary.LittleEndian.PutUint16(state[8:10], p.currentCommand)
	binary.LittleEndian.PutUint16(state[10:12], p.lastData)
	binary.LittleEndian.PutUint64(state[12:20], p.commandWrites)
	binary.LittleEndian.PutUint64(state[20:28], p.dataWrites)
	return state, nil
}

func (p *ParallelPanelInterface) LoadState(state []byte) error {
	if len(state) != 28 || string(state[:4]) != "PPNL" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 {
		return ErrInvalidState
	}
	p.currentCommand = binary.LittleEndian.Uint16(state[8:10])
	p.lastData = binary.LittleEndian.Uint16(state[10:12])
	p.commandWrites = binary.LittleEndian.Uint64(state[12:20])
	p.dataWrites = binary.LittleEndian.Uint64(state[20:28])
	return nil
}

var (
	_ Device         = (*ParallelPanelInterface)(nil)
	_ StatefulDevice = (*ParallelPanelInterface)(nil)
)
