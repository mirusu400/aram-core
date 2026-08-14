package skvmhost

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func (m *Machine) SaveState(output io.Writer) error {
	if output == nil {
		return fmt.Errorf("save SKVM state: writer is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	switch m.state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return fmt.Errorf("save from %s: %w", m.state, guest.ErrInvalidState)
	}
	if err := validateSKVMMachineCoordinator(
		m.services,
		m.owner,
		m.state,
	); err != nil {
		return fmt.Errorf("save SKVM state: %w", err)
	}
	vmState, err := m.vm.MarshalBinary()
	if err != nil {
		return err
	}
	sourceDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || len(sourceDigest) != sha256.Size {
		return fmt.Errorf("save SKVM state: source SHA-256 is unavailable")
	}
	var payload bytes.Buffer
	payload.WriteString(skvmMachineStateMagic)
	writeSKVMU32(&payload, skvmMachineStateVersion)
	payload.WriteByte(byte(m.state))
	if m.started {
		payload.WriteByte(1)
	} else {
		payload.WriteByte(0)
	}
	payload.Write([]byte{0, 0})
	payload.Write(sourceDigest)
	writeSKVMString(&payload, m.mainClass)
	writeSKVMU32(&payload, m.midlet)
	writeSKVMU32(&payload, uint32(len(m.input)))
	for _, event := range m.input {
		writeSKVMString(&payload, event.Control)
		if event.Pressed {
			payload.WriteByte(1)
		} else {
			payload.WriteByte(0)
		}
		payload.Write(make([]byte, 7))
		writeSKVMU64(&payload, uint64(event.At))
	}
	writeSKVMU64(&payload, uint64(len(vmState)))
	payload.Write(vmState)
	digest := sha256.Sum256(payload.Bytes())
	payload.Write(digest[:])
	if uint64(payload.Len()) > maxSKVMMachineStateBytes {
		return fmt.Errorf("save SKVM state: state exceeds byte limit")
	}
	return guest.WriteFull(output, payload.Bytes())
}

func (m *Machine) LoadState(input io.Reader) error {
	if input == nil {
		return fmt.Errorf("load SKVM state: reader is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("SKVM machine is closed")
	}
	if m.state == machinecore.StateRunning || m.state == machinecore.StateEmpty {
		return fmt.Errorf("load from %s: %w", m.state, guest.ErrInvalidState)
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(maxSKVMMachineStateBytes)+1))
	if err != nil {
		return fmt.Errorf("read SKVM state: %w", err)
	}
	if uint64(len(data)) > maxSKVMMachineStateBytes {
		return fmt.Errorf("read SKVM state: state exceeds byte limit")
	}
	parsed, err := m.parseMachineState(data)
	if err != nil {
		return err
	}
	candidateServices, err := shared.NewServices(m.services.Config)
	if err != nil {
		return err
	}
	candidate, err := skengine.NewWithServices(m.classData, candidateServices, m.owner)
	if err != nil {
		return err
	}
	if err := candidate.UnmarshalBinary(parsed.vm); err != nil {
		return err
	}
	if err := validateSKVMMachineCoordinator(
		candidateServices,
		m.owner,
		parsed.state,
	); err != nil {
		return fmt.Errorf("load SKVM state: %w", err)
	}
	if parsed.midlet != 0 {
		if _, ok := candidate.Object(parsed.midlet); !ok {
			return fmt.Errorf("load SKVM state: MIDlet reference is missing")
		}
	}
	if err := m.vm.UnmarshalBinary(parsed.vm); err != nil {
		return err
	}
	m.services = m.vm.Services()
	m.state = parsed.state
	m.started = parsed.started
	m.midlet = parsed.midlet
	m.input = parsed.input
	m.frameQuantum = m.services.Config.FrameDuration
	return nil
}

type parsedSKVMMachineState struct {
	state   machinecore.State
	started bool
	midlet  uint32
	input   []machinecore.InputEvent
	vm      []byte
}

func (m *Machine) parseMachineState(data []byte) (parsedSKVMMachineState, error) {
	if len(data) < len(skvmMachineStateMagic)+4+4+sha256.Size+sha256.Size {
		return parsedSKVMMachineState{}, fmt.Errorf("load SKVM state: truncated header")
	}
	payload := data[:len(data)-sha256.Size]
	expected := data[len(payload):]
	actual := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return parsedSKVMMachineState{}, fmt.Errorf("load SKVM state: checksum mismatch")
	}
	decoder := skvmMachineDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(skvmMachineStateMagic)); string(magic) != skvmMachineStateMagic {
		return parsedSKVMMachineState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != skvmMachineStateVersion {
		return parsedSKVMMachineState{}, decoder.fail(
			fmt.Sprintf("unsupported version %d", version),
		)
	}
	state := machinecore.State(decoder.u8())
	started := decoder.u8() != 0
	decoder.reserved(2)
	sourceDigest := decoder.bytes(sha256.Size)
	mainClass := decoder.string()
	midlet := decoder.u32()
	inputCount := decoder.u32()
	if decoder.err != nil {
		return parsedSKVMMachineState{}, decoder.err
	}
	expectedDigest, err := hex.DecodeString(m.source.SHA256)
	if err != nil || subtle.ConstantTimeCompare(sourceDigest, expectedDigest) != 1 {
		return parsedSKVMMachineState{}, decoder.fail("source SHA-256 mismatch")
	}
	if mainClass != m.mainClass {
		return parsedSKVMMachineState{}, decoder.fail("main class mismatch")
	}
	switch state {
	case machinecore.StateReady, machinecore.StatePaused, machinecore.StateStopped:
	default:
		return parsedSKVMMachineState{}, decoder.fail("invalid machine state")
	}
	if (!started && midlet != 0) || (started && midlet == 0) ||
		(state == machinecore.StateReady && started) ||
		inputCount > maxSKVMPendingInputs {
		return parsedSKVMMachineState{}, decoder.fail("invalid lifecycle or input state")
	}
	inputs := make([]machinecore.InputEvent, 0, inputCount)
	var previousAt time.Duration
	for index := uint32(0); index < inputCount; index++ {
		control := decoder.string()
		pressed := decoder.u8()
		event := machinecore.InputEvent{
			Control: control,
			Pressed: pressed != 0,
		}
		decoder.reserved(7)
		event.At = time.Duration(int64(decoder.u64()))
		if decoder.err != nil {
			return parsedSKVMMachineState{}, decoder.err
		}
		if pressed > 1 ||
			event.Validate() != nil ||
			(index != 0 && event.At < previousAt) {
			return parsedSKVMMachineState{}, decoder.fail(
				fmt.Sprintf("invalid input event %d", index),
			)
		}
		inputs = append(inputs, event)
		previousAt = event.At
	}
	vmSize := decoder.u64()
	if vmSize > uint64(decoder.reader.Len()) || vmSize > uint64(skengine.MaxHostInt()) {
		return parsedSKVMMachineState{}, decoder.fail("invalid VM state size")
	}
	vmState := append([]byte(nil), decoder.bytes(int(vmSize))...)
	if decoder.err != nil {
		return parsedSKVMMachineState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return parsedSKVMMachineState{}, decoder.fail(
			fmt.Sprintf("%d trailing bytes", decoder.reader.Len()),
		)
	}
	return parsedSKVMMachineState{
		state: state, started: started, midlet: midlet,
		input: inputs, vm: vmState,
	}, nil
}

