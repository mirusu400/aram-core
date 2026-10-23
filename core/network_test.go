package core

import (
	"strings"
	"testing"
)

func TestNetworkRequestValidate(t *testing.T) {
	valid := []NetworkRequest{
		{Sequence: 1, Connection: 2, Kind: NetworkRequestConnect, Host: "127.0.0.1", Port: 8501, MaxReceiveBytes: 1024},
		{Sequence: 2, Connection: 2, Kind: NetworkRequestWrite, Data: []byte("packet")},
		{Sequence: 3, Connection: 2, Kind: NetworkRequestDisconnect},
	}
	for _, request := range valid {
		if err := request.Validate(); err != nil {
			t.Fatalf("valid request %+v: %v", request, err)
		}
	}

	invalid := []NetworkRequest{
		{},
		{Sequence: 1, Kind: NetworkRequestDisconnect},
		{Sequence: 1, Connection: 1, Kind: NetworkRequestConnect, Host: " ", Port: 1, MaxReceiveBytes: 1},
		{Sequence: 1, Connection: 1, Kind: NetworkRequestConnect, Host: "host", Port: 0, MaxReceiveBytes: 1},
		{Sequence: 1, Connection: 1, Kind: NetworkRequestConnect, Host: "host", Port: 1},
		{Sequence: 1, Connection: 1, Kind: NetworkRequestConnect, Host: strings.Repeat("x", MaxNetworkHostBytes+1), Port: 1, MaxReceiveBytes: 1},
		{Sequence: 1, Connection: 1, Kind: NetworkRequestWrite},
		{Sequence: 1, Connection: 1, Kind: NetworkRequestDisconnect, Data: []byte{1}},
		{Sequence: 1, Connection: 1, Kind: "unknown"},
	}
	for _, request := range invalid {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid request accepted: %+v", request)
		}
	}
}
