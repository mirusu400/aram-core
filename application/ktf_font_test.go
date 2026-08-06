package application

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFFontSelectionControlsGraphicsText(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	writeParameters := func(values ...uint32) {
		t.Helper()
		for index, value := range values {
			if err := runtime.cpu.WriteRegister(
				cpu.RegisterR1+uint32(index),
				value,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	defaultFont, err := runtime.ensureDefaultFont()
	if err != nil {
		t.Fatal(err)
	}
	writeParameters(0, 0, ktfJavaFontSizeSmall)
	smallFont, err := runtime.handleFontMethod(
		"getFont",
		"(III)Lorg/kwis/msp/lcdui/Font;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if smallFont == 0 || smallFont == defaultFont {
		t.Fatalf(
			"small font = 0x%08x, default = 0x%08x",
			smallFont,
			defaultFont,
		)
	}
	smallService := runtime.fontServices[smallFont]
	metrics, err := runtime.services.Text.Metrics(
		runtime.serviceOwner,
		smallService,
	)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Height != 8 {
		t.Fatalf("small font height = %d, want 8", metrics.Height)
	}
	writeParameters(0, 0, ktfJavaFontSizeSmall)
	reusedFont, err := runtime.handleFontMethod(
		"getFont",
		"(III)Lorg/kwis/msp/lcdui/Font;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if reusedFont != smallFont {
		t.Fatalf(
			"repeated small font = 0x%08x, want 0x%08x",
			reusedFont,
			smallFont,
		)
	}

	graphics, err := runtime.newHostJavaObject("org/kwis/msp/lcdui/Graphics")
	if err != nil {
		t.Fatal(err)
	}
	writeParameters(graphics, 0)
	if _, err := runtime.handleGraphicsMethod(
		"<init>",
		"(Lorg/kwis/msp/lcdui/Display;)V",
	); err != nil {
		t.Fatal(err)
	}
	writeParameters(graphics, smallFont)
	if _, err := runtime.handleGraphicsMethod(
		"setFont",
		"(Lorg/kwis/msp/lcdui/Font;)V",
	); err != nil {
		t.Fatal(err)
	}
	writeParameters(graphics)
	selected, err := runtime.handleGraphicsMethod(
		"getFont",
		"()Lorg/kwis/msp/lcdui/Font;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if selected != smallFont {
		t.Fatalf(
			"graphics font = 0x%08x, want 0x%08x",
			selected,
			smallFont,
		)
	}

	const text = "가가"
	width, err := runtime.services.Text.Measure(
		runtime.serviceOwner,
		smallService,
		text,
	)
	if err != nil {
		t.Fatal(err)
	}
	if width != 16 {
		t.Fatalf("small text width = %d, want 16", width)
	}
	state := runtime.graphics[graphics]
	if err := runtime.drawGraphicsTextShared(state, text, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	right := -1
	for y := 0; y < state.target.Bounds().Dy(); y++ {
		for x := 0; x < state.target.Bounds().Dx(); x++ {
			if _, _, _, alpha := state.target.At(x, y).RGBA(); alpha != 0 {
				right = max(right, x)
			}
		}
	}
	if right < 0 || right >= int(width) {
		t.Fatalf(
			"small text right edge = %d, measured width = %d",
			right,
			width,
		)
	}
}
