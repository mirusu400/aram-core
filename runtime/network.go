package runtime

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

type NetworkLimits struct {
	MaxSockets      uint32
	MaxHTTPRequests uint32
	MaxSerialPorts  uint32
	MaxBufferBytes  uint64
	MaxTotalBytes   uint64
	MaxProperties   uint32
	MaxNameBytes    uint32
}

func DefaultNetworkLimits() NetworkLimits {
	return NetworkLimits{
		MaxSockets:      128,
		MaxHTTPRequests: 128,
		MaxSerialPorts:  32,
		MaxBufferBytes:  8 << 20,
		MaxTotalBytes:   64 << 20,
		MaxProperties:   128,
		MaxNameBytes:    4096,
	}
}

func (l NetworkLimits) Validate() error {
	if l.MaxSockets == 0 || l.MaxHTTPRequests == 0 ||
		l.MaxSerialPorts == 0 || l.MaxBufferBytes == 0 ||
		l.MaxTotalBytes == 0 || l.MaxProperties == 0 ||
		l.MaxNameBytes == 0 || l.MaxBufferBytes > l.MaxTotalBytes {
		return fmt.Errorf("%w: invalid network limits", ErrInvalidArgument)
	}
	return nil
}

type ConnectionState uint8

const (
	ConnectionNew ConnectionState = iota
	ConnectionBound
	ConnectionConnecting
	ConnectionConnected
	ConnectionClosed
)

func (s ConnectionState) Valid() bool {
	return s <= ConnectionClosed
}

type SocketInfo struct {
	ID         ServiceID
	Owner      OwnerID
	Domain     int32
	Type       int32
	State      ConnectionState
	Host       string
	Address    uint32
	Port       uint16
	ReadBytes  uint64
	WriteBytes uint64
}

type SocketState struct {
	ID        ServiceID
	Owner     OwnerID
	Domain    int32
	Type      int32
	State     ConnectionState
	Host      string
	Address   uint32
	Port      uint16
	ReadData  []byte
	WriteData []byte
}

type HTTPProperty struct {
	Name  string
	Value string
}

type HTTPInfo struct {
	ID            ServiceID
	Owner         OwnerID
	URL           string
	Method        string
	State         ConnectionState
	ResponseCode  int32
	RequestBytes  uint64
	ResponseBytes uint64
}

type HTTPState struct {
	ID              ServiceID
	Owner           OwnerID
	URL             string
	Method          string
	State           ConnectionState
	RequestHeaders  []HTTPProperty
	RequestBody     []byte
	ResponseCode    int32
	ResponseHeaders []HTTPProperty
	ResponseBody    []byte
	ResponseOffset  uint64
}

type SerialState struct {
	ID        ServiceID
	Owner     OwnerID
	Port      int32
	State     ConnectionState
	ReadData  []byte
	WriteData []byte
}

type NetworkState struct {
	Limits  NetworkLimits
	Sockets []SocketState
	HTTP    []HTTPState
	Serial  []SerialState
}

type modeledSocket struct {
	id         ServiceID
	owner      OwnerID
	domain     int32
	socketType int32
	state      ConnectionState
	host       string
	address    uint32
	port       uint16
	readData   []byte
	writeData  []byte
}

type modeledHTTP struct {
	id              ServiceID
	owner           OwnerID
	url             string
	method          string
	state           ConnectionState
	requestHeaders  map[string]string
	requestBody     []byte
	responseCode    int32
	responseHeaders map[string]string
	responseBody    []byte
	responseOffset  uint64
}

type modeledSerial struct {
	id        ServiceID
	owner     OwnerID
	port      int32
	state     ConnectionState
	readData  []byte
	writeData []byte
}

// Network models sockets, HTTP, and serial descriptors without implicit host
// I/O. A provider or replay driver supplies responses through Complete/Inject.
type Network struct {
	registry *Registry
	limits   NetworkLimits
	sockets  map[ServiceID]*modeledSocket
	http     map[ServiceID]*modeledHTTP
	serial   map[ServiceID]*modeledSerial
}

