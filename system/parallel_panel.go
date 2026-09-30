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

// ParallelPanelPort presents one 16-bit command or data port as an independent
// MMIO device. Boards that decode the D/C signal with a sparse external-bus
// address can map these two aliases without claiming the unrelated addresses
// between them. Both aliases drive one transport, so the command port owns its
// reset and serialization; the data port carries only its own role.
type ParallelPanelPort struct {
	panel *ParallelPanelInterface
	data  bool
}

// ParallelPanelSelectorPort models a two-register panel transport where the
// first halfword selects command (usually zero) or data (usually one), and a
// write to the second halfword performs the selected transfer. The selector
// and transfer aliases share one state object and one logical panel transport.
type ParallelPanelSelectorPort struct {
	transport *parallelPanelSelectorTransport
	transfer  bool
}

type parallelPanelSelectorTransport struct {
	panel         *ParallelPanelInterface
	commandSelect uint16
	dataSelect    uint16
	selected      uint16
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

func NewParallelPanelCommandPort(panel *ParallelPanelInterface) (*ParallelPanelPort, error) {
	return newParallelPanelPort(panel, false)
}

func NewParallelPanelDataPort(panel *ParallelPanelInterface) (*ParallelPanelPort, error) {
	return newParallelPanelPort(panel, true)
}

// NewParallelPanelSelectorPorts creates the paired selector and transfer
// aliases used by indirect external-bus panel interfaces.
func NewParallelPanelSelectorPorts(
	panel *ParallelPanelInterface,
	commandSelect uint16,
	dataSelect uint16,
) (*ParallelPanelSelectorPort, *ParallelPanelSelectorPort, error) {
	if panel == nil || commandSelect == dataSelect {
		return nil, nil, fmt.Errorf("invalid parallel-panel selector transport")
	}
	transport := &parallelPanelSelectorTransport{
		panel: panel, commandSelect: commandSelect, dataSelect: dataSelect,
		selected: commandSelect,
	}
	return &ParallelPanelSelectorPort{transport: transport},
		&ParallelPanelSelectorPort{transport: transport, transfer: true}, nil
}

func newParallelPanelPort(panel *ParallelPanelInterface, data bool) (*ParallelPanelPort, error) {
	if panel == nil {
		return nil, fmt.Errorf("parallel-panel port transport is nil")
	}
	return &ParallelPanelPort{panel: panel, data: data}, nil
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

// Reset defers to the command port. Resetting the shared transport once keeps
// a two-alias board identical to a single contiguous window.
func (p *ParallelPanelPort) Reset() error {
	if p.data {
		return nil
	}
	return p.panel.Reset()
}

func (p *ParallelPanelPort) Read(offset uint32, width Width) (uint32, error) {
	if offset != 0 {
		return 0, fmt.Errorf("%w: read%d at sparse-port offset 0x%x", ErrParallelPanelMMIO, width*8, offset)
	}
	panelOffset := uint32(0)
	if p.data {
		panelOffset = ParallelPanelDataOffset
	}
	return p.panel.Read(panelOffset, width)
}

func (p *ParallelPanelPort) Write(offset uint32, width Width, value uint32) error {
	if offset != 0 {
		return fmt.Errorf("%w: write%d at sparse-port offset 0x%x", ErrParallelPanelMMIO, width*8, offset)
	}
	panelOffset := uint32(0)
	if p.data {
		panelOffset = ParallelPanelDataOffset
	}
	return p.panel.Write(panelOffset, width, value)
}

// SaveState serializes the shared transport from the command port only. The
// panel state embeds a full pixel surface, so letting both aliases carry it
// would duplicate every frame in each machine snapshot.
func (p *ParallelPanelPort) SaveState() ([]byte, error) {
	header := make([]byte, 9)
	copy(header, "PPPT")
	binary.LittleEndian.PutUint32(header[4:8], 1)
	if p.data {
		header[8] = 1
		return header, nil
	}
	state, err := p.panel.SaveState()
	if err != nil {
		return nil, err
	}
	return append(header, state...), nil
}

func (p *ParallelPanelPort) LoadState(state []byte) error {
	wantData := byte(0)
	if p.data {
		wantData = 1
	}
	if len(state) < 9 || string(state[:4]) != "PPPT" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || state[8] != wantData {
		return ErrInvalidState
	}
	if p.data {
		if len(state) != 9 {
			return ErrInvalidState
		}
		return nil
	}
	return p.panel.LoadState(state[9:])
}

func (p *ParallelPanelSelectorPort) Reset() error {
	if p.transfer {
		return nil
	}
	p.transport.selected = p.transport.commandSelect
	return p.transport.panel.Reset()
}

func (p *ParallelPanelSelectorPort) Read(offset uint32, width Width) (uint32, error) {
	return 0, fmt.Errorf(
		"%w: read%d at selector-port offset 0x%x",
		ErrParallelPanelMMIO,
		width*8,
		offset,
	)
}

func (p *ParallelPanelSelectorPort) Write(offset uint32, width Width, value uint32) error {
	if offset != 0 || width != Width16 || value > 0xffff {
		return fmt.Errorf(
			"%w: write%d value 0x%x at selector-port offset 0x%x",
			ErrParallelPanelMMIO,
			width*8,
			value,
			offset,
		)
	}
	if !p.transfer {
		selected := uint16(value)
		if selected != p.transport.commandSelect && selected != p.transport.dataSelect {
			return fmt.Errorf("%w: unsupported panel selector 0x%x", ErrParallelPanelMMIO, value)
		}
		p.transport.selected = selected
		return nil
	}
	panelOffset := uint32(0)
	if p.transport.selected == p.transport.dataSelect {
		panelOffset = ParallelPanelDataOffset
	}
	return p.transport.panel.Write(panelOffset, width, value)
}

func (p *ParallelPanelSelectorPort) SaveState() ([]byte, error) {
	header := make([]byte, 16)
	copy(header, "PPSL")
	binary.LittleEndian.PutUint32(header[4:8], 1)
	binary.LittleEndian.PutUint16(header[10:12], p.transport.commandSelect)
	binary.LittleEndian.PutUint16(header[12:14], p.transport.dataSelect)
	if p.transfer {
		header[8] = 1
		return header, nil
	}
	binary.LittleEndian.PutUint16(header[14:16], p.transport.selected)
	state, err := p.transport.panel.SaveState()
	if err != nil {
		return nil, err
	}
	return append(header, state...), nil
}

func (p *ParallelPanelSelectorPort) LoadState(state []byte) error {
	wantTransfer := byte(0)
	if p.transfer {
		wantTransfer = 1
	}
	if len(state) < 16 || string(state[:4]) != "PPSL" ||
		binary.LittleEndian.Uint32(state[4:8]) != 1 || state[8] != wantTransfer || state[9] != 0 ||
		binary.LittleEndian.Uint16(state[10:12]) != p.transport.commandSelect ||
		binary.LittleEndian.Uint16(state[12:14]) != p.transport.dataSelect {
		return ErrInvalidState
	}
	if p.transfer {
		if len(state) != 16 || binary.LittleEndian.Uint16(state[14:16]) != 0 {
			return ErrInvalidState
		}
		return nil
	}
	selected := binary.LittleEndian.Uint16(state[14:16])
	if selected != p.transport.commandSelect && selected != p.transport.dataSelect {
		return ErrInvalidState
	}
	if err := p.transport.panel.LoadState(state[16:]); err != nil {
		return err
	}
	p.transport.selected = selected
	return nil
}

var (
	_ Device         = (*ParallelPanelInterface)(nil)
	_ StatefulDevice = (*ParallelPanelInterface)(nil)
	_ StatefulDevice = (*ParallelPanelPort)(nil)
	_ StatefulDevice = (*ParallelPanelSelectorPort)(nil)
)
