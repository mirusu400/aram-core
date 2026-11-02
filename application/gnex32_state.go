package application

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gnex32"
)

const (
	gnex32MachineStateMagic    = "ARAMGNX\x00"
	gnex32MachineStateVersion  = uint32(1)
	maxGNEX32MachineStateBytes = gnex32.MaxRuntimeStateBytes + uint64(1<<20)
)

var ErrGNEX32NetworkStateActive = errors.New("application: GNEX32 live network state is not serializable")

type parsedGNEX32MachineState struct {
	state                  machinecore.State
	audioGeneration        uint64
	audioGenerationPending bool
	audioEpochGuestNS      int64
	audioCursorSample      uint64
	audioCursorValid       bool
	mediaOutputRevision    uint64
	runtime                []byte
}

func (m *gnex32Machine) SaveState(output io.Writer) error {
	if output == nil {
		return fmt.Errorf("save GNEX32 state: writer is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return fmt.Errorf("save GNEX32 state from %s: %w", m.state, ErrInvalidState)
	}
	digest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("save GNEX32 state: source SHA-256 is unavailable")
	}
	if m.runtime.NetworkActive() {
		return fmt.Errorf("save GNEX32 state: %w", ErrGNEX32NetworkStateActive)
	}
	runtimeState, err := m.runtime.MarshalBinary()
	if err != nil {
		return fmt.Errorf("save GNEX32 runtime: %w", err)
	}
	var data bytes.Buffer
	data.WriteString(gnex32MachineStateMagic)
	writeGNEX32StateU32(&data, gnex32MachineStateVersion)
	data.WriteByte(byte(m.state))
	writeGNEX32StateBool(&data, m.audioGenerationPending)
	writeGNEX32StateBool(&data, m.audioCursorValid)
	data.WriteByte(0)
	data.Write(digest)
	writeGNEX32StateU64(&data, m.audioGeneration)
	writeGNEX32StateU64(&data, uint64(m.audioEpochGuestNS))
	writeGNEX32StateU64(&data, m.audioCursorSample)
	writeGNEX32StateU64(&data, m.mediaOutputRevision)
	writeGNEX32StateU64(&data, uint64(len(runtimeState)))
	data.Write(runtimeState)
	checksum := sha256.Sum256(data.Bytes())
	data.Write(checksum[:])
	if uint64(data.Len()) > maxGNEX32MachineStateBytes {
		return fmt.Errorf("save GNEX32 state: state exceeds byte limit")
	}
	return guest.WriteFull(output, data.Bytes())
}

func (m *gnex32Machine) LoadState(input io.Reader) error {
	if input == nil {
		return fmt.Errorf("load GNEX32 state: reader is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state == machinecore.StateRunning || m.state == machinecore.StateEmpty {
		return fmt.Errorf("load GNEX32 state from %s: %w", m.state, ErrInvalidState)
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(maxGNEX32MachineStateBytes)+1))
	if err != nil {
		return fmt.Errorf("read GNEX32 state: %w", err)
	}
	if uint64(len(data)) > maxGNEX32MachineStateBytes {
		return fmt.Errorf("read GNEX32 state: state exceeds byte limit")
	}
	parsed, err := m.parseGNEX32MachineState(data)
	if err != nil {
		return err
	}
	candidate, err := gnex32.NewRuntime(m.image, m.profile)
	if err != nil {
		return err
	}
	if err := candidate.UnmarshalBinary(parsed.runtime); err != nil {
		_ = candidate.Close()
		return fmt.Errorf("load GNEX32 runtime: %w", err)
	}
	if candidate.NetworkActive() {
		_ = candidate.Close()
		return fmt.Errorf("load GNEX32 state: %w", ErrGNEX32NetworkStateActive)
	}
	m.requestIDs.nextSession()
	// The old runtime has no external resources; replacing its isolated service
	// graph lets it be reclaimed without mutating live state before validation.
	m.runtime = candidate
	m.state = parsed.state
	m.audioGeneration = parsed.audioGeneration
	m.audioGenerationPending = parsed.audioGenerationPending
	m.audioEpochGuestNS = parsed.audioEpochGuestNS
	m.audioCursorSample = parsed.audioCursorSample
	m.audioCursorValid = parsed.audioCursorValid
	m.mediaOutputRevision = candidate.AudioOutputRevision()
	m.presentation = framePresentationCache{}
	m.presentationCount = 0
	// Host buffers may still contain samples from the pre-load timeline. Publish
	// a generation after the saved one while preserving the restored guest time.
	m.nextGNEX32AudioGenerationLocked(candidate.AudioTime())
	return nil
}

