package skvm

import (
	"context"
	"fmt"
	"strings"
)

const (
	datagramDataField       = "\x00aram-datagram-data"
	datagramOffsetField     = "\x00aram-datagram-offset"
	datagramLengthField     = "\x00aram-datagram-length"
	datagramAddressField    = "\x00aram-datagram-address"
	datagramMaxLength       = int32(65507)
	serverSocketPortField   = "\x00aram-server-port"
	serverSocketClosedField = "\x00aram-server-closed"
)

func (vm *VM) installCLDCConnectionExtras() {
	for _, method := range []struct {
		name       string
		descriptor string
		data       bool
		output     bool
	}{{"openInputStream", "(Ljava/lang/String;)Ljava/io/InputStream;", false, false}, {"openOutputStream", "(Ljava/lang/String;)Ljava/io/OutputStream;", false, true}, {"openDataInputStream", "(Ljava/lang/String;)Ljava/io/DataInputStream;", true, false}, {"openDataOutputStream", "(Ljava/lang/String;)Ljava/io/DataOutputStream;", true, true}} {
		method := method
		vm.RegisterNative("javax/microedition/io/Connector", method.name, method.descriptor, func(ctx context.Context, vm *VM, _ uint32, args []Value) (Value, bool, error) {
			connectionValue, _, err := nativeOpenConnection(ctx, vm, 0, args)
			if err != nil {
				return Value{}, false, err
			}
			connection, _ := connectionValue.Reference()
			var result Value
			if method.output {
				result, _, err = nativeOpenConnectionOutputStream(ctx, vm, connection, nil)
			} else {
				result, _, err = nativeOpenConnectionInputStream(ctx, vm, connection, nil)
			}
			if err != nil || !method.data {
				return result, err == nil, err
			}
			stream, _ := result.Reference()
			if method.output {
				return ReferenceValue(vm.NewObject("java/io/DataOutputStream", &dataOutputState{stream: stream})), true, nil
			}
			return ReferenceValue(vm.NewObject("java/io/DataInputStream", &dataInputState{stream: stream})), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/HttpConnection", "getEncoding", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.openHTTPConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		if err = vm.ensureHTTPResponse(state); err != nil {
			return Value{}, false, err
		}
		request, err := vm.httpRequestSnapshot(state.request)
		if err != nil {
			return Value{}, false, err
		}
		for _, property := range request.ResponseHeaders {
			if strings.EqualFold(property.Name, "Content-Encoding") {
				return ReferenceValue(vm.NewString(property.Value)), true, nil
			}
		}
		return ReferenceValue(0), true, nil
	})
	vm.installCLDCDatagramNatives()
	vm.installCLDCServerSocketNatives()
}

func (vm *VM) newServerSocketNotifier(port uint16) uint32 {
	reference := vm.NewObject("javax/microedition/io/StreamConnectionNotifier", nil)
	object, _ := vm.Object(reference)
	object.Fields[serverSocketPortField] = IntValue(int32(port))
	object.Fields[serverSocketClosedField] = IntValue(0)
	return reference
}

func (vm *VM) installCLDCServerSocketNatives() {
	vm.RegisterNative("javax/microedition/io/StreamConnectionNotifier", "acceptAndOpen", "()Ljavax/microedition/io/StreamConnection;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok || object.Class != "javax/microedition/io/StreamConnectionNotifier" {
			return Value{}, false, fmt.Errorf("invalid StreamConnectionNotifier")
		}
		closed, _ := object.Fields[serverSocketClosedField].Int()
		if closed != 0 {
			return Value{}, false, vm.newThrowable("java/io/IOException", "notifier closed")
		}
		port, err := object.Fields[serverSocketPortField].Int()
		if err != nil || port <= 0 || port > 65535 {
			return Value{}, false, fmt.Errorf("invalid server socket port")
		}
		socket, err := vm.services.Network.OpenSocket(vm.serviceOwner, 2, 1)
		if err == nil {
			err = vm.services.Network.ConnectSocket(vm.serviceOwner, socket, "127.0.0.1", uint16(port))
		}
		if err == nil {
			err = vm.services.CompleteSocketResponse(vm.serviceOwner, socket, true, vm.services.Clock.Monotonic())
		}
		if err != nil {
			if socket != 0 {
				_ = vm.services.Network.CloseSocket(vm.serviceOwner, socket, vm.services.Events)
			}
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		return ReferenceValue(vm.NewObject("javax/microedition/io/SocketConnection", &socketConnectionState{socket: socket})), true, nil
	})
	vm.RegisterNative("javax/microedition/io/StreamConnectionNotifier", "close", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid StreamConnectionNotifier")
		}
		object.Fields[serverSocketClosedField] = IntValue(1)
		return Value{}, false, nil
	})
}

