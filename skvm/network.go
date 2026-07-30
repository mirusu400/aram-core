package skvm

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	shared "github.com/mirusu400/aram-core/runtime"
)

type socketConnectionState struct {
	socket shared.ServiceID
	closed bool
}

type httpConnectionState struct {
	request shared.ServiceID
	closed  bool
}

func (vm *VM) installConnectionNatives() {
	vm.RegisterNative(
		"javax/microedition/io/Connector",
		"open",
		"(Ljava/lang/String;)Ljavax/microedition/io/Connection;",
		nativeOpenConnection,
	)
	vm.RegisterNative(
		"javax/microedition/io/Connector",
		"open",
		"(Ljava/lang/String;IZ)Ljavax/microedition/io/Connection;",
		nativeOpenConnection,
	)

	vm.RegisterNative(
		"javax/microedition/io/SocketConnection",
		"openDataInputStream",
		"()Ljava/io/DataInputStream;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if _, err := vm.openSocketConnection(receiver); err != nil {
				return Value{}, false, err
			}
			stream := vm.NewObject(
				"java/io/InputStream",
				&inputStreamState{connection: receiver},
			)
			return ReferenceValue(vm.NewObject(
				"java/io/DataInputStream",
				&dataInputState{stream: stream},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/io/SocketConnection",
		"openDataOutputStream",
		"()Ljava/io/DataOutputStream;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if _, err := vm.openSocketConnection(receiver); err != nil {
				return Value{}, false, err
			}
			stream := vm.NewObject(
				"java/io/OutputStream",
				&outputStreamState{connection: receiver},
			)
			return ReferenceValue(vm.NewObject(
				"java/io/DataOutputStream",
				&dataOutputState{stream: stream},
			)), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/io/SocketConnection",
		"close",
		"()V",
		nativeCloseConnection,
	)

	for _, class := range []string{
		"javax/microedition/io/SocketConnection",
		"javax/microedition/io/HttpConnection",
		"org/kwis/msf/io/Socket",
	} {
		vm.RegisterNative(
			class,
			"openInputStream",
			"()Ljava/io/InputStream;",
			nativeOpenConnectionInputStream,
		)
		vm.RegisterNative(
			class,
			"openOutputStream",
			"()Ljava/io/OutputStream;",
			nativeOpenConnectionOutputStream,
		)
		vm.RegisterNative(class, "close", "()V", nativeCloseConnection)
	}
	vm.RegisterNative(
		"org/kwis/msf/io/Socket",
		"getInputStream",
		"()Ljava/io/InputStream;",
		nativeOpenConnectionInputStream,
	)
	vm.RegisterNative(
		"org/kwis/msf/io/Socket",
		"getOutputStream",
		"()Ljava/io/OutputStream;",
		nativeOpenConnectionOutputStream,
	)

	vm.RegisterNative(
		"javax/microedition/io/HttpConnection",
		"setRequestMethod",
		"(Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			method, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			state, request, err := vm.mutableHTTPRequest(receiver)
			if err != nil {
				return Value{}, false, err
			}
			err = vm.services.Network.SetHTTPRequest(
				vm.serviceOwner,
				state.request,
				method,
				request.RequestHeaders,
				request.RequestBody,
			)
			if err != nil {
				return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/io/HttpConnection",
		"setRequestProperty",
		"(Ljava/lang/String;Ljava/lang/String;)V",
		func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			name, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			value, err := vm.stringArgument(args, 1)
			if err != nil {
				return Value{}, false, err
			}
			state, request, err := vm.mutableHTTPRequest(receiver)
			if err != nil {
				return Value{}, false, err
			}
			headers := append([]shared.HTTPProperty(nil), request.RequestHeaders...)
			replaced := false
			for index := range headers {
				if strings.EqualFold(headers[index].Name, name) {
					headers[index] = shared.HTTPProperty{Name: name, Value: value}
					replaced = true
					break
				}
			}
			if !replaced {
				headers = append(headers, shared.HTTPProperty{Name: name, Value: value})
			}
			err = vm.services.Network.SetHTTPRequest(
				vm.serviceOwner,
				state.request,
				request.Method,
				headers,
				request.RequestBody,
			)
			if err != nil {
				return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
			}
			return Value{}, false, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/io/HttpConnection",
		"getLength",
		"()J",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.openHTTPConnection(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.ensureHTTPResponse(state); err != nil {
				return Value{}, false, err
			}
			request, err := vm.httpRequestSnapshot(state.request)
			if err != nil {
				return Value{}, false, err
			}
			return LongValue(int64(len(request.ResponseBody))), true, nil
		},
	)
	vm.RegisterNative(
		"javax/microedition/io/HttpConnection",
		"getType",
		"()Ljava/lang/String;",
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.openHTTPConnection(receiver)
			if err != nil {
				return Value{}, false, err
			}
			if err := vm.ensureHTTPResponse(state); err != nil {
				return Value{}, false, err
			}
			request, err := vm.httpRequestSnapshot(state.request)
			if err != nil {
				return Value{}, false, err
			}
			contentType := ""
			for _, property := range request.ResponseHeaders {
				if strings.EqualFold(property.Name, "Content-Type") {
					contentType = property.Value
					break
				}
			}
			return ReferenceValue(vm.NewString(contentType)), true, nil
		},
	)

	vm.RegisterNative(
		"org/kwis/msf/io/Network",
		"connect",
		"()I",
		func(context.Context, *VM, uint32, []Value) (Value, bool, error) {
			return IntValue(1), true, nil
		},
	)
	vm.RegisterNative(
		"org/kwis/msf/io/Network",
		"disconnect",
		"()V",
		nativeVoid,
	)
	vm.RegisterNative(
		"org/kwis/msf/io/URL",
		"find",
		"(Ljava/lang/String;)Lorg/kwis/msf/io/Socket;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			rawURL, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			if parsed, parseErr := url.Parse(rawURL); parseErr == nil && parsed.Scheme == "" {
				rawURL = "http://" + rawURL
			}
			reference, err := vm.openConnection(rawURL, "org/kwis/msf/io/Socket")
			return ReferenceValue(reference), true, err
		},
	)
}

