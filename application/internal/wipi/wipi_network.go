package wipi

import (
	"encoding/binary"
	"net"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

// DeliverSocketRead fires the async read callback registered on the socket bound
// to serviceID once bytes have arrived (an EventNetworkReady "read" event). The
// immediate-data path in MC_netSetReadCB covers data already buffered at
// registration; this covers data that arrives afterwards, without which a guest
// that arms a read callback and waits for a server reply never wakes. Returns
// true if a callback was fired.
func (r *Runtime) DeliverSocketRead(serviceID shared.ServiceID) bool {
	for descriptor, id := range r.socketServices {
		if id != serviceID {
			continue
		}
		socket := r.sockets[descriptor]
		if socket == nil || socket.readCallback == 0 {
			return false
		}
		callback, parameter := socket.readCallback, socket.readParameter
		socket.readCallback, socket.readParameter = 0, 0
		r.EnqueueCallback(callback, uint32(descriptor), 0, parameter)
		return true
	}
	return false
}

// answerOfflineCarrier acknowledges an LGT carrier request so an auth-gated
// title proceeds offline. The carrier protocol frames each message as
// [u32 len][u16 0xffff marker][u16 code][body]; a real carrier replies to a
// request with a matching success message. With the billing servers shut down
// ARAM cannot complete the exchange, so it synthesizes that acknowledgement:
// a minimal reply carrying code+1 (the success response code) and subtype 0,
// which the title's dispatcher accepts to continue past its connecting screen.
func (r *Runtime) answerOfflineCarrier(socket *wipiSocket, request []byte) {
	if len(request) < 8 || request[4] != 0xff || request[5] != 0xff {
		return
	}
	code := binary.LittleEndian.Uint16(request[6:8]) + 1
	reply := []byte{9, 0, 0, 0, 0xff, 0xff, byte(code), byte(code >> 8), 0}
	socket.readData = append(socket.readData, reply...)
	if socket.readCallback != 0 {
		callback, parameter := socket.readCallback, socket.readParameter
		socket.readCallback, socket.readParameter = 0, 0
		r.EnqueueCallback(callback, uint32(socket.descriptor), 0, parameter)
	}
}

func (r *Runtime) dispatchNetwork(name string) (guest.WIPIReturn, bool, error) {
	count, ok := networkArgumentCount(name)
	if !ok {
		return guest.WIPIReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
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
		r.Services.Device.SetNetworkAvailable(true)
		r.networkCallback = arg(0)
		r.networkParameter = arg(1)
		r.EnqueueCallback(arg(0), 0, arg(1))
		return guest.WIPIReturn{}, true, nil
	case "MC_netClose":
		r.networkConnected = false
		r.Services.Device.SetNetworkAvailable(false)
		return guest.WIPIReturn{}, true, nil
	case "MC_netSocket":
		serviceID, serviceErr := r.Services.Network.OpenSocket(
			r.ServiceOwner,
			int32(arg(0)),
			int32(arg(1)),
		)
		if serviceErr != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		descriptor := r.nextSocket
		r.nextSocket++
		r.sockets[descriptor] = &wipiSocket{
			descriptor: descriptor,
			domain:     int32(arg(0)),
			socketType: int32(arg(1)),
		}
		r.socketServices[descriptor] = serviceID
		return guest.WIPIReturn{Low: uint32(descriptor)}, true, nil
	case "MC_netSocketClose":
		descriptor := int32(arg(0))
		if r.sockets[descriptor] == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceID := r.socketServices[descriptor]; serviceID != 0 {
			if err := r.Services.Network.CloseSocket(
				r.ServiceOwner,
				serviceID,
				r.Services.Events,
			); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			delete(r.socketServices, descriptor)
		}
		delete(r.sockets, descriptor)
		return guest.WIPIReturn{}, true, nil
	case "MC_netSocketConnect":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		socket.address = arg(1)
		socket.port = uint16(arg(2))
		serviceID := r.socketServices[socket.descriptor]
		host := wipiNetworkAddress(socket.address)
		if err := r.Services.Network.ConnectSocket(
			r.ServiceOwner,
			serviceID,
			host,
			socket.port,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if err := r.Services.CompleteSocketResponse(
			r.ServiceOwner,
			serviceID,
			true,
			r.Services.Clock.Monotonic(),
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		socket.connected = true
		r.EnqueueCallback(arg(3), arg(0), 0, arg(4))
		return guest.WIPIReturn{}, true, nil
	case "MC_netSocketBind":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		socket.address = arg(1)
		socket.port = uint16(arg(2))
		if err := r.Services.Network.BindSocket(
			r.ServiceOwner,
			r.socketServices[socket.descriptor],
			wipiNetworkAddress(socket.address),
			socket.port,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_netSocketAccept":
		listener := r.sockets[int32(arg(0))]
		if listener == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		descriptor := r.nextSocket
		r.nextSocket++
		accepted := *listener
		accepted.descriptor = descriptor
		accepted.connected = true
		accepted.readData = nil
		accepted.writeData = nil
		r.sockets[descriptor] = &accepted
		serviceID, serviceErr := r.Services.Network.OpenSocket(
			r.ServiceOwner,
			accepted.domain,
			accepted.socketType,
		)
		if serviceErr != nil {
			delete(r.sockets, descriptor)
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceErr = r.Services.Network.ConnectSocket(
			r.ServiceOwner,
			serviceID,
			wipiNetworkAddress(accepted.address),
			accepted.port,
		); serviceErr == nil {
			serviceErr = r.Services.CompleteSocketResponse(
				r.ServiceOwner,
				serviceID,
				true,
				r.Services.Clock.Monotonic(),
			)
		}
		if serviceErr != nil {
			_ = r.Services.Network.CloseSocket(r.ServiceOwner, serviceID, r.Services.Events)
			delete(r.sockets, descriptor)
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		r.socketServices[descriptor] = serviceID
		return guest.WIPIReturn{Low: uint32(descriptor)}, true, nil
	case "MC_netSocketWrite", "MC_netSocketSendTo":
		socket := r.sockets[int32(arg(0))]
		length := int32(arg(2))
		if socket == nil || length < 0 || length > int32(maxWIPIString) ||
			len(socket.writeData)+int(length) > int(maxWIPIString) {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		data := make([]byte, length)
		if err := r.CPU.ReadMemory(arg(1), data); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		socket.writeData = append(socket.writeData, data...)
		if r.OfflineCarrierAuth {
			r.answerOfflineCarrier(socket, data)
		}
		if name == "MC_netSocketSendTo" {
			socket.address = arg(3)
			socket.port = uint16(arg(4))
		}
		if _, err := r.Services.Network.SocketWrite(
			r.ServiceOwner,
			r.socketServices[socket.descriptor],
			data,
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{Low: uint32(len(data))}, true, nil
	case "MC_netSocketRead", "MC_netSocketRcvFrom":
		socket := r.sockets[int32(arg(0))]
		length := int32(arg(2))
		if socket == nil || length < 0 {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		read := min(len(socket.readData), int(length))
		if err := r.CPU.WriteMemory(arg(1), socket.readData[:read]); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		socket.readData = append(socket.readData[:0], socket.readData[read:]...)
		if name == "MC_netSocketRcvFrom" {
			if arg(3) != 0 {
				if err := r.WriteU32(arg(3), socket.address); err != nil {
					return guest.WIPIReturn{}, true, err
				}
			}
			if arg(4) != 0 {
				var encoded [2]byte
				binary.LittleEndian.PutUint16(encoded[:], socket.port)
				if err := r.CPU.WriteMemory(arg(4), encoded[:]); err != nil {
					return guest.WIPIReturn{}, true, err
				}
			}
		}
		return guest.WIPIReturn{Low: uint32(read)}, true, nil
	case "MC_netSetReadCB":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		socket.readCallback, socket.readParameter = arg(1), arg(2)
		if len(socket.readData) != 0 && socket.readCallback != 0 {
			r.EnqueueCallback(socket.readCallback, arg(0), 0, socket.readParameter)
			socket.readCallback, socket.readParameter = 0, 0
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_netSetWriteCB":
		socket := r.sockets[int32(arg(0))]
		if socket == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		socket.writeCallback, socket.writeParameter = arg(1), arg(2)
		if socket.connected && socket.writeCallback != 0 {
			r.EnqueueCallback(socket.writeCallback, arg(0), 0, socket.writeParameter)
			socket.writeCallback, socket.writeParameter = 0, 0
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_netGetMaxPacketLength":
		return guest.WIPIReturn{Low: 1460}, true, nil
	case "MC_netGetHostAddr":
		host, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		address, ok := deterministicHostAddress(string(host))
		if !ok {
			address = ^uint32(0)
		}
		r.networkCallback = arg(2)
		r.networkParameter = arg(3)
		if arg(2) == 0 {
			return guest.WIPIReturn{Low: guest.WIPIReturnCode(guest.WIPIInvalid)}, true, nil
		}
		r.EnqueueCallback(arg(2), address, arg(3))
		return guest.WIPIReturn{}, true, nil
	default:
		return guest.WIPIReturn{}, false, nil
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

func wipiNetworkAddress(address uint32) string {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], address)
	return net.IP(encoded[:]).String()
}

func (r *Runtime) dispatchHTTP(name string) (guest.WIPIReturn, bool, error) {
	count, ok := httpArgumentCount(name)
	if !ok {
		return guest.WIPIReturn{}, false, nil
	}
	args, err := r.args(count)
	if err != nil {
		return guest.WIPIReturn{}, true, err
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
		url, err := r.ReadCString(arg(0))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		serviceID, serviceErr := r.Services.Network.OpenHTTP(
			r.ServiceOwner,
			string(url),
		)
		if serviceErr != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		descriptor := r.nextHTTP
		r.nextHTTP++
		r.http[descriptor] = &wipiHTTP{
			descriptor: descriptor,
			url:        append([]byte(nil), url...),
			method:     []byte("GET"),
			properties: make(map[string][]byte),
		}
		r.httpServices[descriptor] = serviceID
		return guest.WIPIReturn{Low: uint32(descriptor)}, true, nil
	case "MC_netHttpClose":
		descriptor := int32(arg(0))
		if r.http[descriptor] == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if serviceID := r.httpServices[descriptor]; serviceID != 0 {
			if err := r.Services.Network.CloseHTTP(
				r.ServiceOwner,
				serviceID,
				r.Services.Events,
			); err != nil {
				return guest.WIPIReturn{}, true, err
			}
			delete(r.httpServices, descriptor)
		}
		delete(r.http, descriptor)
		return guest.WIPIReturn{}, true, nil
	case "MC_netHttpConnect":
		current := request()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		current.connected = true
		current.code = 204
		current.response = nil
		serviceID := r.httpServices[current.descriptor]
		if err := r.syncHTTPRequest(current); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if err := r.Services.Network.BeginHTTP(r.ServiceOwner, serviceID); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if err := r.Services.CompleteHTTPResponse(
			r.ServiceOwner,
			serviceID,
			204,
			nil,
			nil,
			r.Services.Clock.Monotonic(),
		); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		r.EnqueueCallback(arg(1), arg(0), ^uint32(0), 0, arg(2))
		return guest.WIPIReturn{}, true, nil
	case "MC_netHttpSetRequestMethod":
		current := request()
		if current == nil || int32(arg(3)) < 0 || arg(3) > maxWIPIString {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		method, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		message := make([]byte, arg(3))
		if err := r.CPU.ReadMemory(arg(2), message); err != nil {
			return guest.WIPIReturn{}, true, err
		}
		current.method = append(current.method[:0], method...)
		current.request = message
		if err := r.syncHTTPRequest(current); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_netHttpGetRequestMethod":
		return r.httpString(request(), arg(1), int32(arg(2)), func(current *wipiHTTP) []byte {
			return current.method
		})
	case "MC_netHttpSetRequestProperty":
		current := request()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		key, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		value, err := r.ReadCString(arg(2))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		current.properties[string(key)] = append([]byte(nil), value...)
		if err := r.syncHTTPRequest(current); err != nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_netHttpGetRequestProperty":
		current := request()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		key, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		value, ok := current.properties[string(key)]
		if !ok {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		count, err := r.writeCString(arg(2), value, int32(arg(3)))
		return guest.WIPIReturn{Low: count}, true, err
	case "MC_netHttpSetProxy":
		current := request()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		current.proxyHost, current.proxyPort = arg(1), uint16(arg(2))
		return guest.WIPIReturn{}, true, nil
	case "MC_netHttpGetProxy":
		current := request()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		if arg(1) != 0 {
			if err := r.WriteU32(arg(1), current.proxyHost); err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		if arg(2) != 0 {
			var encoded [2]byte
			binary.LittleEndian.PutUint16(encoded[:], current.proxyPort)
			if err := r.CPU.WriteMemory(arg(2), encoded[:]); err != nil {
				return guest.WIPIReturn{}, true, err
			}
		}
		return guest.WIPIReturn{}, true, nil
	case "MC_netHttpGetResponseCode":
		if current := request(); current != nil {
			return guest.WIPIReturn{Low: uint32(current.code)}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_netHttpGetResponseMessage":
		return r.httpString(request(), arg(1), int32(arg(2)), func(*wipiHTTP) []byte {
			return []byte("No Content")
		})
	case "MC_netHttpGetHeaderField":
		current := request()
		if current == nil {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		key, err := r.ReadCString(arg(1))
		if err != nil {
			return guest.WIPIReturn{}, true, err
		}
		var value []byte
		if strings.EqualFold(string(key), "Content-Length") {
			value = []byte("0")
		} else {
			return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
		}
		written, err := r.writeCString(arg(2), value, int32(arg(3)))
		return guest.WIPIReturn{Low: written}, true, err
	case "MC_netHttpGetLength":
		if current := request(); current != nil {
			return guest.WIPIReturn{Low: uint32(len(current.response))}, true, nil
		}
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	case "MC_netHttpGetType":
		return r.httpString(request(), arg(1), int32(arg(2)), func(*wipiHTTP) []byte {
			return []byte("application/octet-stream")
		})
	case "MC_netHttpGetEncoding":
		return r.httpString(request(), arg(1), int32(arg(2)), func(*wipiHTTP) []byte {
			return []byte("identity")
		})
	default:
		return guest.WIPIReturn{}, false, nil
	}
}

func (r *Runtime) syncHTTPRequest(current *wipiHTTP) error {
	if current == nil {
		return nil
	}
	names := make([]string, 0, len(current.properties))
	for name := range current.properties {
		names = append(names, name)
	}
	sort.Strings(names)
	properties := make([]shared.HTTPProperty, 0, len(names))
	for _, name := range names {
		properties = append(properties, shared.HTTPProperty{
			Name:  name,
			Value: string(current.properties[name]),
		})
	}
	return r.Services.Network.SetHTTPRequest(
		r.ServiceOwner,
		r.httpServices[current.descriptor],
		string(current.method),
		properties,
		current.request,
	)
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

func (r *Runtime) httpString(
	current *wipiHTTP,
	output uint32,
	length int32,
	value func(*wipiHTTP) []byte,
) (guest.WIPIReturn, bool, error) {
	if current == nil {
		return guest.WIPIReturn{Low: ^uint32(0)}, true, nil
	}
	count, err := r.writeCString(output, value(current), length)
	return guest.WIPIReturn{Low: count}, true, err
}
