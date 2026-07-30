package application

import (
	"encoding/binary"
	"net"
	"strings"
)

func (r *wipiRuntime) dispatchNetwork(name string) (wipiReturn, bool, error) {
	count, ok := networkArgumentCount(name)
	if !ok {
		return wipiReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	switch name {
	case "MC_netConnect":
		r.networkConnected = true
		r.networkCallback = arg(0)
		r.networkParameter = arg(1)
		r.enqueueCallback(arg(0), 0, arg(1))
		return wipiReturn{}, true, nil
	case "MC_netClose":
		r.networkConnected = false
		return wipiReturn{}, true, nil
	case "MC_netSocket":
		descriptor := r.nextSocket
		r.nextSocket++
		r.sockets[descriptor] = &wipiSocket{
			descriptor: descriptor,
			domain:     int32(arg(0)),
			socketType: int32(arg(1)),
		}
		return wipiReturn{low: uint32(descriptor)}, true, nil
	case "MC_netSocketClose":
		descriptor := int32(arg(0))
		if r.sockets[descriptor] == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.sockets, descriptor)
		return wipiReturn{}, true, nil
	case "MC_netSocketConnect":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		socket.address = arg(1)
		socket.port = uint16(arg(2))
		socket.connected = true
		r.enqueueCallback(arg(3), arg(0), 0, arg(4))
		return wipiReturn{}, true, nil
	case "MC_netSocketBind":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		socket.address = arg(1)
		socket.port = uint16(arg(2))
		return wipiReturn{}, true, nil
	case "MC_netSocketAccept":
		listener := r.sockets[int32(arg(0))]
		if listener == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		descriptor := r.nextSocket
		r.nextSocket++
		accepted := *listener
		accepted.descriptor = descriptor
		accepted.connected = true
		accepted.readData = nil
		accepted.writeData = nil
		r.sockets[descriptor] = &accepted
		return wipiReturn{low: uint32(descriptor)}, true, nil
	case "MC_netSocketWrite", "MC_netSocketSendTo":
		socket := r.sockets[int32(arg(0))]
		length := int32(arg(2))
		if socket == nil || length < 0 || length > int32(maxWIPIString) ||
			len(socket.writeData)+int(length) > int(maxWIPIString) {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		data := make([]byte, length)
		if err := r.cpu.ReadMemory(arg(1), data); err != nil {
			return wipiReturn{}, true, err
		}
		socket.writeData = append(socket.writeData, data...)
		if name == "MC_netSocketSendTo" {
			socket.address = arg(3)
			socket.port = uint16(arg(4))
		}
		return wipiReturn{low: uint32(len(data))}, true, nil
	case "MC_netSocketRead", "MC_netSocketRcvFrom":
		socket := r.sockets[int32(arg(0))]
		length := int32(arg(2))
		if socket == nil || length < 0 {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		read := min(len(socket.readData), int(length))
		if err := r.cpu.WriteMemory(arg(1), socket.readData[:read]); err != nil {
			return wipiReturn{}, true, err
		}
		socket.readData = append(socket.readData[:0], socket.readData[read:]...)
		if name == "MC_netSocketRcvFrom" {
			if arg(3) != 0 {
				if err := r.writeU32(arg(3), socket.address); err != nil {
					return wipiReturn{}, true, err
				}
			}
			if arg(4) != 0 {
				var encoded [2]byte
				binary.LittleEndian.PutUint16(encoded[:], socket.port)
				if err := r.cpu.WriteMemory(arg(4), encoded[:]); err != nil {
					return wipiReturn{}, true, err
				}
			}
		}
		return wipiReturn{low: uint32(read)}, true, nil
	case "MC_netSetReadCB":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		socket.readCallback, socket.readParameter = arg(1), arg(2)
		if len(socket.readData) != 0 && socket.readCallback != 0 {
			r.enqueueCallback(socket.readCallback, arg(0), 0, socket.readParameter)
			socket.readCallback, socket.readParameter = 0, 0
		}
		return wipiReturn{}, true, nil
	case "MC_netSetWriteCB":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		socket.writeCallback, socket.writeParameter = arg(1), arg(2)
		if socket.connected && socket.writeCallback != 0 {
			r.enqueueCallback(socket.writeCallback, arg(0), 0, socket.writeParameter)
			socket.writeCallback, socket.writeParameter = 0, 0
		}
		return wipiReturn{}, true, nil
	case "MC_netGetMaxPacketLength":
		return wipiReturn{low: 1460}, true, nil
	case "MC_netGetHostAddr":
		host, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		address, ok := deterministicHostAddress(string(host))
		if !ok {
			address = ^uint32(0)
		}
		r.networkCallback = arg(2)
		r.networkParameter = arg(3)
		if arg(2) == 0 {
			return wipiReturn{low: wipiReturnCode(wipiInvalid)}, true, nil
		}
		r.enqueueCallback(arg(2), address, arg(3))
		return wipiReturn{}, true, nil
	default:
		return wipiReturn{}, false, nil
	}
}

func networkArgumentCount(name string) (int, bool) {
	switch name {
	case "MC_netClose", "MC_netGetMaxPacketLength":
		return 0, true
	case "MC_netSocketClose":
		return 1, true
	case "MC_netConnect", "MC_netSocket":
		return 2, true
	case "MC_netSocketWrite", "MC_netSocketRead", "MC_netSocketBind",
		"MC_netSocketAccept", "MC_netSetReadCB", "MC_netSetWriteCB":
		return 3, true
	case "MC_netGetHostAddr":
		return 4, true
	case "MC_netSocketConnect", "MC_netSocketSendTo", "MC_netSocketRcvFrom":
		return 5, true
	default:
		return 0, false
	}
}

func deterministicHostAddress(host string) (uint32, bool) {
	if strings.EqualFold(host, "localhost") {
		return 0x7f000001, true
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return 0, false
	}
	return binary.BigEndian.Uint32(ip), true
}

func (r *wipiRuntime) dispatchHTTP(name string) (wipiReturn, bool, error) {
	count, ok := httpArgumentCount(name)
	if !ok {
		return wipiReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return wipiReturn{}, true, err
	}
	arg := func(index int) uint32 {
		if index >= len(args) {
			return 0
		}
		return args[index]
	}
	request := func() *wipiHTTP { return r.http[int32(arg(0))] }
	switch name {
	case "MC_netHttpOpen":
		url, err := r.readCString(arg(0))
		if err != nil {
			return wipiReturn{}, true, err
		}
		descriptor := r.nextHTTP
		r.nextHTTP++
		r.http[descriptor] = &wipiHTTP{
			descriptor: descriptor,
			url:        append([]byte(nil), url...),
			method:     []byte("GET"),
			properties: make(map[string][]byte),
		}
		return wipiReturn{low: uint32(descriptor)}, true, nil
	case "MC_netHttpClose":
		descriptor := int32(arg(0))
		if r.http[descriptor] == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		delete(r.http, descriptor)
		return wipiReturn{}, true, nil
	case "MC_netHttpConnect":
		current := request()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		current.connected = true
		current.code = 204
		current.response = nil
		r.enqueueCallback(arg(1), arg(0), ^uint32(0), 0, arg(2))
		return wipiReturn{}, true, nil
	case "MC_netHttpSetRequestMethod":
		current := request()
		if current == nil || int32(arg(3)) < 0 || arg(3) > maxWIPIString {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		method, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		message := make([]byte, arg(3))
		if err := r.cpu.ReadMemory(arg(2), message); err != nil {
			return wipiReturn{}, true, err
		}
		current.method = append(current.method[:0], method...)
		current.request = message
		return wipiReturn{}, true, nil
	case "MC_netHttpGetRequestMethod":
		return r.httpString(request(), arg(1), int32(arg(2)), func(current *wipiHTTP) []byte {
			return current.method
		})
	case "MC_netHttpSetRequestProperty":
		current := request()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		key, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		value, err := r.readCString(arg(2))
		if err != nil {
			return wipiReturn{}, true, err
		}
		current.properties[string(key)] = append([]byte(nil), value...)
		return wipiReturn{}, true, nil
	case "MC_netHttpGetRequestProperty":
		current := request()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		key, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		value, ok := current.properties[string(key)]
		if !ok {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		count, err := r.writeCString(arg(2), value, int32(arg(3)))
		return wipiReturn{low: count}, true, err
	case "MC_netHttpSetProxy":
		current := request()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		current.proxyHost, current.proxyPort = arg(1), uint16(arg(2))
		return wipiReturn{}, true, nil
	case "MC_netHttpGetProxy":
		current := request()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		if arg(1) != 0 {
			if err := r.writeU32(arg(1), current.proxyHost); err != nil {
				return wipiReturn{}, true, err
			}
		}
		if arg(2) != 0 {
			var encoded [2]byte
			binary.LittleEndian.PutUint16(encoded[:], current.proxyPort)
			if err := r.cpu.WriteMemory(arg(2), encoded[:]); err != nil {
				return wipiReturn{}, true, err
			}
		}
		return wipiReturn{}, true, nil
	case "MC_netHttpGetResponseCode":
		if current := request(); current != nil {
			return wipiReturn{low: uint32(current.code)}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_netHttpGetResponseMessage":
		return r.httpString(request(), arg(1), int32(arg(2)), func(*wipiHTTP) []byte {
			return []byte("No Content")
		})
	case "MC_netHttpGetHeaderField":
		current := request()
		if current == nil {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		key, err := r.readCString(arg(1))
		if err != nil {
			return wipiReturn{}, true, err
		}
		var value []byte
		if strings.EqualFold(string(key), "Content-Length") {
			value = []byte("0")
		} else {
			return wipiReturn{low: ^uint32(0)}, true, nil
		}
		written, err := r.writeCString(arg(2), value, int32(arg(3)))
		return wipiReturn{low: written}, true, err
	case "MC_netHttpGetLength":
		if current := request(); current != nil {
			return wipiReturn{low: uint32(len(current.response))}, true, nil
		}
		return wipiReturn{low: ^uint32(0)}, true, nil
	case "MC_netHttpGetType":
		return r.httpString(request(), arg(1), int32(arg(2)), func(*wipiHTTP) []byte {
			return []byte("application/octet-stream")
		})
	case "MC_netHttpGetEncoding":
		return r.httpString(request(), arg(1), int32(arg(2)), func(*wipiHTTP) []byte {
			return []byte("identity")
		})
	default:
		return wipiReturn{}, false, nil
	}
}

func httpArgumentCount(name string) (int, bool) {
	switch name {
	case "MC_netHttpOpen", "MC_netHttpClose", "MC_netHttpGetResponseCode",
		"MC_netHttpGetLength":
		return 1, true
	case "MC_netHttpConnect":
		return 3, true
	case "MC_netHttpGetResponseMessage", "MC_netHttpGetRequestMethod",
		"MC_netHttpSetRequestProperty", "MC_netHttpSetProxy", "MC_netHttpGetProxy",
		"MC_netHttpGetType", "MC_netHttpGetEncoding":
		return 3, true
	case "MC_netHttpGetHeaderField", "MC_netHttpSetRequestMethod",
		"MC_netHttpGetRequestProperty":
		return 4, true
	default:
		return 0, false
	}
}

func (r *wipiRuntime) httpString(
	current *wipiHTTP,
	output uint32,
	length int32,
	value func(*wipiHTTP) []byte,
) (wipiReturn, bool, error) {
	if current == nil {
		return wipiReturn{low: ^uint32(0)}, true, nil
	}
	count, err := r.writeCString(output, value(current), length)
	return wipiReturn{low: count}, true, err
}
