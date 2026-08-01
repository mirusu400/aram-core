package application

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"math"
	"time"
	"unicode/utf8"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

const (
	stateMagic         = "ARAMAPP\x00"
	stateVersion       = uint32(7)
	stateChecksumSize  = 32
	maxStateContext    = 1 << 20
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
	if m.raptor != nil && m.raptor.java != nil {
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
	if len(contextData) > maxStateContext {
		return fmt.Errorf("save CPU context: size %d exceeds limit", len(contextData))
	}
	sourceDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || len(sourceDigest) != sha256.Size {
		return fmt.Errorf("save state: source SHA-256 is unavailable")
	}

	writer := newStateWriter(output)
	writer.write([]byte(stateMagic))
	writer.u32(stateVersion)
	writer.u8(uint8(m.state))
	writer.write([]byte{0, 0, 0})
	writer.string16(identity.Name)
	writer.string16(identity.Version)
	writer.string16(string(identity.Architecture))
	writer.write(sourceDigest)
	writer.string16(m.info.ProfileID)
	writer.string16(m.info.Name)
	writer.u32(m.info.EntryPoint)
	writer.u8(uint8(m.info.Mode))
	writer.write([]byte{0, 0, 0})
	writer.u32(m.info.TextAddress)
	writer.u32(m.info.TextSize)
	writer.u32(m.info.BSSAddress)
	writer.u32(m.info.BSSSize)
	writer.u32(DefaultStackBase)
	writer.u32(DefaultStackSize)
	writer.u8(uint8(m.lastResult.Reason))
	writer.write([]byte{0, 0, 0})
	writer.u64(m.lastResult.Instructions)
	writer.u32(m.lastResult.PC)
	writer.u32(uint32(len(contextData)))
	writer.write(contextData)

	if err := m.writeMemoryState(writer, m.info.TextAddress, m.info.TextSize); err != nil {
		return err
	}
	if err := m.writeMemoryState(writer, m.info.BSSAddress, m.info.BSSSize); err != nil {
		return err
	}
	if err := m.writeMemoryState(writer, DefaultStackBase, DefaultStackSize); err != nil {
		return err
	}

	bounds := m.frame.Bounds()
	writer.u32(uint32(bounds.Dx()))
	writer.u32(uint32(bounds.Dy()))
	writer.u32(uint32(len(m.frame.Pix)))
	writer.write(m.frame.Pix)
	writer.u32(uint32(len(m.input)))
	for _, event := range m.input {
		writer.string16(event.Control)
		if event.Pressed {
			writer.u8(1)
		} else {
			writer.u8(0)
		}
		writer.u8(0)
		writer.u64(uint64(event.At))
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
	if writer.err != nil {
		return fmt.Errorf("save state at offset 0x%x: %w", writer.offset, writer.err)
	}
	if err := writeFull(output, writer.digest()); err != nil {
		return fmt.Errorf("save state checksum: %w", err)
	}
	return nil
}

func (m *Machine) writeMemoryState(
	writer *stateWriter,
	address uint32,
	size uint32,
) error {
	buffer := make([]byte, min(uint32(64<<10), size))
	var offset uint32
	for offset < size {
		count := min(uint32(len(buffer)), size-offset)
		chunk := buffer[:count]
		if err := m.cpu.ReadMemory(address+offset, chunk); err != nil {
			return fmt.Errorf(
				"save guest memory 0x%08x at +0x%x: %w",
				address,
				offset,
				err,
			)
		}
		writer.write(chunk)
		if writer.err != nil {
			return fmt.Errorf("save state at offset 0x%x: %w", writer.offset, writer.err)
		}
		offset += count
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
	if m.raptor != nil && m.raptor.java != nil {
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
		if err := m.wipi.restoreState(parsed.wipi); err != nil {
			return err
		}
	}
	if m.minigame != nil {
		if err := m.minigame.restoreState(parsed.minigame); err != nil {
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
	wipi       *wipiSavedState
	minigame   *minigameSavedState
	raptor     *raptorSavedState
	ktf        *ktfSavedState
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

	decoder := stateDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(stateMagic)); string(magic) != stateMagic {
		return parsedState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != stateVersion {
		return parsedState{}, decoder.fail(fmt.Sprintf("unsupported version %d", version))
	}
	savedState := machinecore.State(decoder.u8())
	decoder.reserved(3)
	if !savedState.Valid() ||
		savedState == machinecore.StateEmpty ||
		savedState == machinecore.StateRunning ||
		savedState == machinecore.StateFaulted {
		return parsedState{}, decoder.fail(fmt.Sprintf("invalid saved machine state %d", savedState))
	}

	identity := cpu.Identity{
		Name:         decoder.string16(),
		Version:      decoder.string16(),
		Architecture: cpu.Architecture(decoder.string16()),
	}
	sourceDigest := decoder.bytes(sha256.Size)
	profileID := decoder.string16()
	imageName := decoder.string16()
	entryPoint := decoder.u32()
	mode := cpu.Mode(decoder.u8())
	decoder.reserved(3)
	textAddress, textSize := decoder.u32(), decoder.u32()
	bssAddress, bssSize := decoder.u32(), decoder.u32()
	stackAddress, stackSize := decoder.u32(), decoder.u32()
	reason := cpu.StopReason(decoder.u8())
	decoder.reserved(3)
	instructions := decoder.u64()
	pc := decoder.u32()
	contextSize := decoder.u32()
	if decoder.err != nil {
		return parsedState{}, decoder.err
	}

	currentIdentity := m.cpu.Identity()
	if identity != currentIdentity || identity.Architecture != m.cpu.Architecture() {
		return parsedState{}, decoder.fail(fmt.Sprintf(
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
		return parsedState{}, decoder.fail("source SHA-256 mismatch")
	}
	if profileID != m.info.ProfileID || imageName != m.info.Name ||
		entryPoint != m.info.EntryPoint || mode != m.info.Mode ||
		textAddress != m.info.TextAddress || textSize != m.info.TextSize ||
		bssAddress != m.info.BSSAddress || bssSize != m.info.BSSSize ||
		stackAddress != DefaultStackBase || stackSize != DefaultStackSize {
		return parsedState{}, decoder.fail("machine geometry or profile mismatch")
	}
	if !mode.Valid() || !reason.Valid() {
		return parsedState{}, decoder.fail("invalid CPU mode or stop reason")
	}
	if contextSize > maxStateContext {
		return parsedState{}, decoder.fail(fmt.Sprintf(
			"CPU context size %d exceeds limit",
			contextSize,
		))
	}

	contextData := append([]byte(nil), decoder.bytes(int(contextSize))...)
	text := append([]byte(nil), decoder.bytes(int(textSize))...)
	bss := append([]byte(nil), decoder.bytes(int(bssSize))...)
	stack := append([]byte(nil), decoder.bytes(int(stackSize))...)
	frameWidth, frameHeight := decoder.u32(), decoder.u32()
	frameSize := decoder.u32()
	if frameWidth != uint32(m.frame.Bounds().Dx()) ||
		frameHeight != uint32(m.frame.Bounds().Dy()) ||
		uint64(frameSize) != uint64(len(m.frame.Pix)) {
		return parsedState{}, decoder.fail("framebuffer geometry mismatch")
	}
	frame := append([]byte(nil), decoder.bytes(int(frameSize))...)
	inputCount := decoder.u32()
	if inputCount > maxStateInputs {
		return parsedState{}, decoder.fail(fmt.Sprintf(
			"input count %d exceeds limit",
			inputCount,
		))
	}
	input := make([]machinecore.InputEvent, 0, inputCount)
	var previousInputAt time.Duration
	for index := uint32(0); index < inputCount; index++ {
		control := decoder.string16()
		pressed := decoder.u8()
		event := machinecore.InputEvent{
			Control: control,
			Pressed: pressed != 0,
		}
		decoder.reserved(1)
		event.At = time.Duration(int64(decoder.u64()))
		if decoder.err != nil {
			return parsedState{}, decoder.err
		}
		if pressed > 1 {
			return parsedState{}, decoder.fail(fmt.Sprintf(
				"invalid input event %d boolean",
				index,
			))
		}
		if err := event.Validate(); err != nil ||
			(index != 0 && event.At < previousInputAt) {
			return parsedState{}, decoder.fail(fmt.Sprintf("invalid input event %d: %v", index, err))
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
	if decoder.err != nil {
		return parsedState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return parsedState{}, decoder.fail(fmt.Sprintf(
			"%d trailing payload bytes",
			decoder.reader.Len(),
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

type stateWriter struct {
	output io.Writer
	hash   hash.Hash
	offset int64
	err    error
}

func newStateWriter(output io.Writer) *stateWriter {
	digest := sha256.New()
	return &stateWriter{
		output: io.MultiWriter(output, digest),
		hash:   digest,
	}
}

func (w *stateWriter) write(data []byte) {
	if w.err != nil {
		return
	}
	count, err := w.output.Write(data)
	w.offset += int64(count)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}
	w.err = err
}

func (w *stateWriter) u8(value uint8) {
	w.write([]byte{value})
}

func (w *stateWriter) u32(value uint32) {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	w.write(data[:])
}

func (w *stateWriter) u64(value uint64) {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	w.write(data[:])
}

func (w *stateWriter) string16(value string) {
	if w.err != nil {
		return
	}
	if !utf8.ValidString(value) || len(value) > math.MaxUint16 {
		w.err = fmt.Errorf("invalid or oversized state string")
		return
	}
	var size [2]byte
	binary.LittleEndian.PutUint16(size[:], uint16(len(value)))
	w.write(size[:])
	w.write([]byte(value))
}

func (w *stateWriter) digest() []byte {
	return w.hash.Sum(nil)
}

type stateDecoder struct {
	reader *bytes.Reader
	offset int64
	err    error
}

func (d *stateDecoder) bytes(size int) []byte {
	if d.err != nil {
		return nil
	}
	if size < 0 || int64(size) > int64(d.reader.Len()) {
		d.err = fmt.Errorf("load state at offset 0x%x: truncated field", d.offset)
		return nil
	}
	data := make([]byte, size)
	count, err := io.ReadFull(d.reader, data)
	d.offset += int64(count)
	if err != nil {
		d.err = fmt.Errorf("load state at offset 0x%x: %w", d.offset-int64(count), err)
		return nil
	}
	return data
}

func (d *stateDecoder) u8() uint8 {
	data := d.bytes(1)
	if len(data) != 1 {
		return 0
	}
	return data[0]
}

func (d *stateDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data)
}

func (d *stateDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}

func (d *stateDecoder) string16() string {
	sizeData := d.bytes(2)
	if len(sizeData) != 2 {
		return ""
	}
	value := string(d.bytes(int(binary.LittleEndian.Uint16(sizeData))))
	if d.err == nil && !utf8.ValidString(value) {
		d.err = fmt.Errorf("load state at offset 0x%x: invalid UTF-8 string", d.offset-int64(len(value)))
	}
	return value
}

func (d *stateDecoder) reserved(size int) {
	data := d.bytes(size)
	if d.err == nil {
		for _, value := range data {
			if value != 0 {
				d.err = fmt.Errorf("load state at offset 0x%x: nonzero reserved field", d.offset-int64(size))
				return
			}
		}
	}
}

func (d *stateDecoder) fail(reason string) error {
	if d.err != nil {
		return d.err
	}
	return fmt.Errorf("load state at offset 0x%x: %s", d.offset, reason)
}

func writeFull(output io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := output.Write(data)
		if count > 0 {
			data = data[count:]
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
