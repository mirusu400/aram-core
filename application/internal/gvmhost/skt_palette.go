package gvmhost

import (
	"errors"
	"fmt"
	"image/color"

	shared "github.com/mirusu400/aram-core/runtime"
)

var ErrUnsupportedSKTPaletteIndex = errors.New("gvmhost: unsupported SKT palette index")

// lastSKTPaletteSelector is the last of the 182 public COLOR8 selectors.
const lastSKTPaletteSelector = 181

// SKTCompatibilityPalette supplies the exact selected-corpus palette-to-packed
// mapping and a portable RGB332 presentation policy. The latter is deliberately
// independent of the unresolved native normal-orientation component ramps.
type SKTCompatibilityPalette struct{}

func (SKTCompatibilityPalette) Map(mapping uint8, selector int16) (byte, error) {
	if selector < 0 || selector > 181 {
		return 0, fmt.Errorf("%w: mapping %d selector %d", ErrUnsupportedSKTPaletteIndex, mapping, selector)
	}
	packed, ok := SKTGammaColor(mapping, uint8(selector))
	if !ok {
		return 0, fmt.Errorf("%w: mapping %d selector %d", ErrUnsupportedSKTPaletteIndex, mapping, selector)
	}
	return packed, nil
}

// MapPhase maps a selector under one of the native palette's blink phases.
func (SKTCompatibilityPalette) MapPhase(mapping, phase uint8, selector int16) (byte, error) {
	if selector < 0 || selector > lastSKTPaletteSelector {
		return 0, fmt.Errorf("%w: mapping %d selector %d", ErrUnsupportedSKTPaletteIndex, mapping, selector)
	}
	packed, ok := shared.SKTGammaColorPhase(mapping, uint8(selector), phase)
	if !ok {
		return 0, fmt.Errorf("%w: mapping %d phase %d selector %d", ErrUnsupportedSKTPaletteIndex, mapping, phase, selector)
	}
	return packed, nil
}

func (SKTCompatibilityPalette) Color(index byte) color.RGBA {
	return shared.SKTPackedColor(index)
}

// SKTGammaColor converts the public Mobile C palette indices used by the
// selected SKT corpus into the packed byte consumed by the GVM display path.
//
// The supported subset includes fixed and normal colors, plus the initial
// phase of the reference player's blinking colors; MapPhase covers the other
// phases. Index 4 is transparent and returns
// a zero byte with ok=true; callers must preserve transparency separately.
func SKTGammaColor(gamma, index uint8) (packed byte, ok bool) {
	return shared.SKTGammaColor(gamma, index)
}