func nativeOpenConnection(
	_ context.Context,
	vm *VM,
	_ uint32,
	args []Value,
) (Value, bool, error) {
	rawURL, err := vm.stringArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	reference, err := vm.openConnection(rawURL, "")
	return ReferenceValue(reference), true, err
}

func (vm *VM) openConnection(rawURL, objectClass string) (uint32, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid connection URL",
		)
	}
	if strings.EqualFold(parsed.Scheme, "http") ||
		strings.EqualFold(parsed.Scheme, "https") {
		request, openErr := vm.services.Network.OpenHTTP(vm.serviceOwner, rawURL)
		if openErr != nil {
			return 0, vm.newThrowable("java/io/IOException", openErr.Error())
		}
		if objectClass == "" {
			objectClass = "javax/microedition/io/HttpConnection"
		}
		return vm.NewObject(objectClass, &httpConnectionState{request: request}), nil
	}
	if !strings.EqualFold(parsed.Scheme, "socket") ||
		parsed.User != nil || parsed.Hostname() == "" ||
		parsed.Port() == "" || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return 0, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid socket connection URL",
		)
	}
	portValue, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || portValue == 0 {
		return 0, vm.newThrowable(
			"java/lang/IllegalArgumentException",
			"invalid socket port",
		)
	}
	socket, err := vm.services.Network.OpenSocket(vm.serviceOwner, 2, 1)
	if err == nil {
		err = vm.services.Network.ConnectSocket(
			vm.serviceOwner,
			socket,
			parsed.Hostname(),
			uint16(portValue),
		)
	}
	if err == nil {
		err = vm.services.CompleteSocketResponse(
			vm.serviceOwner,
			socket,
			true,
			vm.services.Clock.Monotonic(),
		)
	}
	if err != nil {
		if socket != 0 {
			_ = vm.services.Network.CloseSocket(
				vm.serviceOwner,
				socket,
				vm.services.Events,
			)
		}
		return 0, vm.newThrowable("java/io/IOException", err.Error())
	}
	if objectClass == "" {
		objectClass = "javax/microedition/io/SocketConnection"
	}
	return vm.NewObject(
		objectClass,
		&socketConnectionState{socket: socket},
	), nil
}

func nativeOpenConnectionInputStream(
	_ context.Context,
	vm *VM,
	receiver uint32,
	_ []Value,
) (Value, bool, error) {
	if err := vm.ensureOpenConnection(receiver); err != nil {
		return Value{}, false, err
	}
	return ReferenceValue(vm.NewObject(
		"java/io/InputStream",
		&inputStreamState{connection: receiver},
	)), true, nil
}