func (vm *VM) installCLDCDatagramNatives() {
	for _, method := range []struct {
		name       string
		descriptor string
	}{{"getMaximumLength", "()I"}, {"getNominalLength", "()I"}} {
		method := method
		vm.RegisterNative("javax/microedition/io/DatagramConnection", method.name, method.descriptor, func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			if _, err := vm.openSocketConnection(receiver); err != nil {
				return Value{}, false, err
			}
			return IntValue(datagramMaxLength), true, nil
		})
	}
	for _, descriptor := range []string{"(I)Ljavax/microedition/io/Datagram;", "(ILjava/lang/String;)Ljavax/microedition/io/Datagram;", "([BI)Ljavax/microedition/io/Datagram;", "([BILjava/lang/String;)Ljavax/microedition/io/Datagram;"} {
		descriptor := descriptor
		vm.RegisterNative("javax/microedition/io/DatagramConnection", "newDatagram", descriptor, func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
			if _, err := vm.openSocketConnection(receiver); err != nil {
				return Value{}, false, err
			}
			argument, data := 0, uint32(0)
			if descriptor[1] == '[' {
				var err error
				data, err = referenceArgument(args, argument)
				if err != nil {
					return Value{}, false, err
				}
				argument++
			}
			size, err := intArgument(args, argument)
			if err != nil {
				return Value{}, false, err
			}
			argument++
			if size < 0 || size > datagramMaxLength {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid datagram size")
			}
			if data == 0 {
				data = vm.NewByteArray(make([]byte, size))
			} else if bytes, byteErr := vm.ByteArray(data); byteErr != nil || int(size) > len(bytes) {
				return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "datagram buffer too small")
			}
			address := uint32(0)
			if argument < len(args) {
				address, err = referenceArgument(args, argument)
				if err != nil {
					return Value{}, false, err
				}
			}
			return ReferenceValue(vm.newDatagramObject(data, 0, size, address)), true, nil
		})
	}
	vm.RegisterNative("javax/microedition/io/DatagramConnection", "close", "()V", nativeCloseConnection)
	vm.RegisterNative("javax/microedition/io/DatagramConnection", "send", "(Ljavax/microedition/io/Datagram;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		connection, err := vm.openSocketConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		datagram, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		data, _, _, err := vm.datagramSlice(datagram)
		if err != nil {
			return Value{}, false, err
		}
		_, err = vm.services.Network.SocketWrite(vm.serviceOwner, connection.socket, data)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/DatagramConnection", "receive", "(Ljavax/microedition/io/Datagram;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		connection, err := vm.openSocketConnection(receiver)
		if err != nil {
			return Value{}, false, err
		}
		datagram, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		_, offset, capacity, err := vm.datagramSlice(datagram)
		if err != nil {
			return Value{}, false, err
		}
		info, err := vm.services.Network.SocketInfo(vm.serviceOwner, connection.socket)
		if err != nil {
			return Value{}, false, err
		}
		count := min(uint64(capacity), info.ReadBytes)
		data, err := vm.services.Network.SocketRead(vm.serviceOwner, connection.socket, count)
		if err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
		object, _ := vm.Object(datagram)
		bufferReference, _ := object.Fields[datagramDataField].Reference()
		buffer, _ := vm.Object(bufferReference)
		for index, value := range data {
			buffer.Array.Elements[int(offset)+index] = IntValue(int32(int8(value)))
		}
		object.Fields[datagramLengthField] = IntValue(int32(len(data)))
		return Value{}, false, nil
	})
	vm.installDatagramAccessors()
}

func (vm *VM) newDatagramObject(data uint32, offset, length int32, address uint32) uint32 {
	reference := vm.NewObject("javax/microedition/io/Datagram", nil)
	object, _ := vm.Object(reference)
	object.Fields[datagramDataField] = ReferenceValue(data)
	object.Fields[datagramOffsetField] = IntValue(offset)
	object.Fields[datagramLengthField] = IntValue(length)
	object.Fields[datagramAddressField] = ReferenceValue(address)
	return reference
}

