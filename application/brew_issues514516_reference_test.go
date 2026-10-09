package application

import (
	"context"
	"image"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// These optional regressions read exact authorized packages in place. Only
// identities and input timings from the issue reports are retained here.
func TestBREWIssue515TitleCompositesOldTransparentSprites(t *testing.T) {
	path, data := findAuthorizedPackage(t, "37a96c10f96c50150a6af3f7a8f0d5588867d7dd1019b48968aec31b26926c76")
	m := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, m, []brewReportedKeyHold{{"ok", 1152, 1296}})
	stepBREWReference(t, m, 394)
	frame := m.Framebuffer()
	magenta := 0
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r>>8 == 255 && g>>8 == 0 && b>>8 == 255 {
				magenta++
			}
		}
	}
	if magenta != 0 {
		t.Fatalf("title has %d literal magenta color-key pixels", magenta)
	}
	if colored := brewColoredPixelCount(frame); colored < 500 {
		t.Fatalf("title artwork is missing: only %d colored pixels", colored)
	}
}

func TestBREWIssue516NativeMenuArtworkSurvivesNavigation(t *testing.T) {
	path, data := findAuthorizedPackage(t, "1e00e14ec15468d8509eafdf58dd957c0c0f5cecaf6c922e81f8dcc64bf1177f")
	m := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, m, []brewReportedKeyHold{
		{"ok", 1648, 1824}, {"down", 4288, 4416}, {"down", 4768, 4896},
		{"up", 5824, 6016}, {"up", 6096, 6256}, {"down", 8816, 8912},
		{"down", 9136, 9232}, {"ok", 9616, 9712},
		{"down", 10400, 10544}, {"down", 10864, 10976},
	})
	stepBREWReference(t, m, 250)
	menu := brewFrameHash(m.Framebuffer())
	if colored := brewColoredPixelCount(m.Framebuffer()); colored < 5000 {
		t.Fatalf("main menu has only %d colored pixels; native artwork is missing", colored)
	}
	stepBREWReference(t, m, 450)
	if after := brewFrameHash(m.Framebuffer()); after == menu {
		t.Fatal("settings navigation left the main menu unchanged")
	}
	if colored := brewColoredPixelCount(m.Framebuffer()); colored < 5000 {
		t.Fatalf("settings has only %d colored pixels; native artwork is missing", colored)
	}
}

func TestBREWIssue514SoundRestartsAfterDialogue(t *testing.T) {
	path, data := findAuthorizedPackage(t, "ae471911376dface7ded70c917b60b6b8b1eceab2e58c3f9cba4582731131851")
	m := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, m, []brewReportedKeyHold{
		{"ok", 1520, 1632}, {"ok", 2560, 2688}, {"down", 3824, 4016},
		{"down", 4304, 4464}, {"down", 4576, 4736}, {"down", 4864, 4992},
		{"down", 5136, 5264}, {"down", 5456, 5616}, {"down", 6032, 6192},
		{"down", 6592, 6784}, {"down", 7344, 7504}, {"up", 8272, 8384},
		{"up", 8496, 8656}, {"up", 8752, 8896}, {"up", 8976, 9136},
		{"up", 9232, 9392}, {"up", 9504, 9664}, {"up", 10016, 10176},
		{"up", 10464, 10592}, {"ok", 11424, 11584}, {"ok", 12624, 12784},
		{"ok", 13712, 13872}, {"down", 14752, 14976}, {"right", 15184, 15872},
		{"up", 16224, 16352}, {"right", 16576, 16736}, {"up", 18576, 18736},
		{"right", 18912, 19104}, {"ok", 19536, 19712}, {"ok", 21072, 21216},
		{"ok", 21904, 22032}, {"down", 22416, 22544}, {"right", 22864, 23056},
		{"left", 24496, 24704}, {"up", 25696, 25856}, {"ok", 25984, 26192},
		{"ok", 27104, 27296}, {"ok", 27616, 27792}, {"left", 27984, 28192},
		{"down", 28416, 28624}, {"down", 32736, 32944}, {"left", 33376, 33552},
		{"ok", 33584, 33824}, {"ok", 34752, 34976}, {"ok", 35552, 35776},
		{"ok", 36224, 36464}, {"ok", 36912, 37104},
	})
	// The title has its own transition delay. Check sustained PCM in two later
	// music spans, including one after a natural completion, rather than a tap.
	var audible [2]int
	for frame := 1; frame <= 4375; frame++ {
		if err := m.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		audio := m.DrainAudio()
		if err := audio.Validate(); err != nil {
			t.Fatal(err)
		}
		ms := frame * 16
		window := -1
		if ms >= 48000 && ms < 50000 {
			window = 0
		} else if ms >= 66500 && ms < 69000 {
			window = 1
		}
		if window >= 0 {
			for _, sample := range audio.PCM16 {
				if sample != 0 {
					audible[window]++
					break
				}
			}
		}
	}
	for window, count := range audible {
		if count < 100 {
			t.Fatalf("post-dialogue music span %d has only %d audible frames", window, count)
		}
	}
}

type brewReportedKeyHold struct {
	control            string
	pressMS, releaseMS int
}

func queueBREWReportedInputs(t *testing.T, m *brewMachine, holds []brewReportedKeyHold) {
	t.Helper()
	for _, hold := range holds {
		for _, event := range []machinecore.InputEvent{
			{Control: hold.control, Pressed: true, At: time.Duration(hold.pressMS) * time.Millisecond},
			{Control: hold.control, Pressed: false, At: time.Duration(hold.releaseMS) * time.Millisecond},
		} {
			if err := m.QueueInput(event); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func brewColoredPixelCount(frame image.Image) int {
	count := 0
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r != g || g != b {
				count++
			}
		}
	}
	return count
}
