package gnex32

import (
	"bytes"
	"errors"
	"fmt"

	shared "github.com/mirusu400/aram-core/runtime"
)

const maxGNEX32NetworkRequests = 1024

var ErrNetworkRequestUnavailable = errors.New("gnex32: network request unavailable")

type NetworkRequestKind uint8

const (
	NetworkRequestConnect NetworkRequestKind = iota + 1
	NetworkRequestWrite
	NetworkRequestDisconnect
)

func (kind NetworkRequestKind) Valid() bool {
	return kind >= NetworkRequestConnect && kind <= NetworkRequestDisconnect
}

type NetworkRequest struct {
	Sequence        uint64
	Connection      uint64
	Kind            NetworkRequestKind
	Host            string
	Port            uint16
	MaxReceiveBytes uint32
	Data            []byte
}

func (r *Runtime) NetworkRequests() []NetworkRequest {
	requests := make([]NetworkRequest, len(r.networkRequests))
	copy(requests, r.networkRequests)
	for index := range requests {
		requests[index].Data = bytes.Clone(requests[index].Data)
	}
	return requests
}

func (r *Runtime) NetworkActive() bool {
	return r != nil && (r.networkSocket != 0 || len(r.networkRequests) != 0)
}

func (r *Runtime) enqueueNetworkRequest(request NetworkRequest) error {
	if len(r.networkRequests) >= maxGNEX32NetworkRequests {
		return fmt.Errorf("gnex32: network request queue reached %d", maxGNEX32NetworkRequests)
	}
	if r.nextNetworkRequest == 0 {
		return fmt.Errorf("gnex32: network request sequence exhausted")
	}
	request.Sequence = r.nextNetworkRequest
	r.nextNetworkRequest++
	request.Data = bytes.Clone(request.Data)
	r.networkRequests = append(r.networkRequests, request)
	return nil
}

func (r *Runtime) allocateNetworkConnection() (uint64, error) {
	if r.nextNetworkConnection == 0 {
		return 0, fmt.Errorf("gnex32: network connection sequence exhausted")
	}
	connection := r.nextNetworkConnection
	r.nextNetworkConnection++
	return connection, nil
}

func (r *Runtime) removeNetworkRequest(sequence uint64) {
	for index := range r.networkRequests {
		if r.networkRequests[index].Sequence != sequence {
			continue
		}
		copy(r.networkRequests[index:], r.networkRequests[index+1:])
		r.networkRequests[len(r.networkRequests)-1] = NetworkRequest{}
		r.networkRequests = r.networkRequests[:len(r.networkRequests)-1]
		return
	}
}

func (r *Runtime) removeNetworkRequests(connection uint64) {
	kept := r.networkRequests[:0]
	for _, request := range r.networkRequests {
		if request.Connection == connection {
			continue
		}
		kept = append(kept, request)
	}
	clear(r.networkRequests[len(kept):])
	r.networkRequests = kept
}

func (r *Runtime) discardNetworkConnection(connection uint64) error {
	if connection != 0 {
		r.removeNetworkRequests(connection)
	}
	if connection != 0 && connection != r.networkConnection {
		return nil
	}
	if r.networkSocket != 0 {
		socket := r.networkSocket
		if err := r.services.Network.CloseSocket(r.owner, socket, r.services.Events); err != nil {
			return err
		}
	}
	r.networkSocket = 0
	r.networkConnection = 0
	return nil
}

func (r *Runtime) notifyNetworkDisconnect(connection uint64, budget uint64) error {
	if connection == 0 || connection != r.networkConnection {
		return fmt.Errorf("%w: connection %d", ErrNetworkRequestUnavailable, connection)
	}
	if _, err := r.services.Events.Enqueue(shared.Event{
		At: r.services.Clock.Monotonic(), Kind: shared.EventNetworkReady,
		Owner: r.owner, Name: "gnex.disconnect", Value: 10,
	}); err != nil {
		return err
	}
	if err := r.discardNetworkConnection(connection); err != nil {
		return err
	}
	return r.dispatchReadyEvents(budget)
}