func nativeOpenConnectionOutputStream(
	_ context.Context,
	vm *VM,
	receiver uint32,
	_ []Value,
) (Value, bool, error) {
	if err := vm.ensureOpenConnection(receiver); err != nil {
		return Value{}, false, err
	}
	return ReferenceValue(vm.NewObject(
		"java/io/OutputStream",
		&outputStreamState{connection: receiver},
	)), true, nil
}

func nativeCloseConnection(
	_ context.Context,
	vm *VM,
	receiver uint32,
	_ []Value,
) (Value, bool, error) {
	object, ok := vm.Object(receiver)
	if !ok {
		return Value{}, false, fmt.Errorf("invalid connection reference %d", receiver)
	}
	switch state := object.Native.(type) {
	case *socketConnectionState:
		if state.closed {
			return Value{}, false, nil
		}
		if err := vm.services.Network.CloseSocket(
			vm.serviceOwner,
			state.socket,
			vm.services.Events,
		); err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		state.closed = true
		state.socket = 0
	case *httpConnectionState:
		if state.closed {
			return Value{}, false, nil
		}
		if err := vm.services.Network.CloseHTTP(
			vm.serviceOwner,
			state.request,
			vm.services.Events,
		); err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		state.closed = true
		state.request = 0
	default:
		return Value{}, false, fmt.Errorf("object %d is not a connection", receiver)
	}
	return Value{}, false, nil
}

func (vm *VM) socketConnection(reference uint32) (*socketConnectionState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid socket connection reference %d", reference)
	}
	state, ok := object.Native.(*socketConnectionState)
	if !ok {
		return nil, fmt.Errorf("object %d is not a socket connection", reference)
	}
	return state, nil
}

func (vm *VM) openSocketConnection(reference uint32) (*socketConnectionState, error) {
	state, err := vm.socketConnection(reference)
	if err != nil {
		return nil, err
	}
	if state.closed || state.socket == 0 {
		return nil, vm.newThrowable("java/io/IOException", "connection closed")
	}
	return state, nil
}

func (vm *VM) openHTTPConnection(reference uint32) (*httpConnectionState, error) {
	object, ok := vm.Object(reference)
	if !ok {
		return nil, fmt.Errorf("invalid HTTP connection reference %d", reference)
	}
	state, ok := object.Native.(*httpConnectionState)
	if !ok {
		return nil, fmt.Errorf("object %d is not an HTTP connection", reference)
	}
	if state.closed || state.request == 0 {
		return nil, vm.newThrowable("java/io/IOException", "connection closed")
	}
	return state, nil
}

func (vm *VM) ensureOpenConnection(reference uint32) error {
	object, ok := vm.Object(reference)
	if !ok {
		return fmt.Errorf("invalid connection reference %d", reference)
	}
	switch object.Native.(type) {
	case *socketConnectionState:
		_, err := vm.openSocketConnection(reference)
		return err
	case *httpConnectionState:
		_, err := vm.openHTTPConnection(reference)
		return err
	default:
		return fmt.Errorf("object %d is not a connection", reference)
	}
}

func (vm *VM) httpRequestSnapshot(id shared.ServiceID) (shared.HTTPState, error) {
	for _, request := range vm.services.Network.Snapshot().HTTP {
		if request.ID == id && request.Owner == vm.serviceOwner {
			return request, nil
		}
	}
	return shared.HTTPState{}, vm.newThrowable(
		"java/io/IOException",
		"HTTP request is unavailable",
	)
}

func (vm *VM) mutableHTTPRequest(
	reference uint32,
) (*httpConnectionState, shared.HTTPState, error) {
	state, err := vm.openHTTPConnection(reference)
	if err != nil {
		return nil, shared.HTTPState{}, err
	}
	request, err := vm.httpRequestSnapshot(state.request)
	if err != nil {
		return nil, shared.HTTPState{}, err
	}
	if request.State != shared.ConnectionNew {
		return nil, shared.HTTPState{}, vm.newThrowable(
			"java/io/IOException",
			"HTTP request already started",
		)
	}
	return state, request, nil
}

