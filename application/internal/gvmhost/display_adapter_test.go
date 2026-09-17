package gvmhost

import (
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/gvm"
	shared "github.com/mirusu400/aram-core/runtime"
)

type testPalette struct {
	failSelector int16
}

func (p testPalette) Map(mapping uint8, selector int16) (byte, error) {
	if selector == p.failSelector {
		return 0, errors.New("rejected selector")
	}
	return byte(int16(mapping)*20 + selector), nil
}
func (testPalette) Color(index byte) color.RGBA { return color.RGBA{R: index, A: 0xff} }

type frameCollector struct{ frames []image.Image }

func (p *frameCollector) PublishGVMFrame(frame image.Image) error {
	p.frames = append(p.frames, frame)
	return nil
}

func newTestDisplay(t *testing.T, width, height int, orientation DisplayOrientation, palette PaletteMapper, publisher FramePublisher) *DisplayAdapter {
	t.Helper()
	display, err := NewDisplayAdapter(DisplayConfig{Width: width, Height: height, Orientation: orientation, Palette: palette, Publisher: publisher})
	if err != nil {
		t.Fatalf("NewDisplayAdapter() error = %v", err)
	}
	return display
}

func TestDisplayAdapterRejectsInvalidConfigAtomically(t *testing.T) {
	publisher := &frameCollector{}
	valid := DisplayConfig{Width: 1, Height: 1, Palette: testPalette{failSelector: -1}, Publisher: publisher}
	for _, mutate := range []func(*DisplayConfig){
		func(c *DisplayConfig) { c.Width = 0 },
		func(c *DisplayConfig) { c.Width = 257 },
		func(c *DisplayConfig) { c.Height = 0 },
		func(c *DisplayConfig) { c.Height = 257 },
		func(c *DisplayConfig) { c.Orientation = 2 },
		func(c *DisplayConfig) { c.Palette = nil },
		func(c *DisplayConfig) { c.Publisher = nil },
	} {
		config := valid
		mutate(&config)
		display, err := NewDisplayAdapter(config)
		if !errors.Is(err, ErrInvalidDisplayConfig) || display != nil {
			t.Fatalf("NewDisplayAdapter(%+v) = (%v, %v), want nil invalid config", config, display, err)
		}
	}
	config := valid
	config.Palette = testPalette{failSelector: 0}
	if display, err := NewDisplayAdapter(config); display != nil || !errors.Is(err, ErrInvalidDisplayConfig) {
		t.Fatalf("mapper rejection = (%v, %v), want nil invalid config", display, err)
	}
}

func TestDisplayAdapterDrawingCopyAndTransparentColor(t *testing.T) {
	publisher := &frameCollector{}
	display := newTestDisplay(t, 3, 2, DisplayOrientationDefault, testPalette{failSelector: -1}, publisher)
	if err := display.FillGVMDisplay(2); err != nil {
		t.Fatal(err)
	}
	if err := display.CopyGVMDisplay(gvm.DisplayBufferDrawing, gvm.DisplayBufferAuxiliary); err != nil {
		t.Fatal(err)
	}
	if err := display.ClearGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	if err := display.CopyGVMDisplay(gvm.DisplayBufferAuxiliary, gvm.DisplayBufferDrawing); err != nil {
		t.Fatal(err)
	}
	if err := display.SelectGVMMapping(3); err != nil {
		t.Fatal(err)
	}
	if err := display.SelectGVMColor(5); err != nil {
		t.Fatal(err)
	}
	if err := display.FillGVMRectangle(2, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := display.SelectGVMColor(4); err != nil {
		t.Fatal(err)
	}
	if err := display.FillGVMRectangle(-10, -10, 10, 10); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 65, 65, 2, 65, 65}
	for i, value := range want {
		if got := color.RGBAModel.Convert(publisher.frames[0].At(i%3, i/3)).(color.RGBA).R; got != value {
			t.Fatalf("pixel %d = %d, want %d", i, got, value)
		}
	}
}

