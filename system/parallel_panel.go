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

// ParallelPanelWrite is one completed write on the external command/data bus.
// Command is the active command after a command-port write and the command
// associated with a data-port write.
type ParallelPanelWrite struct {
	Command uint16
	Value   uint16
	Data    bool
}

type ParallelPanelObserver func(ParallelPanelWrite)

// ParallelPanelController consumes the logical command/data stream behind the
// external bus transport. Controllers own panel semantics and framebuffer
// state; the transport owns physical width and port decoding.
type ParallelPanelController interface {
	Reset() error
	WriteCommand(uint16) error
	WriteData(uint16) error
	SaveState() ([]byte, error)
	LoadState([]byte) error
}

// ParallelPanelInterface models the two-port 16-bit command/data bus used by
// early panel initialization. Panel-controller register semantics and scanout
// remain separate devices layered behind this transport.
type ParallelPanelInterface struct {
	currentCommand uint16
	lastData       uint16
	commandWrites  uint64
	dataWrites     uint64
	controller     ParallelPanelController
	writeObserver  ParallelPanelObserver
}

func NewParallelPanelInterface() *ParallelPanelInterface {
	return &ParallelPanelInterface{}
}

func NewParallelPanelInterfaceWithController(
	controller ParallelPanelController,
) (*ParallelPanelInterface, error) {
	if controller == nil {
		return nil, fmt.Errorf("parallel-panel controller is nil")
	}
	panel := &ParallelPanelInterface{controller: controller}
	if err := panel.Reset(); err != nil {
		return nil, err
	}
	return panel, nil
}

func (p *ParallelPanelInterface) Reset() error {
	p.currentCommand = 0
	p.lastData = 0
	p.commandWrites = 0
	p.dataWrites = 0
	if p.controller != nil {
		return p.controller.Reset()
	}
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
		if p.controller != nil {
			if err := p.controller.WriteCommand(uint16(value)); err != nil {
				return fmt.Errorf("parallel-panel command 0x%x: %w", value, err)
			}
		}
		p.currentCommand = uint16(value)
		p.commandWrites++
		if p.writeObserver != nil {
			p.writeObserver(ParallelPanelWrite{Command: p.currentCommand, Value: uint16(value)})
		}
	case ParallelPanelDataOffset:
		if p.controller != nil {
			if err := p.controller.WriteData(uint16(value)); err != nil {
				return fmt.Errorf(
					"parallel-panel data 0x%x for command 0x%x: %w",
					value,
					p.currentCommand,
					err,
				)
			}
		}
		p.lastData = uint16(value)
		p.dataWrites++
		if p.writeObserver != nil {
			p.writeObserver(ParallelPanelWrite{
				Command: p.currentCommand,
				Value:   p.lastData,
				Data:    true,
			})
		}
	default:
		return fmt.Errorf("%w: write16 at 0x%x", ErrParallelPanelMMIO, offset)
	}
	return nil
}

// SetWriteObserver installs an optional diagnostic observer. Observers are
// intentionally excluded from reset and save state and cannot alter the
// guest-visible result of a completed transport write.
func (p *ParallelPanelInterface) SetWriteObserver(observer ParallelPanelObserver) {
	p.writeObserver = observer
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
	var controllerState []byte
	if p.controller != nil {
		var err error
		controllerState, err = p.controller.SaveState()
		if err != nil {
			return nil, err
		}
	}
	state := make([]byte, 4+4+2+2+8+8+4+len(controllerState))
	copy(state, "PPNL")
	binary.LittleEndian.PutUint32(state[4:8], 2)
	binary.LittleEndian.PutUint16(state[8:10], p.currentCommand)
	binary.LittleEndian.PutUint16(state[10:12], p.lastData)
	binary.LittleEndian.PutUint64(state[12:20], p.commandWrites)
	binary.LittleEndian.PutUint64(state[20:28], p.dataWrites)
	binary.LittleEndian.PutUint32(state[28:32], uint32(len(controllerState)))
	copy(state[32:], controllerState)
	return state, nil
}

func (p *ParallelPanelInterface) LoadState(state []byte) error {
	if len(state) < 32 || string(state[:4]) != "PPNL" ||
		binary.LittleEndian.Uint32(state[4:8]) != 2 {
		return ErrInvalidState
	}
	controllerLength := binary.LittleEndian.Uint32(state[28:32])
	if uint64(controllerLength) != uint64(len(state)-32) ||
		(p.controller == nil) != (controllerLength == 0) {
		return ErrInvalidState
	}
	if p.controller != nil {
		if err := p.controller.LoadState(state[32:]); err != nil {
			return err
		}
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