func validateSKVMMachineCoordinator(
	services *shared.Services,
	owner shared.OwnerID,
	state machinecore.State,
) error {
	if services == nil || services.Coordinator == nil {
		return fmt.Errorf("shared coordinator is missing")
	}
	expected := shared.LifecycleState(0)
	switch state {
	case machinecore.StateReady:
		expected = shared.LifecycleReady
	case machinecore.StatePaused:
		expected = shared.LifecyclePaused
	case machinecore.StateStopped:
		expected = shared.LifecycleStopped
	default:
		return fmt.Errorf("machine lifecycle %s is not serializable", state)
	}
	snapshot := services.Coordinator.Snapshot()
	if len(snapshot.Adapters) != 1 ||
		snapshot.Adapters[0].Owner != owner ||
		snapshot.Adapters[0].Name != "skvm" ||
		snapshot.Adapters[0].Lifecycle != expected ||
		snapshot.ForegroundOwner != owner ||
		snapshot.PresentationOwner != owner {
		return fmt.Errorf("shared coordinator lifecycle does not match machine state")
	}
	return nil
}

func writeSKVMString(output *bytes.Buffer, value string) {
	writeSKVMU32(output, uint32(len(value)))
	output.WriteString(value)
}

func writeSKVMU32(output *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	output.Write(encoded[:])
}

func writeSKVMU64(output *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	output.Write(encoded[:])
}

type skvmMachineDecoder struct {
	reader *bytes.Reader
	offset uint64
	err    error
}

func (d *skvmMachineDecoder) bytes(size int) []byte {
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

func (d *skvmMachineDecoder) reserved(size int) {
	for _, value := range d.bytes(size) {
		if value != 0 && d.err == nil {
			d.err = d.fail("nonzero reserved field")
		}
	}
}

func (d *skvmMachineDecoder) u8() uint8 {
	data := d.bytes(1)
	if len(data) != 1 {
		return 0
	}
	return data[0]
}

func (d *skvmMachineDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data)
}

func (d *skvmMachineDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}

func (d *skvmMachineDecoder) string() string {
	size := d.u32()
	if size > uint32(d.reader.Len()) {
		d.err = d.fail("truncated string")
		return ""
	}
	return string(d.bytes(int(size)))
}

func (d *skvmMachineDecoder) fail(reason string) error {
	return fmt.Errorf("load SKVM state at offset 0x%x: %s", d.offset, reason)
}
