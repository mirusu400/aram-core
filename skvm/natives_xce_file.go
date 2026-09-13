package skvm

import (
	"context"
	"errors"

	shared "github.com/mirusu400/aram-core/runtime"
)

// XCE paths are names in the private, serializable VFS, never host paths.
func (vm *VM) installXCEFileStreamNatives() {
	for _, descriptor := range []string{"(Ljava/lang/String;)V", "(Ljava/lang/String;Z)V"} {
		vm.RegisterNative("com/xce/io/FileOutputStream", "<init>", descriptor, nativeXCEFileOutputInit)
	}
	vm.RegisterNative("com/xce/io/FileOutputStream", "close", "()V", func(_ context.Context, vm *VM, receiver uint32, _ []Value) (Value, bool, error) {
		state, err := vm.outputStream(receiver)
		if err != nil {
			return Value{}, false, err
		}
		// Writes commit to shared storage immediately. Closing is idempotent.
		state.closed = true
		return Value{}, false, nil
	})
}

func nativeXCEFileOutputInit(_ context.Context, vm *VM, receiver uint32, args []Value) (Value, bool, error) {
	name, err := vm.fileNameArgument(args, 0)
	if err != nil {
		return Value{}, false, err
	}
	appendMode := false
	if len(args) == 2 {
		flag, err := intArgument(args, 1)
		if err != nil {
			return Value{}, false, err
		}
		appendMode = flag != 0
	}
	if appendMode {
		_, err = vm.services.Storage.Stat(shared.NamespacePrivate, name)
		if err != nil && !errors.Is(err, shared.ErrNotFound) {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
	}
	if !appendMode || errors.Is(err, shared.ErrNotFound) {
		if err := vm.services.Storage.WriteFile(shared.NamespacePrivate, name, nil); err != nil {
			return Value{}, false, vm.newThrowable("java/io/IOException", err.Error())
		}
	}
	return Value{}, false, vm.setNative(receiver, &outputStreamState{name: name, appendMode: appendMode})
}

func (vm *VM) writeXCEFileStream(state *outputStreamState, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	current, err := vm.services.Storage.ReadFile(shared.NamespacePrivate, state.name)
	if err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
	}
	// The existing buffer length is the non-append stream's position. Append
	// always uses the current VFS EOF, including writes by another open stream.
	offset := len(state.data)
	if state.appendMode {
		offset = len(current)
	}
	limit := vm.services.Config.Limits.Storage.MaxFileBytes
	end := uint64(offset) + uint64(len(data))
	if end > limit || end > uint64(int(^uint(0)>>1)) {
		return vm.newThrowable("java/io/IOException", "file exceeds storage limit")
	}
	next := make([]byte, max(len(current), int(end)))
	copy(next, current)
	copy(next[offset:], data)
	if err := vm.services.Storage.WriteFile(shared.NamespacePrivate, state.name, next); err != nil {
		return vm.newThrowable("java/io/IOException", err.Error())
	}
	// Update the position only after a successful, quota-checked commit.
	if !state.appendMode {
		state.data = next[:int(end)]
	}
	return nil
}
