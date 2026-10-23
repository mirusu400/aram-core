package core

import (
	"fmt"
	"strings"
)

const (
	MaxNetworkHostBytes    = 4096
	MaxNetworkPayloadBytes = 8 << 20
)

type NetworkRequestKind string

const (
	NetworkRequestConnect    NetworkRequestKind = "connect"
	NetworkRequestWrite      NetworkRequestKind = "write"
	NetworkRequestDisconnect NetworkRequestKind = "disconnect"
)

// NetworkRequest is an ordered request for host-owned network I/O. Sequence
// identifies the acknowledgement while Connection identifies the lifetime of
// one guest connection, including a connect request that has not completed yet.
type NetworkRequest struct {
	Sequence        uint64
	Connection      uint64
	Kind            NetworkRequestKind
	Host            string
	Port            uint16
	MaxReceiveBytes uint32
	Data            []byte
}

func (r NetworkRequest) Validate() error {
	if r.Sequence == 0 {
		return fmt.Errorf("network request sequence is zero")
	}
	if r.Connection == 0 {
		return fmt.Errorf("network request connection is zero")
	}
	switch r.Kind {
	case NetworkRequestConnect:
		if strings.TrimSpace(r.Host) == "" {
			return fmt.Errorf("network connect host is empty")
		}
		if len(r.Host) > MaxNetworkHostBytes {
			return fmt.Errorf("network connect host exceeds %d bytes", MaxNetworkHostBytes)
		}
		if strings.IndexByte(r.Host, 0) >= 0 {
			return fmt.Errorf("network connect host contains NUL")
		}
		if r.Port == 0 {
			return fmt.Errorf("network connect port is zero")
		}
		if r.MaxReceiveBytes == 0 || r.MaxReceiveBytes > MaxNetworkPayloadBytes {
			return fmt.Errorf("network receive limit %d is outside 1..%d", r.MaxReceiveBytes, MaxNetworkPayloadBytes)
		}
		if len(r.Data) != 0 {
			return fmt.Errorf("network connect request contains data")
		}
	case NetworkRequestWrite:
		if r.Host != "" || r.Port != 0 || r.MaxReceiveBytes != 0 {
			return fmt.Errorf("network write request contains connect fields")
		}
		if len(r.Data) == 0 || len(r.Data) > MaxNetworkPayloadBytes {
			return fmt.Errorf("network write size %d is outside 1..%d", len(r.Data), MaxNetworkPayloadBytes)
		}
	case NetworkRequestDisconnect:
		if r.Host != "" || r.Port != 0 || r.MaxReceiveBytes != 0 || len(r.Data) != 0 {
			return fmt.Errorf("network disconnect request contains payload fields")
		}
	default:
		return fmt.Errorf("unknown network request kind %q", r.Kind)
	}
	return nil
}

// NetworkTransportMachine is an optional asynchronous host-network contract.
// Hosts copy and process the ordered request snapshot outside guest execution,
// then serialize completions and inbound data with guest execution.
type NetworkTransportMachine interface {
	NetworkRequests() []NetworkRequest
	CompleteNetworkRequest(sequence uint64, success bool) error
	ReceiveNetworkData(connection uint64, data []byte) error
	DisconnectNetwork(connection uint64) error
}
