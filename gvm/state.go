package gvm

import (
	"errors"
	"fmt"
	"math"
)

// ErrInvalidState reports a malformed or incompatible volatile VM snapshot.
var ErrInvalidState = errors.New("gvm: invalid volatile state")

// TextStyleState is the serializable form of the VM-owned text drawing state.
type TextStyleState struct {
	Mode      uint8
	Primary   uint8
	Secondary uint8
	Variant   uint8
}

// State is a detached volatile VM snapshot. Host service implementations keep
// their own snapshots; this type covers bytecode-visible storage and execution.
type State struct {
	Program        []byte
	RAM            []byte
	PC             uint32
	Stack          []uint16
	StackBacking   []uint16
	Returns        []uint32
	Halted         bool
	SavedTop       int32
	SavedTopValid  bool
	ImageMode      uint8
	Media          [][]byte
	MediaSuffix    []byte
	MediaHasSuffix []bool
	TextStyle      TextStyleState
}

// Snapshot returns an independent, representation-neutral VM state. Faulted
// execution is deliberately excluded because sticky Go error values are not a
// stable serialization contract.
func (v *VM) Snapshot() (State, error) {
	if v == nil || v.fault != nil || v.pc < 0 || uint64(v.pc) > math.MaxUint32 {
		return State{}, fmt.Errorf("%w: GVM is not snapshot-safe", ErrInvalidState)
	}
	backing := v.stackBacking
	copy(backing[:v.depth], v.stack[:v.depth])
	state := State{
		Program:       append([]byte(nil), v.code...),
		PC:            uint32(v.pc),
		Stack:         append([]uint16(nil), v.stack[:v.depth]...),
		StackBacking:  append([]uint16(nil), backing[:]...),
		Halted:        v.halted,
		SavedTop:      v.savedTop,
		SavedTopValid: v.savedTopValid,
		ImageMode:     v.imageMode,
	}
	for _, target := range v.returns[:v.returnDepth] {
		if target < 0 || uint64(target) > math.MaxUint32 {
			return State{}, fmt.Errorf("%w: invalid GVM return target", ErrInvalidState)
		}
		state.Returns = append(state.Returns, uint32(target))
	}
	if v.address != nil {
		state.RAM = append([]byte(nil), v.address.ram...)
	}
	if v.services != nil {
		state.Media = make([][]byte, len(v.services.media))
		for index := range v.services.media {
			state.Media[index] = append([]byte(nil), v.services.media[index]...)
		}
		state.MediaSuffix = append([]byte(nil), v.services.mediaSuffix...)
		state.MediaHasSuffix = append([]bool(nil), v.services.mediaHasSuffix...)
		state.TextStyle = TextStyleState{
			Mode:      v.services.textStyle.mode,
			Primary:   v.services.textStyle.primary,
			Secondary: v.services.textStyle.secondary,
			Variant:   v.services.textStyle.variant,
		}
	}
	return state, nil
}

// Restore validates the complete snapshot before changing the VM.
func (v *VM) Restore(state State) error {
	if v == nil || len(state.Program) != len(v.code) ||
		uint64(state.PC) > uint64(len(v.code)) ||
		(!state.Halted && uint64(state.PC) >= uint64(len(v.code))) ||
		len(state.Stack) > len(v.stack) || len(state.StackBacking) != len(v.stackBacking) ||
		len(state.Returns) > len(v.returns) {
		return fmt.Errorf("%w: invalid GVM execution state", ErrInvalidState)
	}
	for index, value := range state.Stack {
		if state.StackBacking[index] != value {
			return fmt.Errorf("%w: inconsistent GVM stack backing", ErrInvalidState)
		}
	}
	if state.SavedTopValid && (state.SavedTop < -1 || state.SavedTop >= int32(len(v.stack))) {
		return fmt.Errorf("%w: invalid GVM saved stack top", ErrInvalidState)
	}
	for _, target := range state.Returns {
		if uint64(target) > uint64(len(v.code)) {
			return fmt.Errorf("%w: invalid GVM return target", ErrInvalidState)
		}
	}
	if (v.address == nil) != (state.RAM == nil) ||
		v.address != nil && len(state.RAM) != len(v.address.ram) {
		return fmt.Errorf("%w: invalid GVM RAM state", ErrInvalidState)
	}
	if v.services == nil {
		if state.Media != nil || state.MediaSuffix != nil || state.MediaHasSuffix != nil ||
			state.TextStyle != (TextStyleState{}) {
			return fmt.Errorf("%w: unexpected GVM service state", ErrInvalidState)
		}
	} else {
		if len(state.Media) != len(v.services.media) ||
			len(state.MediaSuffix) != len(v.services.mediaSuffix) ||
			len(state.MediaHasSuffix) != len(v.services.mediaHasSuffix) ||
			state.TextStyle.Mode > 3 || state.TextStyle.Primary > 181 ||
			state.TextStyle.Secondary > 181 || state.TextStyle.Variant > 2 {
			return fmt.Errorf("%w: invalid GVM service state", ErrInvalidState)
		}
		for index := range state.Media {
			if len(state.Media[index]) > math.MaxUint16 {
				return fmt.Errorf("%w: GVM media %d exceeds 16-bit length", ErrInvalidState, index)
			}
		}
	}

	copy(v.code, state.Program)
	if v.address != nil {
		copy(v.address.ram, state.RAM)
	}
	v.pc = int(state.PC)
	clear(v.stack[:])
	copy(v.stack[:], state.Stack)
	copy(v.stackBacking[:], state.StackBacking)
	v.depth = len(state.Stack)
	clear(v.returns[:])
	for index, target := range state.Returns {
		v.returns[index] = int(target)
	}
	v.returnDepth = len(state.Returns)
	v.halted = state.Halted
	v.fault = nil
	v.savedTop = state.SavedTop
	v.savedTopValid = state.SavedTopValid
	v.imageMode = state.ImageMode
	if v.services != nil {
		for index := range state.Media {
			v.services.media[index] = append(v.services.media[index][:0], state.Media[index]...)
		}
		copy(v.services.mediaSuffix, state.MediaSuffix)
		copy(v.services.mediaHasSuffix, state.MediaHasSuffix)
		v.services.textStyle = textStyleState{
			mode:      state.TextStyle.Mode,
			primary:   state.TextStyle.Primary,
			secondary: state.TextStyle.Secondary,
			variant:   state.TextStyle.Variant,
		}
	}
	return nil
}