func NewNetwork(registry *Registry, limits NetworkLimits) (*Network, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: network registry is nil", ErrInvalidArgument)
	}
	if limits == (NetworkLimits{}) {
		limits = DefaultNetworkLimits()
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Network{
		registry: registry,
		limits:   limits,
		sockets:  make(map[ServiceID]*modeledSocket),
		http:     make(map[ServiceID]*modeledHTTP),
		serial:   make(map[ServiceID]*modeledSerial),
	}, nil
}

func (n *Network) OpenSocket(owner OwnerID, domain, socketType int32) (ServiceID, error) {
	if uint32(len(n.sockets)) >= n.limits.MaxSockets {
		return 0, fmt.Errorf("%w: socket count reached %d", ErrLimitExceeded, n.limits.MaxSockets)
	}
	id, err := n.registry.Create(owner, KindSocket)
	if err != nil {
		return 0, err
	}
	n.sockets[id] = &modeledSocket{
		id: id, owner: owner, domain: domain, socketType: socketType,
	}
	return id, nil
}

func (n *Network) BindSocket(
	owner OwnerID,
	id ServiceID,
	host string,
	port uint16,
) error {
	socket, err := n.socket(owner, id)
	if err != nil {
		return err
	}
	if socket.state != ConnectionNew {
		return fmt.Errorf("%w: socket cannot bind from state %d", ErrInvalidState, socket.state)
	}
	address, normalized, err := deterministicAddress(host, n.limits.MaxNameBytes)
	if err != nil {
		return err
	}
	socket.host, socket.address, socket.port = normalized, address, port
	socket.state = ConnectionBound
	return nil
}

func (n *Network) ConnectSocket(
	owner OwnerID,
	id ServiceID,
	host string,
	port uint16,
) error {
	socket, err := n.socket(owner, id)
	if err != nil {
		return err
	}
	if socket.state != ConnectionNew && socket.state != ConnectionBound {
		return fmt.Errorf("%w: socket cannot connect from state %d", ErrInvalidState, socket.state)
	}
	address, normalized, err := deterministicAddress(host, n.limits.MaxNameBytes)
	if err != nil {
		return err
	}
	socket.host, socket.address, socket.port = normalized, address, port
	socket.state = ConnectionConnecting
	return nil
}

func (n *Network) CompleteSocketConnect(
	owner OwnerID,
	id ServiceID,
	success bool,
	at int64,
	bus *EventBus,
) error {
	socket, err := n.socket(owner, id)
	if err != nil {
		return err
	}
	if socket.state != ConnectionConnecting || at < 0 || bus == nil {
		return fmt.Errorf("%w: invalid socket completion", ErrInvalidState)
	}
	previous := socket.state
	if success {
		socket.state = ConnectionConnected
	} else {
		socket.state = ConnectionClosed
	}
	if _, err := bus.Enqueue(Event{
		At:        durationFromInt64(at),
		Kind:      EventNetworkReady,
		Owner:     owner,
		ServiceID: id,
		Name:      "connect",
		Value:     boolValue(success),
	}); err != nil {
		socket.state = previous
		return err
	}
	return nil
}

func (n *Network) SocketWrite(owner OwnerID, id ServiceID, data []byte) (int, error) {
	socket, err := n.socket(owner, id)
	if err != nil {
		return 0, err
	}
	if socket.state != ConnectionConnected {
		return 0, fmt.Errorf("%w: socket is not connected", ErrInvalidState)
	}
	if err := n.checkAppend(uint64(len(socket.writeData)), uint64(len(data))); err != nil {
		return 0, err
	}
	socket.writeData = append(socket.writeData, data...)
	return len(data), nil
}

func (n *Network) SocketWritten(owner OwnerID, id ServiceID) ([]byte, error) {
	socket, err := n.socket(owner, id)
	if err != nil {
		return nil, err
	}
	return cloneBytes(socket.writeData), nil
}

