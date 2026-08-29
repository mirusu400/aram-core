package runtime

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	servicesStateMagic       = "ARAMSVC\x00"
	servicesContainerVersion = uint32(1)
	MaxServicesStateBytes    = uint64(768 << 20)
	maxComponentIDBytes      = 64
)

type stateComponent struct {
	id    string
	value any
}

type serviceComponentSpec struct {
	id     string
	schema uint32
}

// Component schemas are intentionally listed independently even when their
// current versions match. A future component can evolve without forcing an
// unrelated service-state schema change.
var requiredServiceComponents = []serviceComponentSpec{
	{"config", 2},
	{"registry", 2},
	{"clock", 2},
	{"random", 2},
	{"events", 2},
	{"input", 2},
	{"timers", 2},
	{"graphics", 2},
	{"assets", 2},
	{"storage", 2},
	// media 3 adds the mixing policy's hand-loop marker, which decides whether
	// a long non-repeating track becomes the persistent music voice.
	{"media", 3},
	{"device", 2},
	{"network", 2},
	{"replay", 2},
	{"coordinator", 2},
	{"text", 2},
}

// MarshalBinary emits a portable, checksummed component container. Trace is
// deliberately absent because it is observational rather than semantic state.
func (s *Services) MarshalBinary() ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: services are nil", ErrInvalidArgument)
	}
	state := s.Snapshot()
	if _, err := servicesFromState(state); err != nil {
		return nil, fmt.Errorf("validate service state before encoding: %w", err)
	}
	components := []stateComponent{
		{"config", state.Config},
		{"registry", state.Registry},
		{"clock", state.Clock},
		{"random", state.Random},
		{"events", state.Events},
		{"input", state.Input},
		{"timers", state.Timers},
		{"graphics", state.Graphics},
		{"assets", state.Assets},
		{"storage", state.Storage},
		{"media", state.Media},
		{"device", state.Device},
		{"network", state.Network},
		{"replay", state.Replay},
		{"coordinator", state.Coordinator},
		{"text", state.Text},
	}
	var output bytes.Buffer
	output.WriteString(servicesStateMagic)
	writeBinaryU32(&output, servicesContainerVersion)
	writeBinaryU32(&output, ServicesSchemaVersion)
	writeBinaryU32(&output, uint32(len(components)))
	for index, component := range components {
		spec := requiredServiceComponents[index]
		if component.id != spec.id {
			return nil, fmt.Errorf(
				"%w: internal service component order mismatch",
				ErrInvalidState,
			)
		}
		payload, err := encodeStateValue(component.value)
		if err != nil {
			return nil, fmt.Errorf("encode service component %q: %w", component.id, err)
		}
		payloadSize := uint64(len(payload))
		if len(component.id) > maxComponentIDBytes ||
			payloadSize > MaxServicesStateBytes-64 ||
			uint64(output.Len()) > MaxServicesStateBytes-payloadSize-64 {
			return nil, fmt.Errorf("%w: service state exceeds byte limit", ErrLimitExceeded)
		}
		writeBinaryU16(&output, uint16(len(component.id)))
		output.WriteString(component.id)
		writeBinaryU32(&output, spec.schema)
		writeBinaryU64(&output, uint64(len(payload)))
		output.Write(payload)
		digest := sha256.Sum256(payload)
		output.Write(digest[:])
	}
	digest := sha256.Sum256(output.Bytes())
	output.Write(digest[:])
	if uint64(output.Len()) > MaxServicesStateBytes {
		return nil, fmt.Errorf("%w: service state exceeds byte limit", ErrLimitExceeded)
	}
	return output.Bytes(), nil
}

// UnmarshalBinary decodes and validates every required component on an
// isolated candidate before mutating the receiver.
func (s *Services) UnmarshalBinary(data []byte) error {
	if s == nil {
		return fmt.Errorf("%w: services are nil", ErrInvalidArgument)
	}
	state, err := DecodeServicesState(data)
	if err != nil {
		return err
	}
	return s.Restore(state)
}

