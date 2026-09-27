package runtime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func localSocket(t *testing.T, s *Services, host string, port uint16) ServiceID {
	t.Helper()
	id, err := s.Network.OpenSocket(1, 2, 1)
	check(t, err)
	check(t, s.Network.ConnectSocket(1, id, host, port))
	check(t, s.CompleteSocketResponse(1, id, true, 0))
	return id
}

func funterPacket(code uint16, body []byte) []byte {
	p := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint32(p, uint32(len(p)))
	binary.LittleEndian.PutUint16(p[4:], 0xffff)
	binary.LittleEndian.PutUint16(p[6:], code)
	copy(p[8:], body)
	return p
}

func TestLocalServersKnownResponsesAndEndpointIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, host        string
		port              uint16
		request, response []byte
	}{
		{"gpang-auth", "222.237.78.175", 40240, []byte{0, 0, 0, 3, 'I', 'R', 9}, []byte{0, 0, 0, 12, 'I', 'R', 'O', 'K', 0, 0, 0, 0, 0, 0, 0, 0}},
		{"gpang-cash", "222.237.78.175", 10020, []byte("CASH|identity|"), []byte{0, 4, 'S', 'A', 'S', 'H'}},
		{"gpang-balance", "222.237.78.175", 10020, []byte("CKN_C|identity|"), append([]byte{0, 13}, []byte("SKN_C|0|9999|")...)},
		{"gpang-pay", "222.237.78.175", 10020, []byte("CKN_U|identity|"), append([]byte{0, 8}, []byte("SKN_U|0|")...)},
		{"dragoneyes", "211.110.18.253", 8501, []byte{2, 0, 0xf8, 0x2a}, []byte{3, 0, 0xf9, 0x2a, 0}},
		{"snowboard-data", "211.234.104.44", 10279, []byte("0311"), []byte("0311000000000001000000001000000000000000")},
		{"snowboard-extended", "211.234.104.44", 10279, []byte("0111"), append([]byte("0011000000610001000000001000000000000000"), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0x20, 0, 0, 0, 0, 0, 0, 0, 0}...)},
		{"snowboard-gateway", "203.236.40.219", 6200, make([]byte, 83), append(append(make([]byte, 4), append([]byte{'0'}, make([]byte, 37)...)...), '0')},
		{"foreign-host", "127.0.0.1", 8501, []byte{2, 0, 0xf8, 0x2a}, nil},
		{"foreign-port", "211.110.18.253", 8502, []byte{2, 0, 0xf8, 0x2a}, nil},
		{"unknown-dragon-opcode", "211.110.18.253", 8503, []byte{2, 0, 1, 0}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewServices(Config{})
			check(t, err)
			id := localSocket(t, s, tc.host, tc.port)
			for _, part := range [][]byte{tc.request[:1], tc.request[1:]} {
				n, err := s.WriteSocketRequest(1, id, part)
				check(t, err)
				if n != len(part) {
					t.Fatalf("write = %d", n)
				}
			}
			got, err := s.Network.SocketRead(1, id, 1024)
			check(t, err)
			if !bytes.Equal(got, tc.response) {
				t.Fatalf("reply = %x, want %x", got, tc.response)
			}
		})
	}
}

func TestLocalServerFailureDoesNotConsumeRequest(t *testing.T) {
	for _, failure := range []string{"frame", "buffer", "events", "replay"} {
		t.Run(failure, func(t *testing.T) {
			config := Config{}
			config.Limits = DefaultLimits()
			if failure == "buffer" {
				config.Limits.Network.MaxBufferBytes = 8
			}
			if failure == "events" {
				config.Limits.MaxEvents = 1
			}
			if failure == "replay" {
				config.ReplayMode = ReplayRecord
				config.Limits.Replay.MaxEntries = 1
			}
			s, err := NewServices(config)
			check(t, err)
			id := localSocket(t, s, "222.237.78.175", 40240)
			request := []byte{0, 0, 0, 3, 'I', 'R', 9}
			if failure == "frame" {
				request = []byte{0xff, 0xff, 0xff, 0xff}
			}
			before := s.Snapshot()
			if n, err := s.WriteSocketRequest(1, id, request); err == nil || n != 0 {
				t.Fatalf("failed write = %d, %v", n, err)
			}
			if !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("failed request mutated services")
			}
		})
	}
}

func TestLocalServersCoalescedFramesAndReplay(t *testing.T) {
	config := Config{ReplayMode: ReplayRecord}
	s, err := NewServices(config)
	check(t, err)
	id := localSocket(t, s, "211.110.18.253", 8503)
	request := []byte{2, 0, 0xf8, 0x2a, 2, 0, 0xf8, 0x2a}
	_, err = s.WriteSocketRequest(1, id, request)
	check(t, err)
	want := []byte{3, 0, 0xf9, 0x2a, 0, 3, 0, 0xf9, 0x2a, 0}
	got, err := s.Network.SocketRead(1, id, 100)
	check(t, err)
	if !bytes.Equal(got, want) {
		t.Fatalf("coalesced response = %x", got)
	}
	log := s.Replay.Snapshot()
	log.Mode = ReplayPlayback
	config.ReplayMode = ReplayPlayback
	playback, err := NewServices(config)
	check(t, err)
	check(t, playback.Replay.Restore(log))
	playbackID := localSocket(t, playback, "211.110.18.253", 8503)
	_, err = playback.WriteSocketRequest(1, playbackID, request)
	check(t, err)
	got, err = playback.Network.SocketRead(1, playbackID, 100)
	check(t, err)
	if !bytes.Equal(got, want) || playback.Replay.Snapshot().Cursor != uint32(len(log.Entries)) {
		t.Fatal("local reply replay diverged")
	}
}

