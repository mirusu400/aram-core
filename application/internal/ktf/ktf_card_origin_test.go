package ktf

import (
	"image"
	"image/color"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// newCardOriginRuntime returns a runtime whose framebuffer is the handset
// screen, ready for a Card paint.
func newCardOriginRuntime(t *testing.T) *Runtime {
	t.Helper()
	frame := image.NewRGBA(image.Rect(0, 0, int(ktfDisplayWidth), int(ktfDisplayHeight)))
	runtime, err := NewRuntimeForProfile(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	}, frame, ProfileID, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	return runtime
}

// showAnnunciator adds a shown, opaque annunciator so the card is laid out
// below it, the way a title that calls AnnunciatorComponent.show() leaves it.
func showAnnunciator(runtime *Runtime) {
	state := &ktfLWCComponent{}
	runtime.initializeLWCAnnunciator(state)
	state.shown = true
	runtime.lwcComponents[1] = state
}

func TestKTFCardWithoutAnnunciatorOwnsTheWholeScreen(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	if got, want := runtime.DefaultCardHeight(), ktfDisplayHeight; got != want {
		t.Fatalf("card height = %d, want %d", got, want)
	}
	if got := runtime.CardOriginY(); got != 0 {
		t.Fatalf("card origin = %d, want 0", got)
	}
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	runtime.ResetScreenGraphics(graphics)
	state := runtime.Graphics[graphics]
	if got, want := state.clip, runtime.frame.Bounds(); got != want {
		t.Fatalf("clip = %v, want %v", got, want)
	}
	if got := state.offset(); got != (image.Point{}) {
		t.Fatalf("offset = %v, want the framebuffer origin", got)
	}
}

func TestKTFShownAnnunciatorPaintsTheCardBelowTheStrip(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	showAnnunciator(runtime)

	cardHeight := runtime.DefaultCardHeight()
	if want := ktfDisplayHeight - uint32(ktfAnnunciatorHeight); cardHeight != want {
		t.Fatalf("card height = %d, want %d", cardHeight, want)
	}
	if got, want := runtime.CardOriginY(), uint32(ktfAnnunciatorHeight); got != want {
		t.Fatalf("card origin = %d, want %d", got, want)
	}

	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	runtime.ResetScreenGraphics(graphics)
	state := runtime.Graphics[graphics]

	want := image.Rect(0, int(ktfAnnunciatorHeight), int(ktfDisplayWidth), int(ktfDisplayHeight))
	if state.clip != want {
		t.Fatalf("clip = %v, want the card rectangle %v", state.clip, want)
	}
	if got, expect := state.offset(), image.Pt(0, int(ktfAnnunciatorHeight)); got != expect {
		t.Fatalf("offset = %v, want %v", got, expect)
	}
	// The guest still sees a card whose top-left is the origin.
	if got := state.translate; got != (image.Point{}) {
		t.Fatalf("guest-visible translate = %v, want zero", got)
	}

	// A guest fill of the whole card must land below the annunciator and
	// reach the bottom edge, not stop an annunciator short of it.
	state.color = color.RGBA{R: 0xff, A: 0xff}
	rect := image.Rect(0, 0, int(ktfDisplayWidth), int(cardHeight)).
		Add(state.offset()).
		Intersect(state.clip)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			state.plot(x, y)
		}
	}
	if got := runtime.frame.RGBAAt(0, int(ktfAnnunciatorHeight)-1); got.R != 0 {
		t.Fatalf("annunciator strip pixel = %#v, want untouched", got)
	}
	if got := runtime.frame.RGBAAt(0, int(ktfAnnunciatorHeight)); got.R != 0xff {
		t.Fatalf("first card row = %#v, want painted", got)
	}
	if got := runtime.frame.RGBAAt(0, int(ktfDisplayHeight)-1); got.R != 0xff {
		t.Fatalf("bottom screen row = %#v, want painted", got)
	}
}

func TestKTFCardClipCannotReachIntoTheAnnunciatorStrip(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	showAnnunciator(runtime)
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	runtime.ResetScreenGraphics(graphics)
	state := runtime.Graphics[graphics]

	// A title that clips above its own card — a logo dropping in from off the
	// top edge does exactly this — must still be bounded by the card.
	above := image.Rect(0, -int(ktfAnnunciatorHeight), int(ktfDisplayWidth), 0).
		Add(state.offset())
	state.clip = above.Intersect(state.drawable())
	if !state.clip.Empty() && state.clip.Min.Y < int(ktfAnnunciatorHeight) {
		t.Fatalf("clip = %v reaches into the annunciator strip", state.clip)
	}
}

func TestKTFShowingTheAnnunciatorClearsTheStripBehindIt(t *testing.T) {
	runtime := newCardOriginRuntime(t)
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	// Paint the full screen the way a title does before it shows its
	// annunciator, then show it.
	runtime.ResetScreenGraphics(graphics)
	state := runtime.Graphics[graphics]
	state.color = color.RGBA{B: 0xff, A: 0xff}
	for y := 0; y < int(ktfDisplayHeight); y++ {
		for x := 0; x < int(ktfDisplayWidth); x++ {
			state.plot(x, y)
		}
	}
	if got := runtime.frame.RGBAAt(0, 0); got.B != 0xff {
		t.Fatalf("pre-annunciator paint = %#v, want painted", got)
	}

	showAnnunciator(runtime)
	runtime.ResetScreenGraphics(graphics)

	if got := runtime.frame.RGBAAt(0, 0); got.B != 0 {
		t.Fatalf("annunciator strip = %#v, want cleared", got)
	}
	if got := runtime.frame.RGBAAt(0, int(ktfAnnunciatorHeight)); got.B != 0xff {
		t.Fatalf("card row = %#v, want the earlier paint kept", got)
	}
}