func (n *Network) InjectSocketRead(
	owner OwnerID,
	id ServiceID,
	data []byte,
	at int64,
	bus *EventBus,
) error {
	socket, err := n.socket(owner, id)
	if err != nil {
		return err
	}
	if socket.state != ConnectionConnected || at < 0 || bus == nil {
		return fmt.Errorf("%w: invalid socket response", ErrInvalidState)
	}
	if err := n.checkAppend(uint64(len(socket.readData)), uint64(len(data))); err != nil {
		return err
	}
	oldLength := len(socket.readData)
	socket.readData = append(socket.readData, data...)
	if _, err := bus.Enqueue(Event{
		At:        durationFromInt64(at),
		Kind:      EventNetworkReady,
		Owner:     owner,
		ServiceID: id,
		Name:      "read",
		Value:     int64(len(data)),
	}); err != nil {
		socket.readData = socket.readData[:oldLength]
		return err
	}
	return nil
}

func (n *Network) SocketRead(owner OwnerID, id ServiceID, size uint64) ([]byte, error) {
	socket, err := n.socket(owner, id)
	if err != nil {
		return nil, err
	}
	count := min(size, uint64(len(socket.readData)))
	if count > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: socket read exceeds host limits", ErrLimitExceeded)
	}
	result := cloneBytes(socket.readData[:count])
	copy(socket.readData, socket.readData[count:])
	socket.readData = socket.readData[:uint64(len(socket.readData))-count]
	return result, nil
}

func (n *Network) SocketInfo(owner OwnerID, id ServiceID) (SocketInfo, error) {
	socket, err := n.socket(owner, id)
	if err != nil {
		return SocketInfo{}, err
	}
	return SocketInfo{
		ID: id, Owner: owner, Domain: socket.domain, Type: socket.socketType,
		State: socket.state, Host: socket.host, Address: socket.address,
		Port: socket.port, ReadBytes: uint64(len(socket.readData)),
		WriteBytes: uint64(len(socket.writeData)),
	}, nil
}

func (n *Network) CloseSocket(owner OwnerID, id ServiceID, bus *EventBus) error {
	if _, err := n.socket(owner, id); err != nil {
		return err
	}
	if err := n.registry.Destroy(id, owner, KindSocket); err != nil {
		return err
	}
	delete(n.sockets, id)
	if bus != nil {
		bus.RemoveService(id)
	}
	return nil
}

func (n *Network) OpenHTTP(owner OwnerID, rawURL string) (ServiceID, error) {
	if uint32(len(n.http)) >= n.limits.MaxHTTPRequests {
		return 0, fmt.Errorf(
			"%w: HTTP request count reached %d",
			ErrLimitExceeded,
			n.limits.MaxHTTPRequests,
		)
	}
	normalized, err := normalizeHTTPURL(rawURL, n.limits.MaxNameBytes)
	if err != nil {
		return 0, err
	}
	id, err := n.registry.Create(owner, KindHTTP)
	if err != nil {
		return 0, err
	}
	n.http[id] = &modeledHTTP{
		id: id, owner: owner, url: normalized, method: "GET",
		requestHeaders:  make(map[string]string),
		responseHeaders: make(map[string]string),
	}
	return id, nil
}

func (n *Network) SetHTTPRequest(
	owner OwnerID,
	id ServiceID,
	method string,
	headers []HTTPProperty,
	body []byte,
) error {
	request, err := n.request(owner, id)
	if err != nil {
		return err
	}
	method, err = normalizeHTTPMethod(method)
	if err != nil || request.state != ConnectionNew ||
		len(headers) > int(n.limits.MaxProperties) ||
		uint64(len(body)) > n.limits.MaxBufferBytes {
		return fmt.Errorf("%w: invalid HTTP request data", ErrInvalidArgument)
	}
	properties, err := validateHTTPProperties(headers, n.limits)
	if err != nil {
		return err
	}
	total := n.totalBytes()
	oldSize, newSize := uint64(len(request.requestBody)), uint64(len(body))
	if total < oldSize || newSize > n.limits.MaxTotalBytes ||
		total-oldSize > n.limits.MaxTotalBytes-newSize {
		return fmt.Errorf("%w: network total byte quota", ErrLimitExceeded)
	}
	request.method = method
	request.requestHeaders = properties
	request.requestBody = cloneBytes(body)
	return nil
}

