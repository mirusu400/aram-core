package ktf

import (
	"context"
	"testing"
)

// Card() is documented to use the default Display dimensions. Issue278
// lays out its background using Display height after showing an annunciator.

func TestKTFDisplayHeightMatchesDefaultCardWithAnnunciator(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		shown, transparent, docked bool
	}{
		{"hidden", false, false, false}, {"shown opaque", true, false, false}, {"shown transparent", true, true, false}, {"docked hidden", false, false, true}, {"docked opaque", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCardOriginRuntime(t)
			state := &ktfLWCComponent{}
			r.initializeLWCAnnunciator(state)
			state.shown = tc.shown
			state.transparent = tc.transparent
			r.lwcComponents[1] = state
			display, err := r.ensureDefaultDisplay()
			check(t, err)
			card, err := r.NewHostJavaObject("org/kwis/msp/lcdui/Card")
			check(t, err)
			check(t, r.initializeCard(card, display))
			if tc.docked {
				parameters := allocWords(t, r, 3)
				r.NativeParameterBase = parameters
				check(t, r.writeWords(parameters, []uint32{display, card, 0}))
				_, err = r.handleDisplayMethod(context.Background(), "setDockedCard", "(Lorg/kwis/msp/lcdui/Card;I)V")
				check(t, err)
			}
			got, err := r.handleDisplayMethod(context.Background(), "getHeight", "()I")
			check(t, err)
			want, err := r.readJavaFieldWord(card, 20)
			check(t, err)
			if got != want {
				t.Errorf("Display height=%d, default Card height=%d", got, want)
			}
			if r.displayHeight() != 320 || r.frame.Bounds().Dy() != 320 {
				t.Fatal("physical framebuffer resized")
			}
			fb, err := r.EnsureWIPICScreenFramebuffer()
			check(t, err)
			if r.wipicFramebuffers[fb].height != 320 {
				t.Fatal("native framebuffer resized")
			}
		})
	}
}
