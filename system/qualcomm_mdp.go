package system

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	qualcommMDPMaximumPendingScripts = 16
	qualcommMDPMaximumScriptWords    = 4096
	qualcommMDPMaximumScriptDepth    = 16
)

var ErrQualcommMDP = errors.New("invalid Qualcomm MDP script")

// QualcommMDPProfile identifies the command-list registers and source format
// used by the script-driven MDP generation found in older Qualcomm handsets.
// The register aperture and completion interrupt remain owned by
// QualcommBootControl; the MDP engine supplies the memory-to-panel side effect.
type QualcommMDPProfile struct {
	CompletionStartOffset uint32
	ScriptPointerOffset   uint32
	RGB565SourceFormat    uint32
}

func (p QualcommMDPProfile) validate() error {
	if p.CompletionStartOffset%4 != 0 ||
		p.CompletionStartOffset >= QualcommBootControlWindowSize ||
		p.ScriptPointerOffset%4 != 0 || p.ScriptPointerOffset >= QualcommBootControlWindowSize ||
		p.RGB565SourceFormat == 0 {
		return ErrQualcommMDP
	}
	return nil
}

// QualcommMDPScriptEngine executes the bounded display subset of an MDP
// command list. Kickoff is queued while the physical bus lock is held and the
// list is consumed later by QualcommBootControl.Advance, where guest RAM can be
// read without re-entering the bus lock.
type QualcommMDPScriptEngine struct {
	bus     *Bus
	panel   *DCSPanelController
	profile QualcommMDPProfile
	pending []uint32
}

func NewQualcommMDPScriptEngine(
	bus *Bus,
	panel *DCSPanelController,
	profile QualcommMDPProfile,
) (*QualcommMDPScriptEngine, error) {
	if bus == nil || panel == nil {
		return nil, fmt.Errorf("%w: nil bus or panel", ErrQualcommMDP)
	}
	if err := profile.validate(); err != nil {
		return nil, fmt.Errorf("Qualcomm MDP profile: %w", err)
	}
	return &QualcommMDPScriptEngine{bus: bus, panel: panel, profile: profile}, nil
}

func (e *QualcommMDPScriptEngine) Reset() error {
	e.pending = nil
	return nil
}

func (e *QualcommMDPScriptEngine) QueueCompletion(
	registerValue func(offset uint32) (uint32, bool),
) error {
	if registerValue == nil {
		return fmt.Errorf("%w: nil register reader", ErrQualcommMDP)
	}
	pointer, ok := registerValue(e.profile.ScriptPointerOffset)
	if !ok {
		return fmt.Errorf(
			"%w: script-pointer register 0x%x is unavailable",
			ErrQualcommMDP,
			e.profile.ScriptPointerOffset,
		)
	}
	return e.QueueScript(pointer)
}

// QueueScript is exposed for deterministic diagnostics and focused device
// tests. Normal guest execution queues the pointer through QueueCompletion.
func (e *QualcommMDPScriptEngine) QueueScript(address uint32) error {
	if address == 0 || address%4 != 0 {
		return fmt.Errorf("%w: unaligned script address 0x%08x", ErrQualcommMDP, address)
	}
	if len(e.pending) >= qualcommMDPMaximumPendingScripts {
		return fmt.Errorf("%w: pending command-list limit exceeded", ErrQualcommMDP)
	}
	e.pending = append(e.pending, address)
	return nil
}

func (e *QualcommMDPScriptEngine) Advance(_ uint64) error {
	for len(e.pending) != 0 {
		address := e.pending[0]
		e.pending = e.pending[1:]
		state := qualcommMDPTransferState{}
		budget := qualcommMDPMaximumScriptWords
		if err := e.executeScript(address, &state, &budget, 0, make(map[uint32]struct{})); err != nil {
			return fmt.Errorf("execute Qualcomm MDP script at 0x%08x: %w", address, err)
		}
	}
	return nil
}

type qualcommMDPTransferState struct {
	sourceAddress uint32
	stride        uint32
	sourceFormat  uint32
	sourceSet     bool
	transferred   bool
}