func (n *Network) BeginHTTP(owner OwnerID, id ServiceID) error {
	request, err := n.request(owner, id)
	if err != nil {
		return err
	}
	if request.state != ConnectionNew {
		return fmt.Errorf("%w: HTTP request already started", ErrInvalidState)
	}
	request.state = ConnectionConnecting
	return nil
}

func (n *Network) CompleteHTTP(
	owner OwnerID,
	id ServiceID,
	code int32,
	headers []HTTPProperty,
	body []byte,
	at int64,
	bus *EventBus,
) error {
	request, err := n.request(owner, id)
	if err != nil {
		return err
	}
	if request.state != ConnectionConnecting || code < 100 || code > 999 ||
		uint64(len(body)) > n.limits.MaxBufferBytes || at < 0 || bus == nil {
		return fmt.Errorf("%w: invalid HTTP response", ErrInvalidArgument)
	}
	properties, err := validateHTTPProperties(headers, n.limits)
	if err != nil {
		return err
	}
	total := n.totalBytes()
	oldSize, newSize := uint64(len(request.responseBody)), uint64(len(body))
	if total < oldSize || newSize > n.limits.MaxTotalBytes ||
		total-oldSize > n.limits.MaxTotalBytes-newSize {
		return fmt.Errorf("%w: network total byte quota", ErrLimitExceeded)
	}
	oldState, oldCode := request.state, request.responseCode
	oldHeaders, oldBody, oldOffset := request.responseHeaders,
		request.responseBody, request.responseOffset
	request.state = ConnectionConnected
	request.responseCode = code
	request.responseHeaders = properties
	request.responseBody = cloneBytes(body)
	request.responseOffset = 0
	if _, err := bus.Enqueue(Event{
		At:        durationFromInt64(at),
		Kind:      EventNetworkReady,
		Owner:     owner,
		ServiceID: id,
		Name:      "http",
		Value:     int64(code),
	}); err != nil {
		request.state, request.responseCode = oldState, oldCode
		request.responseHeaders, request.responseBody = oldHeaders, oldBody
		request.responseOffset = oldOffset
		return err
	}
	return nil
}

func (n *Network) HTTPRead(owner OwnerID, id ServiceID, size uint64) ([]byte, error) {
	request, err := n.request(owner, id)
	if err != nil {
		return nil, err
	}
	if request.state != ConnectionConnected {
		return nil, fmt.Errorf("%w: HTTP response is not ready", ErrInvalidState)
	}
	remaining := uint64(len(request.responseBody)) - request.responseOffset
	count := min(size, remaining)
	if count > uint64(math.MaxInt) {
		return nil, fmt.Errorf("%w: HTTP read exceeds host limits", ErrLimitExceeded)
	}
	result := cloneBytes(
		request.responseBody[request.responseOffset : request.responseOffset+count],
	)
	request.responseOffset += count
	return result, nil
}

func (n *Network) HTTPInfo(owner OwnerID, id ServiceID) (HTTPInfo, error) {
	request, err := n.request(owner, id)
	if err != nil {
		return HTTPInfo{}, err
	}
	return HTTPInfo{
		ID: id, Owner: owner, URL: request.url, Method: request.method,
		State: request.state, ResponseCode: request.responseCode,
		RequestBytes:  uint64(len(request.requestBody)),
		ResponseBytes: uint64(len(request.responseBody)),
	}, nil
}

