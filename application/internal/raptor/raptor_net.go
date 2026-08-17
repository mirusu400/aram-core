package raptor

import (
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/netauth"
)

// cpuNetMemory adapts a cpu.Backend to netauth.Memory so a backend can read a
// game's requests and populate the session state it reads back.
type cpuNetMemory struct{ cpu cpu.Backend }

func (m cpuNetMemory) ReadU8(addr uint32) (uint8, error) {
	var b [1]byte
	if err := m.cpu.ReadMemory(addr, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func (m cpuNetMemory) WriteU8(addr uint32, value uint8) error {
	return m.cpu.WriteMemory(addr, []byte{value})
}

func (m cpuNetMemory) ReadU32(addr uint32) (uint32, error) {
	var b [4]byte
	if err := m.cpu.ReadMemory(addr, b[:]); err != nil {
		return 0, err
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

func (m cpuNetMemory) WriteU32(addr uint32, value uint32) error {
	return m.cpu.WriteMemory(addr, []byte{
		byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24),
	})
}

func (m cpuNetMemory) ReadBytes(addr uint32, n int) ([]byte, error) {
	b := make([]byte, n)
	if err := m.cpu.ReadMemory(addr, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (m cpuNetMemory) WriteBytes(addr uint32, data []byte) error {
	return m.cpu.WriteMemory(addr, data)
}

// dispatchNet routes a network ordinal to the installed netauth.Backend, if
// any. handled=false means the runtime should apply its default.
func (r *Runtime) dispatchNet(ordinal uint32) (result uint32, handled bool, err error) {
	if r.Net == nil {
		return 0, false, nil
	}
	var args [3]uint32
	for i, reg := range []uint32{cpu.RegisterR0, cpu.RegisterR1, cpu.RegisterR2} {
		value, readErr := r.CPU.ReadRegister(reg)
		if readErr != nil {
			return 0, false, readErr
		}
		args[i] = value
	}
	result, handled = r.Net.Handle(netauth.Call{Ordinal: ordinal, Args: args}, cpuNetMemory{r.CPU})
	return result, handled, nil
}