func (m *gnex32Machine) parseGNEX32MachineState(data []byte) (parsedGNEX32MachineState, error) {
	minimum := len(gnex32MachineStateMagic) + 4 + 4 + sha256.Size + 8*5 + sha256.Size
	if len(data) < minimum {
		return parsedGNEX32MachineState{}, fmt.Errorf("load GNEX32 state: truncated header")
	}
	payload := data[:len(data)-sha256.Size]
	expected := data[len(payload):]
	actual := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return parsedGNEX32MachineState{}, fmt.Errorf("load GNEX32 state: checksum mismatch")
	}
	decoder := gnex32MachineStateDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(gnex32MachineStateMagic)); string(magic) != gnex32MachineStateMagic {
		return parsedGNEX32MachineState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != gnex32MachineStateVersion {
		return parsedGNEX32MachineState{}, decoder.fail(fmt.Sprintf("unsupported version %d", version))
	}
	parsed := parsedGNEX32MachineState{
		state:                  machinecore.State(decoder.u8()),
		audioGenerationPending: decoder.boolean(),
		audioCursorValid:       decoder.boolean(),
	}
	decoder.reserved(1)
	sourceDigest := decoder.bytes(sha256.Size)
	parsed.audioGeneration = decoder.u64()
	parsed.audioEpochGuestNS = int64(decoder.u64())
	parsed.audioCursorSample = decoder.u64()
	parsed.mediaOutputRevision = decoder.u64()
	runtimeSize := decoder.u64()
	if decoder.err != nil {
		return parsedGNEX32MachineState{}, decoder.err
	}
	wantDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || subtle.ConstantTimeCompare(sourceDigest, wantDigest) != 1 {
		return parsedGNEX32MachineState{}, decoder.fail("source SHA-256 mismatch")
	}
	switch parsed.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return parsedGNEX32MachineState{}, decoder.fail("invalid machine lifecycle")
	}
	if parsed.audioGeneration == 0 || parsed.audioEpochGuestNS < 0 ||
		runtimeSize > gnex32.MaxRuntimeStateBytes || runtimeSize > uint64(decoder.reader.Len()) {
		return parsedGNEX32MachineState{}, decoder.fail("invalid audio or runtime state")
	}
	parsed.runtime = decoder.bytes(int(runtimeSize))
	if decoder.err != nil {
		return parsedGNEX32MachineState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return parsedGNEX32MachineState{}, decoder.fail(fmt.Sprintf("%d trailing bytes", decoder.reader.Len()))
	}
	return parsed, nil
}

func writeGNEX32StateU32(output *bytes.Buffer, value uint32) {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	output.Write(data[:])
}

func writeGNEX32StateU64(output *bytes.Buffer, value uint64) {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	output.Write(data[:])
}

func writeGNEX32StateBool(output *bytes.Buffer, value bool) {
	if value {
		output.WriteByte(1)
	} else {
		output.WriteByte(0)
	}
}

type gnex32MachineStateDecoder struct {
	reader *bytes.Reader
	offset uint64
	err    error
}

func (d *gnex32MachineStateDecoder) bytes(size int) []byte {
	if d.err != nil || size < 0 || size > d.reader.Len() {
		if d.err == nil {
			d.err = d.fail("truncated data")
		}
		return nil
	}
	result := make([]byte, size)
	if _, err := io.ReadFull(d.reader, result); err != nil {
		d.err = d.fail(err.Error())
		return nil
	}
	d.offset += uint64(size)
	return result
}

func (d *gnex32MachineStateDecoder) u8() uint8 {
	data := d.bytes(1)
	if len(data) == 1 {
		return data[0]
	}
	return 0
}

func (d *gnex32MachineStateDecoder) boolean() bool {
	value := d.u8()
	if value > 1 && d.err == nil {
		d.err = d.fail("invalid boolean")
	}
	return value != 0
}

func (d *gnex32MachineStateDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) == 4 {
		return binary.LittleEndian.Uint32(data)
	}
	return 0
}

func (d *gnex32MachineStateDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) == 8 {
		return binary.LittleEndian.Uint64(data)
	}
	return 0
}

func (d *gnex32MachineStateDecoder) reserved(size int) {
	for _, value := range d.bytes(size) {
		if value != 0 && d.err == nil {
			d.err = d.fail("nonzero reserved field")
		}
	}
}

func (d *gnex32MachineStateDecoder) fail(reason string) error {
	return fmt.Errorf("load GNEX32 state at offset 0x%x: %s", d.offset, reason)
}