func (n *Network) CloseHTTP(owner OwnerID, id ServiceID, bus *EventBus) error {
	if _, err := n.request(owner, id); err != nil {
		return err
	}
	if err := n.registry.Destroy(id, owner, KindHTTP); err != nil {
		return err
	}
	delete(n.http, id)
	if bus != nil {
		bus.RemoveService(id)
	}
	return nil
}

func (n *Network) OpenSerial(owner OwnerID, port int32) (ServiceID, error) {
	if port < 0 || uint32(len(n.serial)) >= n.limits.MaxSerialPorts {
		return 0, fmt.Errorf("%w: invalid or excessive serial port", ErrLimitExceeded)
	}
	id, err := n.registry.Create(owner, KindSerial)
	if err != nil {
		return 0, err
	}
	n.serial[id] = &modeledSerial{
		id: id, owner: owner, port: port, state: ConnectionConnected,
	}
	return id, nil
}

func (n *Network) SerialWrite(owner OwnerID, id ServiceID, data []byte) (int, error) {
	port, err := n.serialPort(owner, id)
	if err != nil {
		return 0, err
	}
	if err := n.checkAppend(uint64(len(port.writeData)), uint64(len(data))); err != nil {
		return 0, err
	}
	port.writeData = append(port.writeData, data...)
	return len(data), nil
}

func (n *Network) InjectSerialRead(
	owner OwnerID,
	id ServiceID,
	data []byte,
	at int64,
	bus *EventBus,
) error {
	port, err := n.serialPort(owner, id)
	if err != nil {
		return err
	}
	if at < 0 || bus == nil {
		return fmt.Errorf("%w: invalid serial response", ErrInvalidArgument)
	}
	if err := n.checkAppend(uint64(len(port.readData)), uint64(len(data))); err != nil {
		return err
	}
	oldLength := len(port.readData)
	port.readData = append(port.readData, data...)
	if _, err := bus.Enqueue(Event{
		At:        durationFromInt64(at),
		Kind:      EventSerialComplete,
		Owner:     owner,
		ServiceID: id,
		Name:      "read",
		Value:     int64(len(data)),
	}); err != nil {
		port.readData = port.readData[:oldLength]
		return err
	}
	return nil
}

func (n *Network) SerialRead(owner OwnerID, id ServiceID, size uint64) ([]byte, error) {
	port, err := n.serialPort(owner, id)
	if err != nil {
		return nil, err
	}
	count := min(size, uint64(len(port.readData)))
	result := cloneBytes(port.readData[:count])
	copy(port.readData, port.readData[count:])
	port.readData = port.readData[:uint64(len(port.readData))-count]
	return result, nil
}

func (n *Network) CloseSerial(owner OwnerID, id ServiceID, bus *EventBus) error {
	if _, err := n.serialPort(owner, id); err != nil {
		return err
	}
	if err := n.registry.Destroy(id, owner, KindSerial); err != nil {
		return err
	}
	delete(n.serial, id)
	if bus != nil {
		bus.RemoveService(id)
	}
	return nil
}

func (n *Network) Snapshot() NetworkState {
	state := NetworkState{Limits: n.limits}
	for _, id := range sortedIDs(n.sockets) {
		socket := n.sockets[id]
		state.Sockets = append(state.Sockets, SocketState{
			ID: socket.id, Owner: socket.owner, Domain: socket.domain,
			Type: socket.socketType, State: socket.state, Host: socket.host,
			Address: socket.address, Port: socket.port,
			ReadData: cloneBytes(socket.readData), WriteData: cloneBytes(socket.writeData),
		})
	}
	for _, id := range sortedIDs(n.http) {
		request := n.http[id]
		state.HTTP = append(state.HTTP, HTTPState{
			ID: id, Owner: request.owner, URL: request.url, Method: request.method,
			State:           request.state,
			RequestHeaders:  sortedProperties(request.requestHeaders),
			RequestBody:     cloneBytes(request.requestBody),
			ResponseCode:    request.responseCode,
			ResponseHeaders: sortedProperties(request.responseHeaders),
			ResponseBody:    cloneBytes(request.responseBody),
			ResponseOffset:  request.responseOffset,
		})
	}
	for _, id := range sortedIDs(n.serial) {
		port := n.serial[id]
		state.Serial = append(state.Serial, SerialState{
			ID: id, Owner: port.owner, Port: port.port, State: port.state,
			ReadData: cloneBytes(port.readData), WriteData: cloneBytes(port.writeData),
		})
	}
	return state
}

