package system

// LevelSignal is a deterministic single-machine wire shared by platform
// devices. The machine owns synchronization; devices only sample or drive the
// level while executing on that machine.
type LevelSignal struct {
	asserted bool
}

func NewLevelSignal() *LevelSignal {
	return &LevelSignal{}
}

func (s *LevelSignal) Set(asserted bool) {
	s.asserted = asserted
}

func (s *LevelSignal) Asserted() bool {
	return s.asserted
}