func TestLocalServerStateRejectsInvalidCursorAndFile(t *testing.T) {
	s, err := NewServices(Config{})
	check(t, err)
	localSocket(t, s, "211.115.203.17", 15102)
	for _, mutate := range []func(*SocketState){
		func(socket *SocketState) { socket.LocalCursor = 1 },
		func(socket *SocketState) { socket.LocalFile = "../escape" },
		func(socket *SocketState) { socket.LocalFile = "item.dat"; socket.Host = "127.0.0.1" },
	} {
		before := s.Snapshot()
		invalid := s.Snapshot()
		mutate(&invalid.Network.Sockets[0])
		if err := s.Restore(invalid); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("invalid local state = %v", err)
		}
		if !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("invalid state mutated services")
		}
	}
}

func TestFunterSavedSelectionAndTerminalStatuses(t *testing.T) {
	s, err := NewServices(Config{})
	check(t, err)
	check(t, s.Storage.MountPackage(map[string][]byte{"tail.bin": bytes.Repeat([]byte{0x42}, 8193)}))
	id := localSocket(t, s, "211.115.203.17", 15102)
	body := make([]byte, 32)
	copy(body[16:], "tail.bin")
	_, err = s.WriteSocketRequest(1, id, funterPacket(1602, body))
	check(t, err)
	_, err = s.Network.SocketRead(1, id, 10000)
	check(t, err)
	encoded, err := s.MarshalBinary()
	check(t, err)
	clone, err := NewServices(Config{})
	check(t, err)
	check(t, clone.UnmarshalBinary(encoded))
	_, err = clone.WriteSocketRequest(1, id, funterPacket(1604, binary.LittleEndian.AppendUint32(nil, 8192)))
	check(t, err)
	got, err := clone.Network.SocketRead(1, id, 100)
	check(t, err)
	if !bytes.Equal(got, funterPacket(1605, []byte{0, 0, 0, 0, 1, 0, 0, 0, 0x42})) {
		t.Fatalf("saved selection = %x", got)
	}
	_, err = clone.WriteSocketRequest(1, id, funterPacket(1604, binary.LittleEndian.AppendUint32(nil, 8193)))
	check(t, err)
	got, err = clone.Network.SocketRead(1, id, 100)
	check(t, err)
	if !bytes.Equal(got, funterPacket(1605, []byte{131, 0, 0, 0})) {
		t.Fatalf("EOF = %x", got)
	}
	for i := range body {
		body[i] = 0
	}
	copy(body[16:], "../private")
	_, err = clone.WriteSocketRequest(1, id, funterPacket(1602, body))
	check(t, err)
	got, err = clone.Network.SocketRead(1, id, 100)
	check(t, err)
	if !bytes.Equal(got, funterPacket(1603, []byte{1, 0, 0, 0})) {
		t.Fatalf("invalid path = %x", got)
	}
}

func TestFunterArchiveDownloadContinuationAndState(t *testing.T) {
	s, err := NewServices(Config{})
	check(t, err)
	content := bytes.Repeat([]byte{0x5a}, 8195)
	check(t, s.Storage.MountPackage(map[string][]byte{"item.dat": content, "too-long-to-fit-in-16.dat": {1}}))
	id := localSocket(t, s, "211.115.203.17", 15102)
	_, err = s.WriteSocketRequest(1, id, funterPacket(1600, nil))
	check(t, err)
	got, err := s.Network.SocketRead(1, id, 1024)
	check(t, err)
	wantBody := make([]byte, 12+28)
	binary.LittleEndian.PutUint32(wantBody[8:], 1)
	binary.LittleEndian.PutUint32(wantBody[20:], uint32(len(content)))
	copy(wantBody[24:], "item.dat")
	if !bytes.Equal(got, funterPacket(1601, wantBody)) {
		t.Fatalf("listing = %x", got)
	}
	body := make([]byte, 32)
	copy(body[16:], "item.dat")
	request := funterPacket(1602, body)
	_, err = s.WriteSocketRequest(1, id, request[:9])
	check(t, err)
	encoded, err := s.MarshalBinary()
	check(t, err)
	clone, err := NewServices(Config{})
	check(t, err)
	check(t, clone.UnmarshalBinary(encoded))
	for _, current := range []*Services{s, clone} {
		_, err = current.WriteSocketRequest(1, id, request[9:])
		check(t, err)
		got, err = current.Network.SocketRead(1, id, 10000)
		check(t, err)
		if len(got) != 8208 || binary.LittleEndian.Uint16(got[6:]) != 1603 || binary.LittleEndian.Uint32(got[12:]) != 8192 || !bytes.Equal(got[16:], content[:8192]) {
			t.Fatalf("first chunk = %x (%d)", got[:min(len(got), 16)], len(got))
		}
		_, err = current.WriteSocketRequest(1, id, funterPacket(1604, binary.LittleEndian.AppendUint32(nil, 8192)))
		check(t, err)
		got, err = current.Network.SocketRead(1, id, 100)
		check(t, err)
		if binary.LittleEndian.Uint16(got[6:]) != 1605 || !bytes.Equal(got[16:], content[8192:]) {
			t.Fatalf("continuation = %x", got)
		}
	}
	if !reflect.DeepEqual(s.Snapshot(), clone.Snapshot()) {
		t.Fatal("saved partial request resumed differently")
	}
}
