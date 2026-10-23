package application

import (
	"errors"
	"fmt"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/gnex32"
)

func (m *gnex32Machine) NetworkRequests() []machinecore.NetworkRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.runtime == nil {
		return nil
	}
	requests := m.runtime.NetworkRequests()
	result := make([]machinecore.NetworkRequest, len(requests))
	for index, request := range requests {
		result[index] = machinecore.NetworkRequest{
			Sequence: request.Sequence, Connection: request.Connection,
			Host: request.Host, Port: request.Port,
			MaxReceiveBytes: request.MaxReceiveBytes,
			Data:            append([]byte(nil), request.Data...),
		}
		switch request.Kind {
		case gnex32.NetworkRequestConnect:
			result[index].Kind = machinecore.NetworkRequestConnect
		case gnex32.NetworkRequestWrite:
			result[index].Kind = machinecore.NetworkRequestWrite
		case gnex32.NetworkRequestDisconnect:
			result[index].Kind = machinecore.NetworkRequestDisconnect
		}
	}
	return result
}

func (m *gnex32Machine) CompleteNetworkRequest(sequence uint64, success bool) error {
	return m.runGNEX32NetworkProvider("complete", func() error {
		return m.runtime.CompleteNetworkRequest(sequence, success, m.budget)
	})
}

func (m *gnex32Machine) ReceiveNetworkData(connection uint64, data []byte) error {
	return m.runGNEX32NetworkProvider("receive", func() error {
		return m.runtime.InjectNetworkReceiveFor(connection, data, m.budget)
	})
}

func (m *gnex32Machine) DisconnectNetwork(connection uint64) error {
	return m.runGNEX32NetworkProvider("disconnect", func() error {
		return m.runtime.DisconnectNetwork(connection, m.budget)
	})
}

func (m *gnex32Machine) runGNEX32NetworkProvider(kind string, action func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cpu.ErrClosed
	}
	if m.state != machinecore.StateRunning {
		return fmt.Errorf("%s GNEX32 network from %s: %w", kind, m.state, ErrInvalidState)
	}
	if err := action(); err != nil {
		if errors.Is(err, gnex32.ErrNetworkRequestUnavailable) ||
			errors.Is(err, gnex32.ErrNetworkReceiveUnavailable) ||
			errors.Is(err, gnex32.ErrNetworkReceiveTooLarge) {
			return err
		}
		m.state = machinecore.StateFaulted
		return fmt.Errorf("%s GNEX32 network at code offset %d: %w", kind, m.runtime.PC(), err)
	}
	_, err := m.finishGNEX32CloseRequestLocked()
	return err
}

var _ machinecore.NetworkTransportMachine = (*gnex32Machine)(nil)