func (n *Network) Restore(state NetworkState) error {
	if err := state.Limits.Validate(); err != nil ||
		uint64(len(state.Sockets)) > uint64(state.Limits.MaxSockets) ||
		uint64(len(state.HTTP)) > uint64(state.Limits.MaxHTTPRequests) ||
		uint64(len(state.Serial)) > uint64(state.Limits.MaxSerialPorts) {
		return fmt.Errorf("%w: invalid network state limits", ErrInvalidState)
	}
	candidate, err := NewNetwork(n.registry, state.Limits)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	var previous ServiceID
	for index, saved := range state.Sockets {
		address, normalizedHost, addressErr :=
			deterministicAddress(saved.Host, state.Limits.MaxNameBytes)
		newState := saved.State == ConnectionNew
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			!saved.State.Valid() ||
			(newState &&
				(saved.Host != "" || saved.Address != 0 || saved.Port != 0)) ||
			(!newState &&
				(addressErr != nil || saved.Host != normalizedHost ||
					saved.Address != address)) ||
			(saved.State != ConnectionConnected &&
				(len(saved.ReadData) != 0 || len(saved.WriteData) != 0)) ||
			uint64(len(saved.ReadData)) > state.Limits.MaxBufferBytes ||
			uint64(len(saved.WriteData)) > state.Limits.MaxBufferBytes ||
			n.registry.Validate(saved.ID, saved.Owner, KindSocket) != nil {
			return fmt.Errorf("%w: invalid socket state %d", ErrInvalidState, index)
		}
		candidate.sockets[saved.ID] = &modeledSocket{
			id: saved.ID, owner: saved.Owner, domain: saved.Domain,
			socketType: saved.Type, state: saved.State, host: saved.Host,
			address: saved.Address, port: saved.Port,
			readData: cloneBytes(saved.ReadData), writeData: cloneBytes(saved.WriteData),
		}
		previous = saved.ID
	}
	previous = 0
	for index, saved := range state.HTTP {
		normalizedURL, urlErr := normalizeHTTPURL(
			saved.URL,
			state.Limits.MaxNameBytes,
		)
		normalizedMethod, methodErr := normalizeHTTPMethod(saved.Method)
		responseReady := saved.State == ConnectionConnected
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			(saved.State != ConnectionNew &&
				saved.State != ConnectionConnecting &&
				saved.State != ConnectionConnected) ||
			urlErr != nil || normalizedURL != saved.URL ||
			methodErr != nil || normalizedMethod != saved.Method ||
			(responseReady &&
				(saved.ResponseCode < 100 || saved.ResponseCode > 999)) ||
			(!responseReady &&
				(saved.ResponseCode != 0 ||
					len(saved.ResponseHeaders) != 0 ||
					len(saved.ResponseBody) != 0 ||
					saved.ResponseOffset != 0)) ||
			uint64(len(saved.RequestBody)) > state.Limits.MaxBufferBytes ||
			uint64(len(saved.ResponseBody)) > state.Limits.MaxBufferBytes ||
			saved.ResponseOffset > uint64(len(saved.ResponseBody)) ||
			n.registry.Validate(saved.ID, saved.Owner, KindHTTP) != nil {
			return fmt.Errorf("%w: invalid HTTP state %d", ErrInvalidState, index)
		}
		requestHeaders, propertyErr := validateCanonicalHTTPProperties(
			saved.RequestHeaders,
			state.Limits,
		)
		if propertyErr != nil {
			return fmt.Errorf("%w: invalid HTTP request properties %d", ErrInvalidState, index)
		}
		responseHeaders, propertyErr := validateCanonicalHTTPProperties(
			saved.ResponseHeaders,
			state.Limits,
		)
		if propertyErr != nil {
			return fmt.Errorf("%w: invalid HTTP response properties %d", ErrInvalidState, index)
		}
		candidate.http[saved.ID] = &modeledHTTP{
			id: saved.ID, owner: saved.Owner, url: saved.URL,
			method: saved.Method, state: saved.State,
			requestHeaders: requestHeaders, requestBody: cloneBytes(saved.RequestBody),
			responseCode: saved.ResponseCode, responseHeaders: responseHeaders,
			responseBody:   cloneBytes(saved.ResponseBody),
			responseOffset: saved.ResponseOffset,
		}
		previous = saved.ID
	}
	previous = 0
	for index, saved := range state.Serial {
		if !saved.ID.Valid() || (index != 0 && saved.ID <= previous) ||
			saved.Port < 0 || saved.State != ConnectionConnected ||
			uint64(len(saved.ReadData)) > state.Limits.MaxBufferBytes ||
			uint64(len(saved.WriteData)) > state.Limits.MaxBufferBytes ||
			n.registry.Validate(saved.ID, saved.Owner, KindSerial) != nil {
			return fmt.Errorf("%w: invalid serial state %d", ErrInvalidState, index)
		}
		candidate.serial[saved.ID] = &modeledSerial{
			id: saved.ID, owner: saved.Owner, port: saved.Port,
			state: saved.State, readData: cloneBytes(saved.ReadData),
			writeData: cloneBytes(saved.WriteData),
		}
		previous = saved.ID
	}
	if candidate.totalBytes() > state.Limits.MaxTotalBytes {
		return fmt.Errorf("%w: saved network buffers exceed total quota", ErrInvalidState)
	}
	*n = *candidate
	return nil
}

