package system

// StatusSignal is a deterministic bitfield wire shared by platform devices.
// The machine owns synchronization; devices only sample or drive status bits
// while executing on that machine.
type StatusSignal struct {
	value uint32
}

func NewStatusSignal() *StatusSignal {
	return &StatusSignal{}
}

func (s *StatusSignal) Set(value uint32) {
	s.value = value
}

func (s *StatusSignal) Clear(mask uint32) {
	s.value &^= mask
}

func (s *StatusSignal) Value() uint32 {
	return s.value
}