func (r *Runtime) CompleteNetworkRequest(sequence uint64, success bool, budget uint64) error {
	if r == nil || r.closed {
		return fmt.Errorf("gnex32: cannot complete network request on a closed runtime")
	}
	if len(r.networkRequests) == 0 || r.networkRequests[0].Sequence != sequence {
		return fmt.Errorf("%w: sequence %d", ErrNetworkRequestUnavailable, sequence)
	}
	request := r.networkRequests[0]
	switch request.Kind {
	case NetworkRequestConnect:
		if request.Connection != r.networkConnection || r.networkSocket == 0 {
			return fmt.Errorf("%w: stale connect sequence %d", ErrNetworkRequestUnavailable, sequence)
		}
		if err := r.services.CompleteSocketResponse(
			r.owner, r.networkSocket, success, r.services.Clock.Monotonic(),
		); err != nil {
			return err
		}
		r.removeNetworkRequest(sequence)
		return r.dispatchReadyEvents(budget)
	case NetworkRequestWrite:
		if request.Connection != r.networkConnection || r.networkSocket == 0 {
			return fmt.Errorf("%w: stale write sequence %d", ErrNetworkRequestUnavailable, sequence)
		}
		if success {
			if err := r.services.Network.ConsumeSocketWritten(r.owner, r.networkSocket, request.Data); err != nil {
				return err
			}
			r.removeNetworkRequest(sequence)
			return nil
		}
		r.removeNetworkRequest(sequence)
		return r.notifyNetworkDisconnect(request.Connection, budget)
	case NetworkRequestDisconnect:
		r.removeNetworkRequest(sequence)
		return nil
	default:
		return fmt.Errorf("gnex32: invalid network request kind %d", request.Kind)
	}
}

func (r *Runtime) DisconnectNetwork(connection uint64, budget uint64) error {
	if r == nil || r.closed {
		return fmt.Errorf("gnex32: cannot disconnect network on a closed runtime")
	}
	return r.notifyNetworkDisconnect(connection, budget)
}

func (r *Runtime) validateNetworkState() error {
	if r.nextNetworkConnection == 0 || r.nextNetworkRequest == 0 {
		return fmt.Errorf("gnex32: saved network sequence is invalid")
	}
	if r.networkSocket == 0 && r.networkConnection != 0 {
		return fmt.Errorf("gnex32: saved network connection has no socket")
	}
	var socketState shared.ConnectionState
	var socketHost string
	var socketPort uint16
	local := false
	if r.networkSocket != 0 {
		info, err := r.services.Network.SocketInfo(r.owner, r.networkSocket)
		if err != nil {
			return fmt.Errorf("gnex32: saved network socket is invalid: %w", err)
		}
		socketState, socketHost, socketPort = info.State, info.Host, info.Port
		local = r.services.Network.HasLocalSocketServer(r.owner, r.networkSocket)
		if local == (r.networkConnection != 0) {
			return fmt.Errorf("gnex32: saved network provider ownership is inconsistent")
		}
		if socketState != shared.ConnectionConnecting && socketState != shared.ConnectionConnected {
			return fmt.Errorf("gnex32: saved network socket state %d is invalid", socketState)
		}
	}

	var previous uint64
	var connectCount int
	var pendingWrites []byte
	for _, request := range r.networkRequests {
		if request.Sequence == 0 || request.Sequence <= previous || request.Sequence >= r.nextNetworkRequest ||
			request.Connection == 0 || request.Connection >= r.nextNetworkConnection || !request.Kind.Valid() {
			return fmt.Errorf("gnex32: saved network request ordering is invalid")
		}
		previous = request.Sequence
		switch request.Kind {
		case NetworkRequestConnect:
			if request.Connection != r.networkConnection || socketState != shared.ConnectionConnecting ||
				request.Host != socketHost || request.Port != socketPort ||
				request.MaxReceiveBytes != maxGNEX32ReceiveBytes || len(request.Data) != 0 {
				return fmt.Errorf("gnex32: saved network connect request is invalid")
			}
			connectCount++
		case NetworkRequestWrite:
			if request.Connection != r.networkConnection || socketState != shared.ConnectionConnected ||
				request.Host != "" || request.Port != 0 || request.MaxReceiveBytes != 0 ||
				len(request.Data) == 0 || len(request.Data) > maxGNEX32ReceiveBytes {
				return fmt.Errorf("gnex32: saved network write request is invalid")
			}
			pendingWrites = append(pendingWrites, request.Data...)
		case NetworkRequestDisconnect:
			if request.Connection == r.networkConnection || request.Host != "" || request.Port != 0 ||
				request.MaxReceiveBytes != 0 || len(request.Data) != 0 {
				return fmt.Errorf("gnex32: saved network disconnect request is invalid")
			}
		}
	}
	if socketState == shared.ConnectionConnecting && connectCount != 1 ||
		socketState == shared.ConnectionConnected && connectCount != 0 {
		return fmt.Errorf("gnex32: saved network connect lifecycle is invalid")
	}
	if r.networkSocket != 0 && !local {
		written, err := r.services.Network.SocketWritten(r.owner, r.networkSocket)
		if err != nil || !bytes.Equal(written, pendingWrites) {
			return fmt.Errorf("gnex32: saved network writes do not match request queue")
		}
	}
	return nil
}
