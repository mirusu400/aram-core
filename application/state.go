package application

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"io"
	"math"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	stateMagic         = "ARAMAPP\x00"
	stateVersion       = uint32(7)
	stateChecksumSize  = 32
	maxStateInputs     = 1024
	stateOverheadLimit = uint64(16 << 20)
)

func (m *Machine) SaveState(output io.Writer) error {
	if output == nil {
		return fmt.Errorf("save state: writer is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return fmt.Errorf("save from %s: %w", m.state, ErrInvalidState)
	}
	if m.lastResult.Err != nil {
		return fmt.Errorf("save state with pending execution error: %w", m.lastResult.Err)
	}
	if m.raptor != nil && m.raptor.Java != nil {
		return fmt.Errorf("save state: Raptor Java adapter state is not supported")
	}
	identity := m.cpu.Identity()
	if err := identity.Validate(); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if identity.Architecture != m.cpu.Architecture() {
		return fmt.Errorf("save state: CPU backend identity architecture mismatch")
	}
	contextData, err := m.cpu.SaveContext()
	if err != nil {
		return fmt.Errorf("save CPU context: %w", err)
	}
	if len(contextData) > guest.MaxStateContext {
		return fmt.Errorf("save CPU context: size %d exceeds limit", len(contextData))
	}
	sourceDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || len(sourceDigest) != sha256.Size {
		return fmt.Errorf("save state: source SHA-256 is unavailable")
	}

	writer := guest.NewStateWriter(output)
	writer.Write([]byte(stateMagic))
	writer.U32(stateVersion)
	writer.U8(uint8(m.state))
	writer.Write([]byte{0, 0, 0})
	writer.String16(identity.Name)
	writer.String16(identity.Version)
	writer.String16(string(identity.Architecture))
	writer.Write(sourceDigest)
	writer.String16(m.info.ProfileID)
	writer.String16(m.info.Name)
	writer.U32(m.info.EntryPoint)
	writer.U8(uint8(m.info.Mode))
	writer.Write([]byte{0, 0, 0})
	writer.U32(m.info.TextAddress)
	writer.U32(m.info.TextSize)
	writer.U32(m.info.BSSAddress)
	writer.U32(m.info.BSSSize)
	writer.U32(DefaultStackBase)
	writer.U32(DefaultStackSize)
	writer.U8(uint8(m.lastResult.Reason))
	writer.Write([]byte{0, 0, 0})
	writer.U64(m.lastResult.Instructions)
	writer.U32(m.lastResult.PC)
	writer.U32(uint32(len(contextData)))
	writer.Write(contextData)

	if err := guest.WriteMemoryState(writer, m.cpu, m.info.TextAddress, m.info.TextSize); err != nil {
		return err
	}
	if err := guest.WriteMemoryState(writer, m.cpu, m.info.BSSAddress, m.info.BSSSize); err != nil {
		return err
	}
	if err := guest.WriteMemoryState(writer, m.cpu, DefaultStackBase, DefaultStackSize); err != nil {
		return err
	}

	bounds := m.frame.Bounds()
	writer.U32(uint32(bounds.Dx()))
	writer.U32(uint32(bounds.Dy()))
	writer.U32(uint32(len(m.frame.Pix)))
	writer.Write(m.frame.Pix)
	writer.U32(uint32(len(m.input)))
	for _, event := range m.input {
		writer.String16(event.Control)
		if event.Pressed {
			writer.U8(1)
		} else {
			writer.U8(0)
		}
		writer.U8(0)
		writer.U64(uint64(event.At))
	}
	if err := m.writeWIPIState(writer); err != nil {
		return err
	}
	if err := m.writeMinigameState(writer); err != nil {
		return err
	}
	if err := m.writeRaptorState(writer); err != nil {
		return err
	}
	if err := m.writeKTFState(writer); err != nil {
		return err
	}
	if writer.Err != nil {
		return fmt.Errorf("save state at offset 0x%x: %w", writer.Offset, writer.Err)
	}
	if err := guest.WriteFull(output, writer.Digest()); err != nil {
		return fmt.Errorf("save state checksum: %w", err)
	}
	return nil
}

func (m *Machine) LoadState(input io.Reader) error {
	if input == nil {
		return fmt.Errorf("load state: reader is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning || m.state == machinecore.StateEmpty {
		return fmt.Errorf("load from %s: %w", m.state, ErrInvalidState)
	}
	if m.raptor != nil && m.raptor.Java != nil {
		return fmt.Errorf("load state: Raptor Java adapter state is not supported")
	}
	maximum := m.memoryLimit + uint64(len(m.frame.Pix)) + stateOverheadLimit
	if maximum <= math.MaxUint64-shared.MaxServicesStateBytes {
		maximum += shared.MaxServicesStateBytes
	} else {
		maximum = math.MaxUint64
	}
	if maximum > math.MaxInt64-1 {
		maximum = math.MaxInt64 - 1
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(maximum)+1))
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if uint64(len(data)) > maximum {
		return fmt.Errorf("read state: size exceeds %d-byte limit", maximum)
	}
	parsed, err := m.parseState(data)
	if err != nil {
		return err
	}
	// From this point restoration may replace guest time and media state. Make
	// any PCM published from the previous timeline unreachable immediately.
	m.beginAudioGeneration(0)

	if err := m.cpu.RestoreContext(parsed.context); err != nil {
		return fmt.Errorf("restore CPU context: %w", err)
	}
	if err := m.cpu.WriteMemory(m.info.TextAddress, parsed.text); err != nil {
		return fmt.Errorf("restore text memory: %w", err)
	}
	if err := m.cpu.WriteMemory(m.info.BSSAddress, parsed.bss); err != nil {
		return fmt.Errorf("restore BSS memory: %w", err)
	}
	if err := m.cpu.WriteMemory(DefaultStackBase, parsed.stack); err != nil {
		return fmt.Errorf("restore stack memory: %w", err)
	}
	if m.wipi != nil {
		if err := m.wipi.RestoreState(parsed.wipi); err != nil {
			return err
		}
	}
	if m.minigame != nil {
		if err := m.minigame.RestoreState(parsed.minigame); err != nil {
			return err
		}
	}
	if err := m.restoreRaptorState(parsed.raptor); err != nil {
		return err
	}
	if err := m.restoreKTFState(parsed.ktf); err != nil {
		return err
	}
	copy(m.frame.Pix, parsed.frame)
	m.input = parsed.input
	m.lastResult = parsed.lastResult
	m.state = parsed.state
	// The restored media state carries whatever policy was active when the save
	// was written; re-apply the current preference so a toggle since then wins.
	m.applyAudioMixMode()
	m.setAudioGenerationEpoch(m.guestTimeLocked())
	return nil
}

type parsedState struct {
	state      machinecore.State
	lastResult cpu.Result
	context    []byte
	text       []byte
	bss        []byte
	stack      []byte
	frame      []byte
	input      []machinecore.InputEvent
	wipi       *wipirt.SavedState
	minigame   *minigame.SavedState
	raptor     *raptorrt.SavedState
	ktf        *ktfrt.SavedState
}

func (m *Machine) parseState(data []byte) (parsedState, error) {
	if len(data) < len(stateMagic)+4+stateChecksumSize {
		return parsedState{}, fmt.Errorf("load state at offset 0x0: truncated header")
	}
	payload := data[:len(data)-stateChecksumSize]
	expectedChecksum := data[len(payload):]
	actualChecksum := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expectedChecksum, actualChecksum[:]) != 1 {
		return parsedState{}, fmt.Errorf("load state: checksum mismatch")
	}

	decoder := guest.StateDecoder{Reader: bytes.NewReader(payload)}
	if magic := decoder.Bytes(len(stateMagic)); string(magic) != stateMagic {
		return parsedState{}, decoder.Fail("magic mismatch")
	}
	if version := decoder.U32(); version != stateVersion {
		return parsedState{}, decoder.Fail(fmt.Sprintf("unsupported version %d", version))
	}
	savedState := machinecore.State(decoder.U8())
	decoder.Reserved(3)
	if !savedState.Valid() ||
		savedState == machinecore.StateEmpty ||
		savedState == machinecore.StateRunning ||
		savedState == machinecore.StateFaulted {
		return parsedState{}, decoder.Fail(fmt.Sprintf("invalid saved machine state %d", savedState))
	}

	identity := cpu.Identity{
		Name:         decoder.String16(),
		Version:      decoder.String16(),
		Architecture: cpu.Architecture(decoder.String16()),
	}
	sourceDigest := decoder.Bytes(sha256.Size)
	profileID := decoder.String16()
	imageName := decoder.String16()
	entryPoint := decoder.U32()
	mode := cpu.Mode(decoder.U8())
	decoder.Reserved(3)
	textAddress, textSize := decoder.U32(), decoder.U32()
	bssAddress, bssSize := decoder.U32(), decoder.U32()
	stackAddress, stackSize := decoder.U32(), decoder.U32()
	reason := cpu.StopReason(decoder.U8())
	decoder.Reserved(3)
	instructions := decoder.U64()
	pc := decoder.U32()
	contextSize := decoder.U32()
	if decoder.Err != nil {
		return parsedState{}, decoder.Err
	}

	currentIdentity := m.cpu.Identity()
	// A save is portable across CPU backends of the same architecture: the
	// backend name and version are recorded for provenance but do not gate
	// restore, so a state saved under the precise interpreter can load under a
	// fast backend and vice versa. Context-byte compatibility is still enforced
	// by the backend's RestoreContext, which rejects a format it cannot decode.
	if identity.Architecture != m.cpu.Architecture() {
		return parsedState{}, decoder.Fail(fmt.Sprintf(
			"CPU backend %s/%s/%s is incompatible with %s/%s/%s",
			identity.Name,
			identity.Version,
			identity.Architecture,
			currentIdentity.Name,
			currentIdentity.Version,
			currentIdentity.Architecture,
		))
	}
	expectedDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || subtle.ConstantTimeCompare(sourceDigest, expectedDigest) != 1 {
		return parsedState{}, decoder.Fail("source SHA-256 mismatch")
	}
	if profileID != m.info.ProfileID || imageName != m.info.Name ||
		entryPoint != m.info.EntryPoint || mode != m.info.Mode ||
		textAddress != m.info.TextAddress || textSize != m.info.TextSize ||
		bssAddress != m.info.BSSAddress || bssSize != m.info.BSSSize ||
		stackAddress != DefaultStackBase || stackSize != DefaultStackSize {
		return parsedState{}, decoder.Fail("machine geometry or profile mismatch")
	}
	if !mode.Valid() || !reason.Valid() {
		return parsedState{}, decoder.Fail("invalid CPU mode or stop reason")
	}
	if contextSize > guest.MaxStateContext {
		return parsedState{}, decoder.Fail(fmt.Sprintf(
			"CPU context size %d exceeds limit",
			contextSize,
		))
	}

	contextData := append([]byte(nil), decoder.Bytes(int(contextSize))...)
	text := append([]byte(nil), decoder.Bytes(int(textSize))...)
	bss := append([]byte(nil), decoder.Bytes(int(bssSize))...)
	stack := append([]byte(nil), decoder.Bytes(int(stackSize))...)
	frameWidth, frameHeight := decoder.U32(), decoder.U32()
	frameSize := decoder.U32()
	if frameWidth != uint32(m.frame.Bounds().Dx()) ||
		frameHeight != uint32(m.frame.Bounds().Dy()) ||
		uint64(frameSize) != uint64(len(m.frame.Pix)) {
		return parsedState{}, decoder.Fail("framebuffer geometry mismatch")
	}
	frame := append([]byte(nil), decoder.Bytes(int(frameSize))...)
	inputCount := decoder.U32()
	if inputCount > maxStateInputs {
		return parsedState{}, decoder.Fail(fmt.Sprintf(
			"input count %d exceeds limit",
			inputCount,
		))
	}
	input := make([]machinecore.InputEvent, 0, inputCount)
	var previousInputAt time.Duration
	for index := uint32(0); index < inputCount; index++ {
		control := decoder.String16()
		pressed := decoder.U8()
		event := machinecore.InputEvent{
			Control: control,
			Pressed: pressed != 0,
		}
		decoder.Reserved(1)
		event.At = time.Duration(int64(decoder.U64()))
		if decoder.Err != nil {
			return parsedState{}, decoder.Err
		}
		if pressed > 1 {
			return parsedState{}, decoder.Fail(fmt.Sprintf(
				"invalid input event %d boolean",
				index,
			))
		}
		if err := event.Validate(); err != nil ||
			(index != 0 && event.At < previousInputAt) {
			return parsedState{}, decoder.Fail(fmt.Sprintf("invalid input event %d: %v", index, err))
		}
		input = append(input, event)
		previousInputAt = event.At
	}
	public, err := m.parseWIPIState(&decoder)
	if err != nil {
		return parsedState{}, err
	}
	minigame, err := m.parseMinigameState(&decoder)
	if err != nil {
		return parsedState{}, err
	}
	raptorState, err := m.parseRaptorState(&decoder)
	if err != nil {
		return parsedState{}, err
	}
	ktfState, err := m.parseKTFState(&decoder)
	if err != nil {
		return parsedState{}, err
	}
	if decoder.Err != nil {
		return parsedState{}, decoder.Err
	}
	if decoder.Reader.Len() != 0 {
		return parsedState{}, decoder.Fail(fmt.Sprintf(
			"%d trailing payload bytes",
			decoder.Reader.Len(),
		))
	}
	return parsedState{
		state: savedState,
		lastResult: cpu.Result{
			Reason:       reason,
			Instructions: instructions,
			PC:           pc,
		},
		context:  contextData,
		text:     text,
		bss:      bss,
		stack:    stack,
		frame:    frame,
		input:    input,
		wipi:     public,
		minigame: minigame,
		raptor:   raptorState,
		ktf:      ktfState,
	}, nil
}
