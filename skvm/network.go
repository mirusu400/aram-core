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

func (vm *VM) installConnectionNatives() {
	vm.RegisterNative(
		"javax/microedition/io/Connector",
		"open",
		"(Ljava/lang/String;)Ljavax/microedition/io/Connection;",
		func(_ context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			rawURL, err := vm.stringArgument(args, 0)
			if err != nil {
				return Value{}, false, err
			}
			parsed, err := url.Parse(rawURL)
			if err != nil || !strings.EqualFold(parsed.Scheme, "socket") ||
				parsed.User != nil || parsed.Hostname() == "" ||
				parsed.Port() == "" || parsed.Path != "" ||
				parsed.RawQuery != "" || parsed.Fragment != "" {
				return Value{}, false, vm.newThrowable(
					"java/lang/IllegalArgumentException",
					"invalid socket connection URL",
				)
			}
			portValue, err := strconv.ParseUint(parsed.Port(), 10, 16)
			if err != nil || portValue == 0 {
				return Value{}, false, vm.newThrowable(
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
				return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
			}
			return ReferenceValue(vm.NewObject(
				"javax/microedition/io/SocketConnection",
				&socketConnectionState{socket: socket},
			)), true, nil
		},
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
		func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			state, err := vm.socketConnection(receiver)
			if err != nil {
				return Value{}, false, err
			}
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
			return Value{}, false, nil
		},
	)
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

func (vm *VM) refreshSocketInput(state *inputStreamState) error {
	if state == nil || state.connection == 0 || state.closed {
		return nil
	}
	connection, err := vm.openSocketConnection(state.connection)
	if err != nil {
		return err
	}
	info, err := vm.services.Network.SocketInfo(vm.serviceOwner, connection.socket)
	if err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
	}
	if info.ReadBytes == 0 {
		return nil
	}
	data, err := vm.services.Network.SocketRead(
		vm.serviceOwner,
		connection.socket,
		info.ReadBytes,
	)
	if err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
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
		connection, err := vm.openSocketConnection(state.connection)
		if err != nil {
			return err
		}
		if _, err := vm.services.Network.SocketWrite(
			vm.serviceOwner,
			connection.socket,
			data,
		); err != nil {
			return vm.newThrowable("java/io/IOException", err.Error())
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
