package gvmhost

import "fmt"

// DisplayState is a detached snapshot of guest-visible indexed display state.
type DisplayState struct {
	Width     uint32
	Height    uint32
	Drawing   []byte
	Auxiliary []byte
	Mapping   uint8
	Selector  uint8
}

func (d *DisplayAdapter) Snapshot() DisplayState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return DisplayState{
		Width:     uint32(d.drawing.width),
		Height:    uint32(d.drawing.height),
		Drawing:   append([]byte(nil), d.drawing.pixels...),
		Auxiliary: append([]byte(nil), d.auxiliary.pixels...),
		Mapping:   d.mapping,
		Selector:  d.selector,
	}
}

// Restore validates the complete display snapshot before committing it.
func (d *DisplayAdapter) Restore(state DisplayState) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	pixels := uint64(state.Width) * uint64(state.Height)
	if state.Width != uint32(d.drawing.width) || state.Height != uint32(d.drawing.height) ||
		pixels != uint64(len(state.Drawing)) || pixels != uint64(len(state.Auxiliary)) ||
		state.Mapping > 6 || state.Selector > 181 {
		return fmt.Errorf("%w: incompatible display state", ErrInvalidDisplayConfig)
	}
	active, err := d.palette.Map(state.Mapping, int16(state.Selector))
	if err != nil {
		return fmt.Errorf("%w: restore palette: %v", ErrInvalidDisplayConfig, err)
	}
	copy(d.drawing.pixels, state.Drawing)
	copy(d.auxiliary.pixels, state.Auxiliary)
	d.mapping = state.Mapping
	d.selector = state.Selector
	d.activeColor = active
	// Font handles are an internal cache owned by the newly constructed text
	// service. They can be recreated without changing guest-visible state.
	clear(d.textFonts[:])
	return nil
}