func TestDisplayAdapterDrawRectangleClipsInclusiveEdges(t *testing.T) {
	publisher := &frameCollector{}
	display := newTestDisplay(t, 4, 3, DisplayOrientationDefault, testPalette{failSelector: -1}, publisher)
	if err := display.FillGVMDisplay(0); err != nil {
		t.Fatal(err)
	}
	if err := display.SelectGVMColor(7); err != nil {
		t.Fatal(err)
	}
	if err := display.DrawGVMRectangle(-1, 0, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	want := []byte{7, 7, 7, 0, 0, 0, 7, 0, 7, 7, 7, 0}
	for i, value := range want {
		if got := color.RGBAModel.Convert(publisher.frames[0].At(i%4, i/4)).(color.RGBA).R; got != value {
			t.Fatalf("pixel %d = %d, want %d", i, got, value)
		}
	}
	if err := display.SelectGVMColor(4); err != nil {
		t.Fatal(err)
	}
	if err := display.DrawGVMRectangle(0, 0, 3, 2); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayAdapterSpriteUsesDecoderMapperAndTransparencyAtomically(t *testing.T) {
	publisher := &frameCollector{}
	display := newTestDisplay(t, 3, 1, DisplayOrientationDefault, testPalette{failSelector: -1}, publisher)
	if err := display.FillGVMDisplay(9); err != nil {
		t.Fatal(err)
	}
	if err := display.SelectGVMMapping(2); err != nil {
		t.Fatal(err)
	}
	if err := display.DrawGVMSprite([]byte{8, 3, 1, 0, 0, 1, 4, 3}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	want := []byte{41, 9, 43}
	for x, value := range want {
		if got := color.RGBAModel.Convert(publisher.frames[0].At(x, 0)).(color.RGBA).R; got != value {
			t.Fatalf("pixel %d = %d, want %d", x, got, value)
		}
	}

	before := publisher.frames[0]
	display.palette = testPalette{failSelector: 3}
	if err := display.DrawGVMSprite([]byte{8, 2, 1, 0, 0, 1, 3}, 0, 0); err == nil {
		t.Fatal("DrawGVMSprite() error = nil, want mapper rejection")
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 3; x++ {
		if before.At(x, 0) != publisher.frames[1].At(x, 0) {
			t.Fatalf("failed sprite draw mutated pixel %d", x)
		}
	}
}

func TestDisplayAdapterTransformedSpriteMirrorsUsingNativeAnchorConvention(t *testing.T) {
	publisher := &frameCollector{}
	display := newTestDisplay(t, 3, 1, DisplayOrientationDefault, testPalette{failSelector: -1}, publisher)
	resource := []byte{8, 3, 1, 0, 0, 1, 2, 3}
	if err := display.DrawGVMTransformedSprite(resource, 3, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	for x, want := range []byte{3, 2, 1} {
		if got := color.RGBAModel.Convert(publisher.frames[0].At(x, 0)).(color.RGBA).R; got != want {
			t.Fatalf("mirrored pixel %d = %d, want %d", x, got, want)
		}
	}
}

func TestDisplayAdapterDrawsEuckrTextWithNativeAlignment(t *testing.T) {
	services, err := shared.NewServices(shared.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	publisher := &frameCollector{}
	display, err := NewDisplayAdapter(DisplayConfig{
		Width: 32, Height: 16, Palette: testPalette{failSelector: -1},
		Publisher: publisher, Text: services.Text, TextOwner: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// "가" in EUC-KR. Center alignment is computed from the two source bytes,
	// matching the native renderer's fixed-cell width calculation.
	if err := display.DrawGVMText([]byte{0xb0, 0xa1, 0}, 16, 1, gvm.TextDrawStyle{Mode: 2, Primary: 9, Alignment: 1}); err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, pixel := range display.drawing.pixels {
		changed = changed || pixel == 9
	}
	if !changed {
		t.Fatal("DrawGVMText() produced no glyph pixels")
	}
}

func TestGVMTextGeometryMatchesNativeCellsAndInk(t *testing.T) {
	tests := []struct {
		mode, bytes               int
		inkWidth, inkHeight, cell int
		ok                        bool
	}{
		{0, 1, 3, 5, 4, true}, {1, 1, 5, 7, 6, true},
		{2, 1, 5, 11, 6, true}, {3, 1, 10, 22, 12, true},
		{0, 2, 8, 6, 8, true}, {1, 2, 12, 8, 12, true},
		{2, 2, 11, 11, 12, true}, {3, 2, 22, 22, 24, true},
	}
	for _, test := range tests {
		width, height, cell, ok := gvmTextGeometry(uint8(test.mode), test.bytes)
		if width != test.inkWidth || height != test.inkHeight || cell != test.cell || ok != test.ok {
			t.Fatalf("geometry(%d,%d) = (%d,%d,%d,%v), want (%d,%d,%d,%v)", test.mode, test.bytes, width, height, cell, ok, test.inkWidth, test.inkHeight, test.cell, test.ok)
		}
	}
}

func TestScaleGVMGlyphUsesExactRequestedBounds(t *testing.T) {
	glyph := shared.Glyph{Width: 2, Height: 2, Alpha: []byte{1, 2, 3, 4}}
	got := scaleGVMGlyph(glyph, 3, 5)
	want := []byte{
		1, 1, 2,
		1, 1, 2,
		1, 1, 2,
		3, 3, 4,
		3, 3, 4,
	}
	if len(got) != 3*5 {
		t.Fatalf("scaled length = %d, want 15", len(got))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("scaled[%d] = %d, want %d", index, got[index], want[index])
		}
	}
}

func TestDisplayAdapterTextModesAlignClipAndFailAtomically(t *testing.T) {
	services, err := shared.NewServices(shared.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for mode := uint8(0); mode <= 3; mode++ {
		for alignment := uint8(0); alignment <= 2; alignment++ {
			display, err := NewDisplayAdapter(DisplayConfig{
				Width: 16, Height: 24, Palette: testPalette{failSelector: -1},
				Publisher: &frameCollector{}, Text: services.Text, TextOwner: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := display.DrawGVMText([]byte{'A', 0}, 0, -1, gvm.TextDrawStyle{Mode: mode, Primary: 7, Alignment: alignment}); err != nil {
				t.Fatalf("mode=%d alignment=%d: %v", mode, alignment, err)
			}
			_, height, _, _ := gvmTextGeometry(mode, 1)
			for y := height - 1; y < display.drawing.height; y++ {
				if y < 0 {
					continue
				}
				for x := range display.drawing.width {
					if display.drawing.pixels[y*display.drawing.width+x] == 7 {
						t.Fatalf("mode=%d drew below clipped native ink height at (%d,%d)", mode, x, y)
					}
				}
			}
		}
	}

	display, err := NewDisplayAdapter(DisplayConfig{
		Width: 8, Height: 8, Palette: testPalette{failSelector: -1},
		Publisher: &frameCollector{}, Text: services.Text, TextOwner: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := display.FillGVMDisplay(9); err != nil {
		t.Fatal(err)
	}
	if err := display.DrawGVMText([]byte{0xb0, 0}, 0, 0, gvm.TextDrawStyle{Mode: 2, Primary: 7}); err != nil {
		t.Fatalf("malformed EUC-KR fallback error = %v", err)
	}
	changed := false
	for _, pixel := range display.drawing.pixels {
		if pixel == 7 {
			changed = true
		}
	}
	if !changed {
		t.Fatal("malformed EUC-KR did not draw the native fallback glyph")
	}
	if err := display.FillGVMDisplay(9); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), display.drawing.pixels...)

	config := shared.DefaultConfig()
	config.Limits.Text.MaxStringBytes = 1
	limited, err := shared.NewServices(config)
	if err != nil {
		t.Fatal(err)
	}
	display.text = limited.Text
	if err := display.DrawGVMText([]byte{'A', 'B', 0}, 0, 0, gvm.TextDrawStyle{Mode: 1, Primary: 7}); !errors.Is(err, shared.ErrLimitExceeded) {
		t.Fatalf("oversized text error = %v, want ErrLimitExceeded", err)
	}
	for index := range before {
		if display.drawing.pixels[index] != before[index] {
			t.Fatalf("oversized text mutated pixel %d", index)
		}
	}
}

func TestDisplayAdapterPresentPublishesImmutableSnapshots(t *testing.T) {
	publisher := &frameCollector{}
	display := newTestDisplay(t, 1, 1, DisplayOrientationDefault, testPalette{failSelector: -1}, publisher)
	if err := display.FillGVMDisplay(7); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	first := publisher.frames[0]
	if _, mutable := first.(interface{ Set(int, int, color.Color) }); mutable {
		t.Fatal("published frame exposes mutation")
	}
	if err := display.FillGVMDisplay(8); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(first.At(0, 0)).(color.RGBA).R; got != 7 {
		t.Fatalf("first snapshot changed to %d", got)
	}
}

func TestDisplayAdapterAlternatePresentationRequiresExplicitOrientation(t *testing.T) {
	publisher := &frameCollector{}
	display := newTestDisplay(t, 2, 1, DisplayOrientationAlternate, testPalette{failSelector: -1}, publisher)
	if err := display.FillGVMDisplay(0x49); err != nil {
		t.Fatal(err)
	}
	if err := display.PresentGVMDisplay(); err != nil {
		t.Fatal(err)
	}
	if got := publisher.frames[0].Bounds(); got != image.Rect(0, 0, 1, 2) {
		t.Fatalf("bounds = %v, want rotated bounds", got)
	}
	if got := color.RGBAModel.Convert(publisher.frames[0].At(0, 0)).(color.RGBA); got != (color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}) {
		t.Fatalf("alternate pixel = %#v", got)
	}
}

type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingPublisher) PublishGVMFrame(image.Image) error {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return nil
}

func TestDisplayAdapterSerializesPresentationAndDrawing(t *testing.T) {
	publisher := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	display := newTestDisplay(t, 1, 1, DisplayOrientationDefault, testPalette{failSelector: -1}, publisher)
	presentDone := make(chan struct{})
	go func() { _ = display.PresentGVMDisplay(); close(presentDone) }()
	<-publisher.entered
	fillDone := make(chan struct{})
	go func() { _ = display.FillGVMDisplay(1); close(fillDone) }()
	select {
	case <-fillDone:
		t.Fatal("drawing completed while presentation held serialization lock")
	case <-time.After(20 * time.Millisecond):
	}
	close(publisher.release)
	select {
	case <-presentDone:
	case <-time.After(time.Second):
		t.Fatal("presentation did not complete")
	}
	select {
	case <-fillDone:
	case <-time.After(time.Second):
		t.Fatal("drawing did not resume")
	}
}
