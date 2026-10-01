package brewrt

import (
	"encoding/binary"
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	brewNetError            = ^uint32(0) // AEE_NET_ERROR
	brewNetSockNotSupported = uint32(0x207)
	brewNetDown             = uint32(0x216)
	brewNetNoMemory         = uint32(0x21f)
)

// A BREW socket exists independently of a network connection. In particular,
// INetMgr::OpenSocket can succeed while the handset is offline; a later socket
// operation reports the network failure.
type brewSocket struct {
	kind      uint32
	lastError uint32
}

func (r *Runtime) openGuestSocket() error {
	kind, err := r.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		return fmt.Errorf("read BREW socket type: %w", err)
	}
	// BREW defines STREAM, DGRAM, and (in later versions) UNSPEC.
	if kind > 2 {
		r.netLastError = brewNetSockNotSupported
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	object, err := r.allocateGuest(4)
	if err != nil {
		r.netLastError = brewNetNoMemory
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	}
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], socketVTable)
	if err := r.cpu.WriteMemory(object, encoded[:]); err != nil {
		r.releaseGuest(object)
		return fmt.Errorf("initialize BREW socket: %w", err)
	}
	r.socketHandles[object] = &brewSocket{kind: kind}
	r.netLastError = 0
	return r.cpu.WriteRegister(cpu.RegisterR0, object)
}

func (r *Runtime) handleGuestSocketMethod(slot uint32) error {
	object, err := r.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		return fmt.Errorf("read BREW socket object: %w", err)
	}
	socket := r.socketHandles[object]
	if socket == nil {
		return fmt.Errorf("BREW socket object 0x%08x is not open", object)
	}
	switch slot {
	case 2, 4, 14: // IAStream::Readable, Cancel; ISocket::Writeable
		// No readiness notification is pending in the offline runtime.
		return r.cpu.WriteRegister(cpu.RegisterR0, 0)
	case 6: // ISocket::GetLastError
		return r.cpu.WriteRegister(cpu.RegisterR0, socket.lastError)
	case 3, 5, 7, 8, 9, 10, 11, 12, 13, 15:
		// Read, GetPeerName, Connect, Bind, Write, WriteV, ReadV,
		// SendTo, RecvFrom, and IOCtl require a live network connection.
		socket.lastError = brewNetDown
		return r.cpu.WriteRegister(cpu.RegisterR0, brewNetError)
	default:
		return fmt.Errorf("unsupported BREW socket vtable slot %d", slot)
	}
}
