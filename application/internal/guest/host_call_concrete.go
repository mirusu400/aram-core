package guest

import (
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// CaptureHostCallFrame avoids a capability-interface lookup for the exact Go
// backend. The backend value is supplied anew for every operation; no identity
// or capability is retained across callbacks or CPU replacement.
func CaptureHostCallFrame(
	backend cpu.Backend,
	destination *cpu.HostCallFrame,
	request cpu.HostCallFrameRequest,
) error {
	if concrete, ok := backend.(*interpreter.Backend); ok && concrete != nil &&
		destination != nil && request.StackWords <= cpu.MaxHostCallWords &&
		request.ParameterWords <= cpu.MaxHostCallWords &&
		(request.ParameterWords == 0 || request.ParameterAddress != 0) {
		return concrete.CaptureHostCallFrame(destination, request)
	}
	// Keep validation precedence, error text and typed-nil behavior in the
	// original wrapper. Other concrete types retain their own optional methods.
	return cpu.CaptureHostCallFrame(backend, destination, request)
}

// ReturnHostCallFrame keeps the existing atomic capture and ordered register
// writes. A wrapper or any other backend still uses the original public path.
func ReturnHostCallFrame(
	backend cpu.Backend,
	destination *cpu.HostCallFrame,
	low, high uint32,
) error {
	if concrete, ok := backend.(*interpreter.Backend); ok && concrete != nil && destination != nil {
		return concrete.ReturnHostCallFrame(destination, low, high)
	}
	return cpu.ReturnHostCallFrame(backend, destination, low, high)
}
