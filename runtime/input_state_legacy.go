package runtime

// Schema 2 stored held controls but had no deferred transition queue.
type inputStateV2 struct {
	MaxControls    uint32
	RepeatDelayNS  int64
	RepeatPeriodNS int64
	Focused        bool
	Controls       []InputControlState
}

func decodeLegacyInputState(data []byte) (InputState, error) {
	var old inputStateV2
	if err := decodeStateValue(data, &old); err != nil {
		return InputState{}, err
	}
	return InputState{
		MaxControls: old.MaxControls, RepeatDelayNS: old.RepeatDelayNS,
		RepeatPeriodNS: old.RepeatPeriodNS, Focused: old.Focused, Controls: old.Controls,
	}, nil
}
