package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestKTFFontSelectionControlsGraphicsText(t *testing.T) {
	runtime := newTestRuntime(t)
	writeParameters := func(values ...uint32) {
		t.Helper()
		for index, value := range values {
			check(t, runtime.CPU.WriteRegister(
				cpu.RegisterR1+uint32(index),
				value,
			))
		}
	}

	defaultFont, err := runtime.ensureDefaultFont()
	check(t, err)
	writeParameters(0, 0, JavaFontSizeSmall)
	smallFont, err := runtime.handleFontMethod(
		"getFont",
		"(III)Lorg/kwis/msp/lcdui/Font;",
	)
	check(t, err)
	if smallFont == 0 || smallFont == defaultFont {
		t.Fatalf(
			"small font = 0x%08x, default = 0x%08x",
			smallFont,
			defaultFont,
		)
	}
	smallService := runtime.FontServices[smallFont]
	metrics, err := runtime.Services.Text.Metrics(
		runtime.ServiceOwner,
		smallService,
	)
	check(t, err)
	if metrics.Height != 8 {
		t.Fatalf("small font height = %d, want 8", metrics.Height)
	}
	writeParameters(0, 0, JavaFontSizeSmall)
	reusedFont, err := runtime.handleFontMethod(
		"getFont",
		"(III)Lorg/kwis/msp/lcdui/Font;",
	)
	check(t, err)
	if reusedFont != smallFont {
		t.Fatalf(
			"repeated small font = 0x%08x, want 0x%08x",
			reusedFont,
			smallFont,
		)
	}

	graphics := newHostObject(t, runtime, "org/kwis/msp/lcdui/Graphics")
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
	check(t, err)
	if selected != smallFont {
		t.Fatalf(
			"graphics font = 0x%08x, want 0x%08x",
			selected,
			smallFont,
		)
	}

	const text = "가가"
	width, err := runtime.Services.Text.Measure(
		runtime.ServiceOwner,
		smallService,
		text,
	)
	check(t, err)
	if width != 16 {
		t.Fatalf("small text width = %d, want 16", width)
	}
	state := runtime.Graphics[graphics]
	check(t, runtime.drawGraphicsTextShared(state, text, 0, 0, 0))
	right := -1
	for y := 0; y < state.Target.Bounds().Dy(); y++ {
		for x := 0; x < state.Target.Bounds().Dx(); x++ {
			if _, _, _, alpha := state.Target.At(x, y).RGBA(); alpha != 0 {
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