func (n *Network) socket(owner OwnerID, id ServiceID) (*modeledSocket, error) {
	if err := n.registry.Validate(id, owner, KindSocket); err != nil {
		return nil, err
	}
	socket := n.sockets[id]
	if socket == nil {
		return nil, fmt.Errorf("%w: socket %s", ErrInvalidState, id)
	}
	return socket, nil
}

func (n *Network) request(owner OwnerID, id ServiceID) (*modeledHTTP, error) {
	if err := n.registry.Validate(id, owner, KindHTTP); err != nil {
		return nil, err
	}
	request := n.http[id]
	if request == nil {
		return nil, fmt.Errorf("%w: HTTP request %s", ErrInvalidState, id)
	}
	return request, nil
}

func (n *Network) serialPort(owner OwnerID, id ServiceID) (*modeledSerial, error) {
	if err := n.registry.Validate(id, owner, KindSerial); err != nil {
		return nil, err
	}
	port := n.serial[id]
	if port == nil {
		return nil, fmt.Errorf("%w: serial port %s", ErrInvalidState, id)
	}
	return port, nil
}

func (n *Network) checkAppend(current, added uint64) error {
	if added > n.limits.MaxBufferBytes ||
		current > n.limits.MaxBufferBytes-added ||
		added > n.limits.MaxTotalBytes ||
		n.totalBytes() > n.limits.MaxTotalBytes-added {
		return fmt.Errorf("%w: network buffer quota", ErrLimitExceeded)
	}
	return nil
}

func (n *Network) totalBytes() uint64 {
	var total uint64
	add := func(size int) {
		value := uint64(size)
		if total > ^uint64(0)-value {
			total = ^uint64(0)
			return
		}
		total += value
	}
	for _, socket := range n.sockets {
		add(len(socket.readData))
		add(len(socket.writeData))
	}
	for _, request := range n.http {
		add(len(request.requestBody))
		add(len(request.responseBody))
	}
	for _, port := range n.serial {
		add(len(port.readData))
		add(len(port.writeData))
	}
	return total
}