func DecodeServicesState(data []byte) (ServicesState, error) {
	if uint64(len(data)) > MaxServicesStateBytes {
		return ServicesState{}, fmt.Errorf("%w: service state exceeds byte limit", ErrLimitExceeded)
	}
	minimum := len(servicesStateMagic) + 4 + 4 + 4 + sha256.Size
	if len(data) < minimum {
		return ServicesState{}, fmt.Errorf("%w: truncated service state", ErrInvalidState)
	}
	payload := data[:len(data)-sha256.Size]
	expected := data[len(payload):]
	actual := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return ServicesState{}, fmt.Errorf("%w: service state checksum mismatch", ErrInvalidState)
	}
	decoder := binaryStateDecoder{reader: bytes.NewReader(payload)}
	if magic := decoder.bytes(len(servicesStateMagic)); string(magic) != servicesStateMagic {
		return ServicesState{}, decoder.fail("magic mismatch")
	}
	if version := decoder.u32(); version != servicesContainerVersion {
		return ServicesState{}, decoder.fail(
			fmt.Sprintf("unsupported container version %d", version),
		)
	}
	schema := decoder.u32()
	if schema != ServicesSchemaVersion {
		return ServicesState{}, decoder.fail(
			fmt.Sprintf("unsupported services schema %d", schema),
		)
	}
	count := decoder.u32()
	if count != uint32(len(requiredServiceComponents)) {
		return ServicesState{}, decoder.fail(
			fmt.Sprintf("component count %d", count),
		)
	}
	state := ServicesState{Schema: schema}
	for index, expected := range requiredServiceComponents {
		idLength := decoder.u16()
		if idLength == 0 || idLength > maxComponentIDBytes {
			return ServicesState{}, decoder.fail(
				fmt.Sprintf("invalid component ID length at %d", index),
			)
		}
		id := string(decoder.bytes(int(idLength)))
		version := decoder.u32()
		size := decoder.u64()
		if decoder.err != nil {
			return ServicesState{}, decoder.err
		}
		if id != expected.id {
			return ServicesState{}, decoder.fail(
				fmt.Sprintf("component %d is %q, want %q", index, id, expected.id),
			)
		}
		if version != expected.schema {
			return ServicesState{}, decoder.fail(
				fmt.Sprintf("component %q schema %d", id, version),
			)
		}
		if size > MaxServicesStateBytes || size > uint64(decoder.reader.Len()) ||
			size > uint64(maxIntValue()) {
			return ServicesState{}, decoder.fail(
				fmt.Sprintf("component %q has invalid size %d", id, size),
			)
		}
		componentPayload := decoder.bytes(int(size))
		componentDigest := decoder.bytes(sha256.Size)
		if decoder.err != nil {
			return ServicesState{}, decoder.err
		}
		digest := sha256.Sum256(componentPayload)
		if subtle.ConstantTimeCompare(componentDigest, digest[:]) != 1 {
			return ServicesState{}, decoder.fail(
				fmt.Sprintf("component %q checksum mismatch", id),
			)
		}
		var target any
		switch id {
		case "config":
			target = &state.Config
		case "registry":
			target = &state.Registry
		case "clock":
			target = &state.Clock
		case "random":
			target = &state.Random
		case "events":
			target = &state.Events
		case "input":
			target = &state.Input
		case "timers":
			target = &state.Timers
		case "graphics":
			target = &state.Graphics
		case "assets":
			target = &state.Assets
		case "storage":
			target = &state.Storage
		case "media":
			target = &state.Media
		case "device":
			target = &state.Device
		case "network":
			target = &state.Network
		case "replay":
			target = &state.Replay
		case "coordinator":
			target = &state.Coordinator
		case "text":
			target = &state.Text
		default:
			return ServicesState{}, decoder.fail("unknown required component")
		}
		if err := decodeStateValue(componentPayload, target); err != nil {
			return ServicesState{}, decoder.fail(
				fmt.Sprintf("decode component %q: %v", id, err),
			)
		}
	}
	if decoder.err != nil {
		return ServicesState{}, decoder.err
	}
	if decoder.reader.Len() != 0 {
		return ServicesState{}, decoder.fail(
			fmt.Sprintf("%d trailing payload bytes", decoder.reader.Len()),
		)
	}
	// Constructing a candidate performs limits, cross-reference, ownership,
	// and object-graph validation without touching a caller's live services.
	if _, err := servicesFromState(state); err != nil {
		return ServicesState{}, err
	}
	return state, nil
}

type binaryStateDecoder struct {
	reader *bytes.Reader
	offset uint64
	err    error
}

func (d *binaryStateDecoder) bytes(size int) []byte {
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

func (d *binaryStateDecoder) u16() uint16 {
	data := d.bytes(2)
	if len(data) != 2 {
		return 0
	}
	return binary.LittleEndian.Uint16(data)
}

func (d *binaryStateDecoder) u32() uint32 {
	data := d.bytes(4)
	if len(data) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(data)
}

func (d *binaryStateDecoder) u64() uint64 {
	data := d.bytes(8)
	if len(data) != 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(data)
}

func (d *binaryStateDecoder) fail(reason string) error {
	return fmt.Errorf(
		"%w: service state at offset 0x%x: %s",
		ErrInvalidState,
		d.offset,
		reason,
	)
}

func writeBinaryU16(output *bytes.Buffer, value uint16) {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	output.Write(encoded[:])
}

func writeBinaryU32(output *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	output.Write(encoded[:])
}

func writeBinaryU64(output *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	output.Write(encoded[:])
}

func maxIntValue() int {
	return int(^uint(0) >> 1)
}