func (vm *VM) datagramSlice(reference uint32) ([]byte, int32, int32, error) {
	object, ok := vm.Object(reference)
	if !ok || object.Class != "javax/microedition/io/Datagram" {
		return nil, 0, 0, fmt.Errorf("invalid Datagram")
	}
	dataReference, err := object.Fields[datagramDataField].Reference()
	if err != nil {
		return nil, 0, 0, err
	}
	data, err := vm.ByteArray(dataReference)
	if err != nil {
		return nil, 0, 0, err
	}
	offset, err := object.Fields[datagramOffsetField].Int()
	if err != nil {
		return nil, 0, 0, err
	}
	length, err := object.Fields[datagramLengthField].Int()
	if err != nil || offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(data)) {
		return nil, 0, 0, fmt.Errorf("invalid Datagram range")
	}
	return data[offset : offset+length], offset, int32(len(data)) - offset, nil
}

func (vm *VM) installDatagramAccessors() {
	vm.RegisterNative("javax/microedition/io/Datagram", "getData", "()[B", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Datagram")
		}
		return object.Fields[datagramDataField], true, nil
	})
	for _, method := range []struct{ name, field string }{{"getLength", datagramLengthField}, {"getOffset", datagramOffsetField}} {
		method := method
		vm.RegisterNative("javax/microedition/io/Datagram", method.name, "()I", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
			object, ok := vm.Object(receiver)
			if !ok {
				return Value{}, false, fmt.Errorf("invalid Datagram")
			}
			value, err := object.Fields[method.field].Int()
			return IntValue(value), err == nil, err
		})
	}
	vm.RegisterNative("javax/microedition/io/Datagram", "getAddress", "()Ljava/lang/String;", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Datagram")
		}
		value, ok := object.Fields[datagramAddressField]
		if !ok {
			value = ReferenceValue(0)
		}
		return value, true, nil
	})
	vm.RegisterNative("javax/microedition/io/Datagram", "setData", "([BII)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		data, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		bytes, err := vm.ByteArray(data)
		if err != nil {
			return Value{}, false, err
		}
		offset, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		length, err := intArgument(args, 2)
		if err != nil || offset < 0 || length < 0 || int64(offset)+int64(length) > int64(len(bytes)) {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid datagram range")
		}
		object, _ := vm.Object(receiver)
		object.Fields[datagramDataField], object.Fields[datagramOffsetField], object.Fields[datagramLengthField] = ReferenceValue(data), IntValue(offset), IntValue(length)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/Datagram", "setLength", "(I)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		length, err := intArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		_, _, capacity, err := vm.datagramSlice(receiver)
		if err != nil || length < 0 || length > capacity {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid datagram length")
		}
		object, _ := vm.Object(receiver)
		object.Fields[datagramLengthField] = IntValue(length)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/Datagram", "reset", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		object, ok := vm.Object(receiver)
		if !ok {
			return Value{}, false, fmt.Errorf("invalid Datagram")
		}
		object.Fields[datagramOffsetField], object.Fields[datagramLengthField] = IntValue(0), IntValue(0)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/Datagram", "setAddress", "(Ljava/lang/String;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		address, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		if address != 0 {
			if _, err = vm.String(address); err != nil {
				return Value{}, false, err
			}
		}
		object, _ := vm.Object(receiver)
		object.Fields[datagramAddressField] = ReferenceValue(address)
		return Value{}, false, nil
	})
	vm.RegisterNative("javax/microedition/io/Datagram", "setAddress", "(Ljavax/microedition/io/Datagram;)V", func(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
		other, err := referenceArgument(args, 0)
		if err != nil {
			return Value{}, false, err
		}
		otherObject, ok := vm.Object(other)
		if !ok || otherObject.Class != "javax/microedition/io/Datagram" {
			return Value{}, false, vm.newThrowable("java/lang/IllegalArgumentException", "invalid Datagram")
		}
		object, _ := vm.Object(receiver)
		object.Fields[datagramAddressField] = otherObject.Fields[datagramAddressField]
		return Value{}, false, nil
	})
}
