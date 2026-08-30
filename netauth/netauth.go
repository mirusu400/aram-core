// Package netauth defines the seam by which the raptor runtime delegates the
// LGT carrier network/DRM ordinals (106/238, and any related connect/send/recv)
// to an out-of-tree backend (aram-authd). It has no dependencies so aram-core
// stays standalone-buildable; the backend implements this contract and the
// composition root (aram-emu) injects it.
package netauth

// Memory is the guest-memory view a backend uses to read a game's requests and
// populate the session state it reads back. Addresses are guest addresses;
// errors mirror the underlying CPU backend.
type Memory interface {
	ReadU8(addr uint32) (uint8, error)
	WriteU8(addr uint32, value uint8) error
	ReadU32(addr uint32) (uint32, error)
	WriteU32(addr uint32, value uint32) error
	ReadBytes(addr uint32, n int) ([]byte, error)
	WriteBytes(addr uint32, data []byte) error
}

// Call is one raptor network-ordinal invocation. Args holds r0..r2 as the guest
// passed them; SP and LR are the guest stack pointer and link register at the
// call, which a backend may use to walk the caller chain.
type Call struct {
	Ordinal uint32
	Args    [3]uint32
	SP      uint32
	LR      uint32
}

// Backend services the LGT carrier network/DRM ordinals. The runtime calls
// Handle for each routed ordinal; the backend may read/write guest session
// memory via mem and returns the guest-visible result (r0). handled=false
// declines the ordinal so the runtime applies its default.
type Backend interface {
	Handle(call Call, mem Memory) (result uint32, handled bool)
}

// Completion is the asynchronous carrier response a backend asks the runtime to
// deliver to the title after a network ordinal, emulating the DRM/server
// handshake result a real handset receives out of band. A raptor title blocks
// on "접속중"/"서버 접속중" until it arrives.
//
// The runtime delivers it as a Clet HandleEvent(Event, Arg1, buffer): Event is
// the handset event type that carries a server response, Arg1 the status word,
// and Response the bytes written to a runtime-owned buffer the title reads as
// the event's data pointer. DelayFrames lets the title settle into its wait
// state before the event is posted.
type Completion struct {
	Event       uint32
	Arg1        uint32
	Response    []byte
	DelayFrames int
}

// CompletionSource is an optional Backend capability. After the runtime routes
// a network ordinal that the backend handled, it asks the backend for an
// asynchronous completion to deliver to the title; nil means none. A backend
// that only reads/writes session memory need not implement it.
type CompletionSource interface {
	Complete(call Call) *Completion
}
