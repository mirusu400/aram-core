//go:build !cgo || (!darwin && !freebsd && !linux && !windows)

package unicornbackend

import (
	"fmt"
	"runtime"
	"unsafe"
)

func openUnicornAPI(string) (*unicornAPI, error) {
	return nil, fmt.Errorf(
		"native cgo loader unavailable for %s/%s (CGO_ENABLED may be disabled)",
		runtime.GOOS, runtime.GOARCH,
	)
}

func (*unicornAPI) release() error { return nil }

func (*unicornAPI) openEngine(int32, int32, *uintptr) int32 { return ucErrHandle }

func (*unicornAPI) closeEngine(uintptr) int32 { return ucErrHandle }

func (*unicornAPI) readRegister(uintptr, int32, unsafe.Pointer) int32 {
	return ucErrHandle
}

func (*unicornAPI) writeRegister(uintptr, int32, unsafe.Pointer) int32 {
	return ucErrHandle
}

func (*unicornAPI) readMemory(uintptr, uint64, unsafe.Pointer, uint64) int32 {
	return ucErrHandle
}

func (*unicornAPI) writeMemory(uintptr, uint64, unsafe.Pointer, uint64) int32 {
	return ucErrHandle
}

func (*unicornAPI) mapMemory(uintptr, uint64, uint64, uint32) int32 {
	return ucErrHandle
}

func (*unicornAPI) start(uintptr, uint64, uint64, uint64, uintptr) int32 {
	return ucErrHandle
}

func (*unicornAPI) stop(uintptr) int32 { return ucErrHandle }
