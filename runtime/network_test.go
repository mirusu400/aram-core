package runtime

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestModeledSocketRequiresExplicitResponse(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	socket, err := services.Network.OpenSocket(2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Network.ConnectSocket(2, socket, "localhost", 1234); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Network.SocketWrite(2, socket, []byte("early")); err == nil {
		t.Fatal("SocketWrite succeeded before provider completion")
	}
	if err := services.Network.CompleteSocketConnect(
		2,
		socket,
		true,
		int64(time.Millisecond),
		services.Events,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Network.SocketWrite(2, socket, []byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := services.Network.InjectSocketRead(
		2,
		socket,
		[]byte("response"),
		int64(2*time.Millisecond),
		services.Events,
	); err != nil {
		t.Fatal(err)
	}
	got, err := services.Network.SocketRead(2, socket, 4)
	if err != nil || string(got) != "resp" {
		t.Fatalf("SocketRead = %q, %v", got, err)
	}
	state := services.Snapshot()
	clone, err := NewServices(state.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.Restore(state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Network.Snapshot(), services.Network.Snapshot()) {
		t.Fatal("network state did not round-trip")
	}
}

func TestHTTPCompletionRollsBackWhenEventQueueIsFull(t *testing.T) {
	registry := NewRegistry(16)
	network, err := NewNetwork(registry, NetworkLimits{})
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(1, 16)
	if _, err := bus.Enqueue(Event{Kind: EventApplication}); err != nil {
		t.Fatal(err)
	}
	request, err := network.OpenHTTP(1, "https://example.invalid/resource")
	if err != nil {
		t.Fatal(err)
	}
	if err := network.SetHTTPRequest(
		1,
		request,
		"POST",
		[]HTTPProperty{{Name: "content-type", Value: "application/octet-stream"}},
		[]byte("request"),
	); err != nil {
		t.Fatal(err)
	}
	if err := network.BeginHTTP(1, request); err != nil {
		t.Fatal(err)
	}
	before := network.Snapshot()
	if err := network.CompleteHTTP(
		1,
		request,
		200,
		nil,
		[]byte("response"),
		0,
		bus,
	); err == nil {
		t.Fatal("CompleteHTTP succeeded with a full event queue")
	}
	if after := network.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("failed HTTP completion changed request state")
	}
	if data, err := network.HTTPRead(1, request, 100); err == nil ||
		!bytes.Equal(data, nil) {
		t.Fatalf("HTTPRead before completion = %v, %v", data, err)
	}
}

func TestNetworkRestoreRejectsNonCanonicalStateAtomically(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	socket, err := services.Network.OpenSocket(3, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Network.ConnectSocket(
		3,
		socket,
		"localhost",
		8080,
	); err != nil {
		t.Fatal(err)
	}
	if err := services.Network.CompleteSocketConnect(
		3,
		socket,
		true,
		0,
		services.Events,
	); err != nil {
		t.Fatal(err)
	}
	request, err := services.Network.OpenHTTP(
		3,
		"https://example.invalid/resource",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Network.SetHTTPRequest(
		3,
		request,
		"post",
		[]HTTPProperty{
			{Name: "x-second", Value: "2"},
			{Name: "X-First", Value: "1"},
		},
		[]byte("request"),
	); err != nil {
		t.Fatal(err)
	}
	if err := services.Network.BeginHTTP(3, request); err != nil {
		t.Fatal(err)
	}
	if err := services.Network.CompleteHTTP(
		3,
		request,
		200,
		[]HTTPProperty{{Name: "content-type", Value: "text/plain"}},
		[]byte("response"),
		0,
		services.Events,
	); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		mutate func(*NetworkState)
	}{
		{
			name: "socket host",
			mutate: func(state *NetworkState) {
				state.Sockets[0].Host = "LOCALHOST"
			},
		},
		{
			name: "socket address",
			mutate: func(state *NetworkState) {
				state.Sockets[0].Address ^= 1
			},
		},
		{
			name: "HTTP URL",
			mutate: func(state *NetworkState) {
				state.HTTP[0].URL = "https://example.invalid/%zz"
			},
		},
		{
			name: "HTTP method",
			mutate: func(state *NetworkState) {
				state.HTTP[0].Method = "post"
			},
		},
		{
			name: "HTTP header order",
			mutate: func(state *NetworkState) {
				state.HTTP[0].RequestHeaders[0],
					state.HTTP[0].RequestHeaders[1] =
					state.HTTP[0].RequestHeaders[1],
					state.HTTP[0].RequestHeaders[0]
			},
		},
		{
			name: "HTTP response lifecycle",
			mutate: func(state *NetworkState) {
				state.HTTP[0].State = ConnectionConnecting
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := services.Network.Snapshot()
			invalid := services.Network.Snapshot()
			test.mutate(&invalid)
			if err := services.Network.Restore(invalid); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Restore invalid network error = %v", err)
			}
			if after := services.Network.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatal("rejected network restore mutated live state")
			}
		})
	}
}

func TestNetworkRejectsEmbeddedNULAndNonCanonicalReplayHeaders(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	socket, err := services.Network.OpenSocket(1, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Network.ConnectSocket(
		1,
		socket,
		"localhost\x00suffix",
		80,
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ConnectSocket embedded NUL error = %v", err)
	}
	if _, err := services.Network.OpenHTTP(
		1,
		"https://example.invalid/\x00suffix",
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenHTTP embedded NUL error = %v", err)
	}
	request, err := services.Network.OpenHTTP(
		1,
		"https://example.invalid/",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Network.SetHTTPRequest(
		1,
		request,
		"GE\x00T",
		nil,
		nil,
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetHTTPRequest embedded NUL error = %v", err)
	}

	encoded := encodeHTTPReplayResponse(
		[]HTTPProperty{{Name: "Content-Type", Value: "text/plain"}},
		nil,
	)
	if _, _, err := decodeHTTPReplayResponse(
		encoded,
		services.Network.limits,
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("decodeHTTPReplayResponse non-canonical header error = %v", err)
	}
}
