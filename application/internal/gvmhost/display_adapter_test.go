package gvmhost

import (
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/gvm"
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
