package runtime

import (
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

const (
	replaySocketConnect = "socket.connect"
	replaySocketRead    = "socket.read"
	replayHTTPComplete  = "http.complete"
	replaySerialRead    = "serial.read"
	replayDeviceStatus  = "device.status"
)

// CompleteSocketResponse applies a modeled provider result and records it in
// replay mode. During playback, the recorded success value is authoritative.
func (s *Services) CompleteSocketResponse(
	owner OwnerID,
	id ServiceID,
	success bool,
	at time.Duration,
) error {
	return s.applyNetworkResponse(
		ReplayEntry{
			AtNS: int64(at), Kind: ReplayNetworkResponse,
			Owner: owner, ServiceID: id, Name: replaySocketConnect,
			Value: boolValue(success),
		},
		func(entry ReplayEntry) error {
			if entry.Value != 0 && entry.Value != 1 || len(entry.Data) != 0 {
				return fmt.Errorf("%w: invalid socket-connect replay response", ErrInvalidState)
			}
			return s.Network.CompleteSocketConnect(
				owner,
				id,
				entry.Value != 0,
				entry.AtNS,
				s.Events,
			)
		},
	)
}

// InjectSocketResponse delivers provider bytes through the modeled socket and
// captures the exact payload for deterministic replay.
func (s *Services) InjectSocketResponse(
	owner OwnerID,
	id ServiceID,
	data []byte,
	at time.Duration,
) error {
	return s.applyNetworkResponse(
		ReplayEntry{
			AtNS: int64(at), Kind: ReplayNetworkResponse,
			Owner: owner, ServiceID: id, Name: replaySocketRead,
			Value: int64(len(data)), Data: cloneBytes(data),
		},
		func(entry ReplayEntry) error {
			if entry.Value != int64(len(entry.Data)) {
				return fmt.Errorf("%w: invalid socket-read replay response", ErrInvalidState)
			}
			return s.Network.InjectSocketRead(
				owner,
				id,
				entry.Data,
				entry.AtNS,
				s.Events,
			)
		},
	)
}

// CompleteHTTPResponse applies a complete modeled HTTP response. Headers are
// normalized and serialized in sorted order before entering the replay log.
func (s *Services) CompleteHTTPResponse(
	owner OwnerID,
	id ServiceID,
	code int32,
	headers []HTTPProperty,
	body []byte,
	at time.Duration,
) error {
	if s == nil || s.Network == nil {
		return fmt.Errorf("%w: network response services are missing", ErrInvalidArgument)
	}
	normalized, err := validateHTTPProperties(headers, s.Network.limits)
	if err != nil {
		return err
	}
	entry := ReplayEntry{
		AtNS: int64(at), Kind: ReplayNetworkResponse,
		Owner: owner, ServiceID: id, Name: replayHTTPComplete,
		Value: int64(code),
		Data:  encodeHTTPReplayResponse(sortedProperties(normalized), body),
	}
	return s.applyNetworkResponse(entry, func(current ReplayEntry) error {
		if current.Value < 100 || current.Value > 999 {
			return fmt.Errorf("%w: invalid HTTP replay status", ErrInvalidState)
		}
		replayedHeaders, replayedBody, decodeErr :=
			decodeHTTPReplayResponse(current.Data, s.Network.limits)
		if decodeErr != nil {
			return decodeErr
		}
		return s.Network.CompleteHTTP(
			owner,
			id,
			int32(current.Value),
			replayedHeaders,
			replayedBody,
			current.AtNS,
			s.Events,
		)
	})
}

// InjectSerialResponse applies modeled serial input and captures its exact
// payload for deterministic replay.
func (s *Services) InjectSerialResponse(
	owner OwnerID,
	id ServiceID,
	data []byte,
	at time.Duration,
) error {
	return s.applyNetworkResponse(
		ReplayEntry{
			AtNS: int64(at), Kind: ReplayNetworkResponse,
			Owner: owner, ServiceID: id, Name: replaySerialRead,
			Value: int64(len(data)), Data: cloneBytes(data),
		},
		func(entry ReplayEntry) error {
			if entry.Value != int64(len(entry.Data)) {
				return fmt.Errorf("%w: invalid serial-read replay response", ErrInvalidState)
			}
			return s.Network.InjectSerialRead(
				owner,
				id,
				entry.Data,
				entry.AtNS,
				s.Events,
			)
		},
	)
}

// UpdateDeviceStatus applies a provider-supplied battery, signal, and network
// sample. Playback uses the captured sample and does not consult host state.
func (s *Services) UpdateDeviceStatus(
	owner OwnerID,
	battery, signal uint8,
	networkAvailable bool,
	at time.Duration,
) error {
	if s == nil || s.Device == nil || s.Replay == nil {
		return fmt.Errorf("%w: device response services are missing", ErrInvalidArgument)
	}
	provided := ReplayEntry{
		AtNS: int64(at), Kind: ReplayDeviceResponse,
		Owner: owner, Name: replayDeviceStatus,
		Value: int64(battery) |
			int64(signal)<<8 |
			boolValue(networkAvailable)<<16,
	}
	deviceBefore := s.Device.Snapshot()
	replayBefore := s.Replay.Snapshot()
	rollback := func(err error) error {
		_ = s.Device.Restore(deviceBefore)
		_ = s.Replay.Restore(replayBefore)
		return err
	}
	current := provided
	if s.Replay.Mode() == ReplayPlayback {
		recorded, ok := s.Replay.Peek()
		if !ok || recorded.Kind != ReplayDeviceResponse ||
			recorded.AtNS != provided.AtNS ||
			recorded.Owner != owner ||
			recorded.Name != replayDeviceStatus {
			return fmt.Errorf("%w: device replay response mismatch", ErrInvalidState)
		}
		if err := s.Replay.Consume(recorded); err != nil {
			return err
		}
		current = recorded
	}
	if current.Value < 0 || current.Value>>17 != 0 ||
		current.Aux != 0 || len(current.Data) != 0 {
		return rollback(fmt.Errorf("%w: invalid device status replay response", ErrInvalidState))
	}
	currentBattery := uint8(current.Value)
	currentSignal := uint8(current.Value >> 8)
	currentNetwork := current.Value>>16&1 != 0
	if err := s.Device.SetStatus(
		currentBattery,
		currentSignal,
		currentNetwork,
	); err != nil {
		return rollback(err)
	}
	if s.Replay.Mode() == ReplayRecord {
		if _, err := s.Replay.Record(provided); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func (s *Services) applyNetworkResponse(
	provided ReplayEntry,
	apply func(ReplayEntry) error,
) error {
	if s == nil || s.Network == nil || s.Events == nil || s.Replay == nil ||
		apply == nil {
		return fmt.Errorf("%w: network response services are missing", ErrInvalidArgument)
	}
	networkBefore := s.Network.Snapshot()
	eventsBefore := s.Events.Snapshot()
	replayBefore := s.Replay.Snapshot()
	rollback := func(err error) error {
		_ = s.Network.Restore(networkBefore)
		_ = s.Events.Restore(eventsBefore)
		_ = s.Replay.Restore(replayBefore)
		return err
	}

	current := provided
	switch s.Replay.Mode() {
	case ReplayPlayback:
		recorded, ok := s.Replay.Peek()
		if !ok ||
			recorded.Kind != ReplayNetworkResponse ||
			recorded.AtNS != provided.AtNS ||
			recorded.Owner != provided.Owner ||
			recorded.ServiceID != provided.ServiceID ||
			recorded.Name != provided.Name {
			return fmt.Errorf("%w: external replay response mismatch", ErrInvalidState)
		}
		if err := s.Replay.Consume(recorded); err != nil {
			return err
		}
		current = recorded
	case ReplayRecord, ReplayOff:
	default:
		return fmt.Errorf("%w: invalid replay mode", ErrInvalidState)
	}
	if err := apply(current); err != nil {
		return rollback(err)
	}
	if s.Replay.Mode() == ReplayRecord {
		if _, err := s.Replay.Record(provided); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func encodeHTTPReplayResponse(headers []HTTPProperty, body []byte) []byte {
	size := 4 + len(body)
	for _, header := range headers {
		size += 8 + len(header.Name) + len(header.Value)
	}
	result := make([]byte, 0, size)
	result = binary.LittleEndian.AppendUint32(result, uint32(len(headers)))
	for _, header := range headers {
		result = binary.LittleEndian.AppendUint32(result, uint32(len(header.Name)))
		result = append(result, header.Name...)
		result = binary.LittleEndian.AppendUint32(result, uint32(len(header.Value)))
		result = append(result, header.Value...)
	}
	return append(result, body...)
}

func decodeHTTPReplayResponse(
	data []byte,
	limits NetworkLimits,
) ([]HTTPProperty, []byte, error) {
	offset := 0
	readU32 := func() (uint32, bool) {
		if len(data)-offset < 4 {
			return 0, false
		}
		value := binary.LittleEndian.Uint32(data[offset:])
		offset += 4
		return value, true
	}
	count, ok := readU32()
	if !ok || count > limits.MaxProperties {
		return nil, nil, fmt.Errorf("%w: invalid HTTP replay headers", ErrInvalidState)
	}
	headers := make([]HTTPProperty, 0, count)
	for index := uint32(0); index < count; index++ {
		nameSize, valid := readU32()
		if !valid || nameSize > limits.MaxNameBytes ||
			uint64(nameSize) > uint64(len(data)-offset) {
			return nil, nil, fmt.Errorf("%w: invalid HTTP replay header name", ErrInvalidState)
		}
		name := string(data[offset : offset+int(nameSize)])
		offset += int(nameSize)
		valueSize, valid := readU32()
		if !valid || valueSize > limits.MaxNameBytes ||
			uint64(valueSize) > uint64(len(data)-offset) {
			return nil, nil, fmt.Errorf("%w: invalid HTTP replay header value", ErrInvalidState)
		}
		value := string(data[offset : offset+int(valueSize)])
		offset += int(valueSize)
		headers = append(headers, HTTPProperty{Name: name, Value: value})
	}
	if _, err := validateCanonicalHTTPProperties(headers, limits); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid HTTP replay properties", ErrInvalidState)
	}
	if !sort.SliceIsSorted(headers, func(i, j int) bool {
		return headers[i].Name < headers[j].Name
	}) || uint64(len(data)-offset) > limits.MaxBufferBytes {
		return nil, nil, fmt.Errorf("%w: invalid HTTP replay response", ErrInvalidState)
	}
	return headers, cloneBytes(data[offset:]), nil
}

func validateServiceReplayState(
	state ReplayState,
	networkLimits NetworkLimits,
) error {
	for index, entry := range state.Entries {
		var err error
		switch entry.Kind {
		case ReplayClockAdvance, ReplayInput:
			if entry.Aux != 0 {
				err = fmt.Errorf("nonzero auxiliary value")
			}
		case ReplayNetworkResponse:
			switch entry.Name {
			case replaySocketConnect:
				if (entry.Value != 0 && entry.Value != 1) ||
					entry.Aux != 0 || len(entry.Data) != 0 {
					err = fmt.Errorf("invalid socket-connect response")
				}
			case replaySocketRead, replaySerialRead:
				if entry.Value != int64(len(entry.Data)) ||
					entry.Aux != 0 ||
					uint64(len(entry.Data)) > networkLimits.MaxBufferBytes {
					err = fmt.Errorf("invalid buffered response")
				}
			case replayHTTPComplete:
				if entry.Value < 100 || entry.Value > 999 ||
					entry.Aux != 0 {
					err = fmt.Errorf("invalid HTTP response status")
				} else {
					_, _, err = decodeHTTPReplayResponse(
						entry.Data,
						networkLimits,
					)
				}
			default:
				err = fmt.Errorf("unknown network response %q", entry.Name)
			}
		case ReplayDeviceResponse:
			battery := uint8(entry.Value)
			signal := uint8(entry.Value >> 8)
			if entry.Name != replayDeviceStatus ||
				entry.ServiceID != 0 ||
				entry.Value < 0 || entry.Value>>17 != 0 ||
				battery > 100 || signal > 100 ||
				entry.Aux != 0 || len(entry.Data) != 0 {
				err = fmt.Errorf("invalid device status response")
			}
		}
		if err != nil {
			return fmt.Errorf(
				"%w: invalid replay entry %d: %v",
				ErrInvalidState,
				index,
				err,
			)
		}
	}
	return nil
}