func (vm *VM) ensureHTTPResponse(state *httpConnectionState) error {
	if state == nil || state.closed || state.request == 0 {
		return vm.newThrowable("java/io/IOException", "connection closed")
	}
	info, err := vm.services.Network.HTTPInfo(vm.serviceOwner, state.request)
	if err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
	}
	if info.State == shared.ConnectionConnected {
		return nil
	}
	if info.State == shared.ConnectionNew {
		if err := vm.services.Network.BeginHTTP(
			vm.serviceOwner,
			state.request,
		); err != nil {
			return vm.newThrowable("java/io/IOException", err.Error())
		}
		info.State = shared.ConnectionConnecting
	}
	if info.State != shared.ConnectionConnecting {
		return vm.newThrowable("java/io/IOException", "HTTP request is not available")
	}
	// SKVM is a deterministic headless runtime: absent an attached provider,
	// complete requests with the same empty response used by the WIPI runtime.
	// Replay-aware services record this completion for save/load determinism.
	if err := vm.services.CompleteHTTPResponse(
		vm.serviceOwner,
		state.request,
		204,
		[]shared.HTTPProperty{{Name: "Content-Type", Value: "application/octet-stream"}},
		nil,
		vm.services.Clock.Monotonic(),
	); err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
	}
	return nil
}

func (vm *VM) refreshSocketInput(state *inputStreamState) error {
	if state == nil || state.connection == 0 || state.closed {
		return nil
	}
	object, ok := vm.Object(state.connection)
	if !ok {
		return fmt.Errorf("invalid stream connection reference %d", state.connection)
	}
	var data []byte
	switch connection := object.Native.(type) {
	case *socketConnectionState:
		open, err := vm.openSocketConnection(state.connection)
		if err != nil {
			return err
		}
		info, err := vm.services.Network.SocketInfo(vm.serviceOwner, open.socket)
		if err != nil {
			return vm.newThrowable("java/io/IOException", err.Error())
		}
		if info.ReadBytes == 0 {
			return nil
		}
		data, err = vm.services.Network.SocketRead(
			vm.serviceOwner,
			open.socket,
			info.ReadBytes,
		)
		if err != nil {
			return vm.newThrowable("java/io/IOException", err.Error())
		}
	case *httpConnectionState:
		if _, err := vm.openHTTPConnection(state.connection); err != nil {
			return err
		}
		if err := vm.ensureHTTPResponse(connection); err != nil {
			return err
		}
		request, err := vm.httpRequestSnapshot(connection.request)
		if err != nil {
			return err
		}
		remaining := uint64(len(request.ResponseBody)) - request.ResponseOffset
		if remaining == 0 {
			return nil
		}
		data, err = vm.services.Network.HTTPRead(
			vm.serviceOwner,
			connection.request,
			remaining,
		)
		if err != nil {
			return vm.newThrowable("java/io/IOException", err.Error())
		}
	default:
		return fmt.Errorf("object %d is not a stream connection", state.connection)
	}
	if state.offset != 0 {
		state.data = append([]byte(nil), state.data[state.offset:]...)
		state.offset = 0
	}
	state.data = append(state.data, data...)
	return nil
}

func (vm *VM) writeOutputStream(state *outputStreamState, data []byte) error {
	if state.connection != 0 {
		object, ok := vm.Object(state.connection)
		if !ok {
			return fmt.Errorf("invalid output connection reference %d", state.connection)
		}
		switch connection := object.Native.(type) {
		case *socketConnectionState:
			open, err := vm.openSocketConnection(state.connection)
			if err != nil {
				return err
			}
			if _, err := vm.services.Network.SocketWrite(
				vm.serviceOwner,
				open.socket,
				data,
			); err != nil {
				return vm.newThrowable("java/io/IOException", err.Error())
			}
		case *httpConnectionState:
			if _, err := vm.openHTTPConnection(state.connection); err != nil {
				return err
			}
			request, err := vm.httpRequestSnapshot(connection.request)
			if err != nil {
				return err
			}
			if request.State != shared.ConnectionNew {
				return vm.newThrowable(
					"java/io/IOException",
					"HTTP request already started",
				)
			}
			body := append(append([]byte(nil), request.RequestBody...), data...)
			if err := vm.services.Network.SetHTTPRequest(
				vm.serviceOwner,
				connection.request,
				request.Method,
				request.RequestHeaders,
				body,
			); err != nil {
				return vm.newThrowable("java/io/IOException", err.Error())
			}
		default:
			return fmt.Errorf("object %d is not an output connection", state.connection)
		}
		return nil
	}
	if state.file != nil {
		end := state.file.offset + len(data)
		if end > len(state.file.data) {
			state.file.data = append(
				state.file.data,
				make([]byte, end-len(state.file.data))...,
			)
		}
		copy(state.file.data[state.file.offset:end], data)
		state.file.offset = end
		return vm.persistXFile(state.file)
	}
	state.data = append(state.data, data...)
	if state.name != "" {
		return vm.services.Storage.WriteFile(
			shared.NamespacePrivate,
			state.name,
			state.data,
		)
	}
	return nil
}