func deterministicAddress(host string, maxName uint32) (uint32, string, error) {
	host = strings.TrimSpace(host)
	if host == "" || uint64(len(host)) > uint64(maxName) ||
		strings.IndexByte(host, 0) >= 0 {
		return 0, "", fmt.Errorf("%w: invalid network host", ErrInvalidArgument)
	}
	if strings.EqualFold(host, "localhost") {
		return 0x7f000001, "localhost", nil
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return 0, "", fmt.Errorf("%w: host requires an explicit provider response", ErrNotFound)
	}
	return binary.BigEndian.Uint32(ip), ip.String(), nil
}

func normalizeHTTPURL(rawURL string, maxName uint32) (string, error) {
	if rawURL == "" || uint64(len(rawURL)) > uint64(maxName) ||
		strings.IndexByte(rawURL, 0) >= 0 {
		return "", fmt.Errorf("%w: invalid HTTP URL", ErrInvalidArgument)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%w: invalid HTTP URL", ErrInvalidArgument)
	}
	normalized := parsed.String()
	if uint64(len(normalized)) > uint64(maxName) {
		return "", fmt.Errorf("%w: HTTP URL exceeds limit", ErrLimitExceeded)
	}
	return normalized, nil
}

func normalizeHTTPMethod(method string) (string, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || len(method) > 32 {
		return "", fmt.Errorf("%w: invalid HTTP method", ErrInvalidArgument)
	}
	for index := range len(method) {
		current := method[index]
		if current >= 'A' && current <= 'Z' ||
			current >= '0' && current <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(current)) {
			continue
		}
		return "", fmt.Errorf("%w: invalid HTTP method", ErrInvalidArgument)
	}
	return method, nil
}

func validateHTTPProperties(
	properties []HTTPProperty,
	limits NetworkLimits,
) (map[string]string, error) {
	if uint64(len(properties)) > uint64(limits.MaxProperties) {
		return nil, fmt.Errorf("%w: too many HTTP properties", ErrLimitExceeded)
	}
	result := make(map[string]string, len(properties))
	for index, property := range properties {
		name := strings.ToLower(strings.TrimSpace(property.Name))
		if name == "" || uint64(len(name)) > uint64(limits.MaxNameBytes) ||
			uint64(len(property.Value)) > uint64(limits.MaxNameBytes) ||
			strings.IndexByte(name, 0) >= 0 ||
			strings.IndexByte(property.Value, 0) >= 0 {
			return nil, fmt.Errorf("%w: invalid HTTP property %d", ErrInvalidArgument, index)
		}
		if _, duplicate := result[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate HTTP property %q", ErrInvalidArgument, name)
		}
		result[name] = property.Value
	}
	return result, nil
}

func validateCanonicalHTTPProperties(
	properties []HTTPProperty,
	limits NetworkLimits,
) (map[string]string, error) {
	result, err := validateHTTPProperties(properties, limits)
	if err != nil {
		return nil, err
	}
	previous := ""
	for index, property := range properties {
		canonical := strings.ToLower(strings.TrimSpace(property.Name))
		if property.Name != canonical ||
			(index != 0 && property.Name <= previous) {
			return nil, fmt.Errorf(
				"%w: non-canonical HTTP property %d",
				ErrInvalidState,
				index,
			)
		}
		previous = property.Name
	}
	return result, nil
}

func sortedProperties(properties map[string]string) []HTTPProperty {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]HTTPProperty, 0, len(names))
	for _, name := range names {
		result = append(result, HTTPProperty{Name: name, Value: properties[name]})
	}
	return result
}

func sortedIDs[T any](values map[ServiceID]T) []ServiceID {
	ids := make([]ServiceID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func durationFromInt64(value int64) time.Duration {
	return time.Duration(value)
}

func boolValue(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
