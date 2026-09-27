package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// HasLocalSocketServer identifies only the supported retired TCP endpoints.
// Generic provider/replay sockets keep their existing explicit-response model.
func (n *Network) HasLocalSocketServer(owner OwnerID, id ServiceID) bool {
	socket, err := n.socket(owner, id)
	return err == nil && localServerKind(socket.host, socket.port, socket.socketType) != ""
}

// IsLocalSocketEndpoint reports which TCP destinations have offline handlers.
func IsLocalSocketEndpoint(host string, port uint16) bool {
	return localServerKind(host, port, 1) != ""
}

func localServerKind(host string, port uint16, socketType int32) string {
	if socketType != 1 {
		return ""
	}
	switch host {
	case "222.237.78.175":
		if port == 40240 {
			return "gpang-auth"
		}
		if port == 10020 {
			return "gpang-billing"
		}
	case "211.115.203.17":
		if port == 15102 {
			return "funter"
		}
	case "211.110.18.253":
		if port == 8501 || port == 8503 {
			return "dragoneyes"
		}
	case "211.234.104.44":
		if port == 10279 {
			return "snowboard-data"
		}
	case "203.236.40.219", "203.236.40.205":
		if port == 6200 {
			return "snowboard-gateway"
		}
	}
	return ""
}

// WriteSocketRequest sends guest bytes and serves supported offline protocols.
// A complete request, its response, and the parsing cursor commit atomically.
// Partial frames and the Funter filename live in serializable socket state.
func (s *Services) WriteSocketRequest(owner OwnerID, id ServiceID, data []byte) (int, error) {
	socket, err := s.Network.socket(owner, id)
	if err != nil {
		return 0, err
	}
	before := *socket
	n, err := s.Network.SocketWrite(owner, id, data)
	if err != nil {
		return 0, err
	}
	kind := localServerKind(socket.host, socket.port, socket.socketType)
	if kind == "" {
		return n, nil
	}
	reply, err := s.localSocketReplies(socket, kind)
	if err == nil && len(reply) != 0 {
		err = s.InjectSocketResponse(owner, id, reply, s.Clock.Monotonic())
	}
	if err != nil {
		// Replay-aware injection can restore Network onto new socket objects.
		*s.Network.sockets[id] = before
		return 0, err
	}
	return n, nil
}

func (s *Services) localSocketReplies(socket *modeledSocket, kind string) ([]byte, error) {
	var result []byte
	appendReply := func(reply []byte) error {
		if uint64(len(reply)) > s.Network.limits.MaxBufferBytes-uint64(len(socket.readData))-uint64(len(result)) {
			return fmt.Errorf("%w: local server response quota", ErrLimitExceeded)
		}
		result = append(result, reply...)
		return nil
	}
	for socket.localCursor < uint64(len(socket.writeData)) {
		pending := socket.writeData[socket.localCursor:]
		var reply []byte
		var consumed uint64
		switch kind {
		case "gpang-billing":
			if socket.localCursor != 0 {
				socket.localCursor = uint64(len(socket.writeData))
				return result, nil
			}
			if len(pending) < 6 {
				return result, nil
			}
			var body []byte
			switch {
			case bytes.HasPrefix(pending, []byte("CASH|")):
				body = []byte("SASH")
			case bytes.HasPrefix(pending, []byte("CKN_C|")):
				body = []byte("SKN_C|0|9999|")
			case bytes.HasPrefix(pending, []byte("CKN_U|")):
				body = []byte("SKN_U|0|")
			}
			if body != nil {
				reply = binary.BigEndian.AppendUint16(nil, uint16(len(body)))
				reply = append(reply, body...)
			}
			consumed = uint64(len(pending))
		case "snowboard-data":
			if len(pending) < 4 {
				return result, nil
			}
			reply = append([]byte{'0', pending[1], '1', '1'}, []byte("000000000001000000001000000000000000")...)
			if pending[1] == '1' || pending[1] == '2' {
				copy(reply, "0011")
				copy(reply[4:12], "00000061")
				extra := make([]byte, 21)
				extra[11], extra[12] = 1, 0x20
				reply = append(reply, extra...)
			}
			consumed = uint64(len(pending))
		case "snowboard-gateway":
			if len(pending) < 83 {
				return result, nil
			}
			reply = make([]byte, 43)
			reply[4], reply[42] = '0', '0'
			consumed = 83
		default:
			var length uint64
			var minimum uint64
			switch kind {
			case "gpang-auth":
				if len(pending) < 4 {
					return result, nil
				}
				length, minimum = uint64(binary.BigEndian.Uint32(pending))+4, 4
			case "dragoneyes":
				if len(pending) < 2 {
					return result, nil
				}
				length, minimum = uint64(binary.LittleEndian.Uint16(pending))+2, 4
			case "funter":
				if len(pending) < 4 {
					return result, nil
				}
				length, minimum = uint64(binary.LittleEndian.Uint32(pending)), 8
			}
			if length < minimum || length > s.Network.limits.MaxBufferBytes {
				return nil, fmt.Errorf("%w: invalid local server frame size", ErrInvalidArgument)
			}
			if length > uint64(len(pending)) {
				return result, nil
			}
			frame := pending[:length]
			consumed = length
			switch kind {
			case "gpang-auth":
				if bytes.HasPrefix(frame[4:], []byte{'I', 'R', 9}) {
					reply = make([]byte, 16)
					binary.BigEndian.PutUint32(reply, 12)
					copy(reply[4:], "IROK")
				}
			case "dragoneyes":
				if binary.LittleEndian.Uint16(frame[2:]) == 11000 {
					reply = []byte{3, 0, 0xf9, 0x2a, 0}
				}
			case "funter":
				if binary.LittleEndian.Uint16(frame[4:]) == 0xffff {
					var err error
					reply, err = s.funterReply(socket, frame, s.Network.limits.MaxBufferBytes-uint64(len(socket.readData))-uint64(len(result)))
					if err != nil {
						return nil, err
					}
				}
			}
		}
		if err := appendReply(reply); err != nil {
			return nil, err
		}
		socket.localCursor += consumed
	}
	return result, nil
}