func (e *QualcommMDPScriptEngine) executeScript(
	address uint32,
	state *qualcommMDPTransferState,
	budget *int,
	depth int,
	active map[uint32]struct{},
) error {
	if depth >= qualcommMDPMaximumScriptDepth {
		return fmt.Errorf("%w: command-list nesting limit exceeded", ErrQualcommMDP)
	}
	if _, recursive := active[address]; recursive {
		return fmt.Errorf("%w: recursive command list at 0x%08x", ErrQualcommMDP, address)
	}
	active[address] = struct{}{}
	defer delete(active, address)

	for cursor := address; ; cursor += 4 {
		word, err := e.readScriptWord(cursor, budget)
		if err != nil {
			return err
		}
		opcode := uint8(word >> 24)
		switch opcode {
		case 0x01, 0x04, 0x06:
			return nil
		case 0x03:
			outputAddress, nextErr := e.readScriptWord(cursor+4, budget)
			if nextErr != nil {
				return nextErr
			}
			cursor += 4
			memoryWrite, outputErr := e.executeOutputScript(outputAddress, budget)
			if outputErr != nil {
				return outputErr
			}
			if memoryWrite && !state.transferred {
				if err := e.transferRGB565(state); err != nil {
					return err
				}
				state.transferred = true
			}
		case 0x05:
			nestedAddress, nextErr := e.readScriptWord(cursor+4, budget)
			if nextErr != nil {
				return nextErr
			}
			cursor += 4
			if err := e.executeScript(nestedAddress, state, budget, depth+1, active); err != nil {
				return err
			}
		case 0x10:
			sourceAddress, nextErr := e.readScriptWord(cursor+4, budget)
			if nextErr != nil {
				return nextErr
			}
			cursor += 4
			state.sourceAddress = sourceAddress
			state.sourceSet = true
		case 0x11:
			state.stride = word & 0xffff
		case 0x12:
			state.sourceFormat = word & 0xff
		default:
			// Remaining words configure scaling, color conversion, blending,
			// destination geometry, and synchronization. They do not alter a
			// direct RGB565 copy; retain them as bounded no-ops until a profile
			// selects a format or transform that needs those units.
		}
	}
}

func (e *QualcommMDPScriptEngine) executeOutputScript(address uint32, budget *int) (bool, error) {
	if address == 0 || address%4 != 0 {
		return false, fmt.Errorf("%w: unaligned output script 0x%08x", ErrQualcommMDP, address)
	}
	memoryWrite := false
	for cursor := address; ; cursor += 4 {
		word, err := e.readScriptWord(cursor, budget)
		if err != nil {
			return false, err
		}
		switch uint8(word >> 24) {
		case 0x00:
		case 0x04:
			return memoryWrite, nil
		case 0x0a, 0x0c:
			if _, err := e.readScriptWord(cursor+4, budget); err != nil {
				return false, err
			}
			cursor += 4
		case 0x0b:
			command := uint16(word)
			if err := e.panel.WriteCommand(command); err != nil {
				return false, fmt.Errorf("%w: panel command 0x%x: %v", ErrQualcommMDP, command, err)
			}
			if e.panel.isMemoryWriteCommand(command) {
				memoryWrite = true
			}
		case 0x0e:
			if err := e.panel.WriteData(uint16(word)); err != nil {
				return false, fmt.Errorf("%w: panel data 0x%x: %v", ErrQualcommMDP, uint16(word), err)
			}
		default:
			return false, fmt.Errorf(
				"%w: unsupported output opcode 0x%02x at 0x%08x",
				ErrQualcommMDP,
				uint8(word>>24),
				cursor,
			)
		}
	}
}

func (e *QualcommMDPScriptEngine) transferRGB565(state *qualcommMDPTransferState) error {
	if !state.sourceSet || state.stride == 0 {
		return fmt.Errorf("%w: incomplete RGB565 source configuration", ErrQualcommMDP)
	}
	if state.sourceFormat != e.profile.RGB565SourceFormat {
		return fmt.Errorf(
			"%w: unsupported source format 0x%x",
			ErrQualcommMDP,
			state.sourceFormat,
		)
	}
	columnStart, columnEnd, pageStart, pageEnd := e.panel.AddressWindow()
	width := uint32(columnEnd-columnStart) + 1
	height := uint32(pageEnd-pageStart) + 1
	if state.stride < width*2 {
		return fmt.Errorf(
			"%w: source stride %d is smaller than %d RGB565 bytes",
			ErrQualcommMDP,
			state.stride,
			width*2,
		)
	}
	var encoded [2]byte
	for y := uint32(0); y < height; y++ {
		rowAddress := uint64(state.sourceAddress) + uint64(y)*uint64(state.stride)
		rowEnd := rowAddress + uint64(width)*2
		if rowEnd > 1<<32 {
			return fmt.Errorf("%w: RGB565 source address overflow", ErrQualcommMDP)
		}
		for x := uint32(0); x < width; x++ {
			address := uint32(rowAddress + uint64(x)*2)
			if err := e.bus.Read(address, encoded[:], cpu.PermissionRead); err != nil {
				return fmt.Errorf("read RGB565 pixel at 0x%08x: %w", address, err)
			}
			if err := e.panel.WriteData(binary.LittleEndian.Uint16(encoded[:])); err != nil {
				return fmt.Errorf("%w: write RGB565 pixel: %v", ErrQualcommMDP, err)
			}
		}
	}
	return nil
}

func (e *QualcommMDPScriptEngine) readScriptWord(address uint32, budget *int) (uint32, error) {
	if address%4 != 0 {
		return 0, fmt.Errorf("%w: unaligned command word at 0x%08x", ErrQualcommMDP, address)
	}
	if *budget == 0 {
		return 0, fmt.Errorf("%w: command-list word limit exceeded", ErrQualcommMDP)
	}
	*budget = *budget - 1
	var encoded [4]byte
	if err := e.bus.Read(address, encoded[:], cpu.PermissionRead); err != nil {
		return 0, fmt.Errorf("read command word at 0x%08x: %w", address, err)
	}
	return binary.LittleEndian.Uint32(encoded[:]), nil
}