func validFunterName(name string) bool {
	return name != "" && len(name) <= 16 && name != "." && name != ".." && !strings.ContainsAny(name, "/\\:\x00")
}

// Funter serves only caller-mounted, immutable package files representable by
// the protocol's 16-byte filename field. It never reads private or host files.
func (s *Services) funterReply(socket *modeledSocket, frame []byte, budget uint64) ([]byte, error) {
	code := binary.LittleEndian.Uint16(frame[6:])
	var body []byte
	response := code + 1
	switch code {
	case 1600:
		var names []string
		for _, file := range s.Storage.files {
			name := strings.TrimPrefix(file.path, "/")
			if file.namespace == NamespacePackage && validFunterName(name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if uint64(len(names))*28+20 > budget {
			return nil, fmt.Errorf("%w: Funter catalogue quota", ErrLimitExceeded)
		}
		body = make([]byte, 12+28*len(names))
		binary.LittleEndian.PutUint32(body[8:], uint32(len(names)))
		for i, name := range names {
			file := s.Storage.files[storageKey(NamespacePackage, "/"+name)]
			row := body[12+i*28:]
			binary.LittleEndian.PutUint32(row[8:], uint32(len(file.data)))
			copy(row[12:28], name)
		}
	case 1602, 1604:
		if code == 1602 {
			if len(frame) < 40 {
				body = make([]byte, 4)
				break
			}
			name := frame[24:40]
			if end := bytes.IndexByte(name, 0); end >= 0 {
				name = name[:end]
			}
			socket.localFile = string(name)
			if !validFunterName(socket.localFile) {
				socket.localFile = ""
			}
		} else if len(frame) < 12 || socket.localFile == "" {
			body = make([]byte, 4)
			break
		}
		file := s.Storage.files[storageKey(NamespacePackage, "/"+socket.localFile)]
		status := uint32(1)
		var chunk []byte
		if file != nil {
			offset := uint64(binary.LittleEndian.Uint32(frame[8:]))
			status = 131
			if offset < uint64(len(file.data)) {
				status = 0
				chunk = file.data[offset:min(offset+8192, uint64(len(file.data)))]
			}
		}
		body = binary.LittleEndian.AppendUint32(nil, status)
		if status == 0 {
			body = binary.LittleEndian.AppendUint32(body, uint32(len(chunk)))
			body = append(body, chunk...)
		}
	default:
		return nil, nil
	}
	if uint64(len(body))+8 > budget {
		return nil, fmt.Errorf("%w: Funter response quota", ErrLimitExceeded)
	}
	result := binary.LittleEndian.AppendUint32(nil, uint32(len(body)+8))
	result = binary.LittleEndian.AppendUint16(result, 0xffff)
	result = binary.LittleEndian.AppendUint16(result, response)
	return append(result, body...), nil
}
