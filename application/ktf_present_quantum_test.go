package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// mapleArcherSHA256 identifies the exact shipped 메이플스토리 궁수편 package the
// presentation-quantum allowance was measured against.
const mapleArcherSHA256 = "89c214dbd15dd6c12f7c7a0f6d6fa023ff43a6769be5fdbfced46f4c7b7a243c"

// findAuthorizedPackage returns the package in ARAM_TEST_DATA with the given
// digest, or skips when the corpus is not configured or does not hold it.
func findAuthorizedPackage(t *testing.T, digest string) (string, []byte) {
	t.Helper()
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	var (
		found string
		data  []byte
	)
	stop := errors.New("package selected")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".zip") {
			return nil
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != digest {
			return nil
		}
		found, data = path, payload
		return stop
	})
	if err != nil && !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if found == "" {
		t.Skipf("ARAM_TEST_DATA holds no package with digest %s", digest)
	}
	return found, data
}

// TestKTFPresentQuantumKeepsAPerElementMenuOnScreen covers issue #55. The
// title paints its main menu one element per Card.repaint, so a quantum that
// ended at the first submitted frame handed it roughly five hundred guest
// instructions per 16.67 ms against a budget of a million. The menu was still
// half-composed when the title's own two-second timer wiped it, and the player
// saw an almost empty screen with a single stray tile.
//
// The menu is fully on screen for 5 frames with an allowance of one, 61 with
// two, and 67 from four upward, so this asserts a floor well above the broken
// behaviour without pinning the exact count.
func TestKTFPresentQuantumKeepsAPerElementMenuOnScreen(t *testing.T) {
	path, data := findAuthorizedPackage(t, mapleArcherSHA256)

	factory := NewFactory()
	// Only the precise interpreter is required for determinism here; the
	// measurement is about scheduling, not about the CPU tier.
	factory.NewCPU = func() cpu.Backend { return interpreter.New() }
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The menu is the only screen in this window with a large coloured area:
	// the logos before it are greyscale on white and the screen after it is
	// the title card. Counting coloured samples therefore identifies it
	// without depending on exact pixels.
	const (
		frames    = 400
		threshold = 3000
		want      = 40
	)
	visible := 0
	for frame := 0; frame < frames; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		image := machine.Framebuffer()
		bounds := image.Bounds()
		coloured := 0
		for y := 24; y < bounds.Dy(); y += 4 {
			for x := 0; x < bounds.Dx(); x += 4 {
				r, g, b, _ := image.At(x, y).RGBA()
				if r != g || g != b {
					coloured++
				}
			}
		}
		if coloured >= threshold {
			visible++
		}
	}
	if visible < want {
		t.Fatalf(
			"main menu fully drawn in %d of %d frames, want at least %d",
			visible,
			frames,
			want,
		)
	}
}

// TestKTFPresentsPerQuantumStaysUnderTheJavaTaskLimit pins the ceiling the
// allowance was chosen against: 스파이더맨3 queues Java paint tasks faster than
// they retire, and at eight presents per quantum it overflows the task table
// with "KTF Java task limit 16 reached" before it draws a frame.
func TestKTFPresentsPerQuantumStaysUnderTheJavaTaskLimit(t *testing.T) {
	if ktfPresentsPerQuantumMax < 2 {
		t.Fatalf(
			"presents per quantum = %d; one ends the quantum at the first "+
				"submitted frame, which is what issue #55 fixed",
			ktfPresentsPerQuantumMax,
		)
	}
	if ktfPresentsPerQuantumMax >= 8 {
		t.Fatalf(
			"presents per quantum = %d; eight already overflows the Java task "+
				"table in 스파이더맨3",
			ktfPresentsPerQuantumMax,
		)
	}
}

// TestKTFInputStaysOffTheBusUntilTheCardCanTakeIt covers the regression the
// wider presentation quantum exposed. The shared event bus is strictly
// ordered and DrainServiceEvents leaves an input it cannot deliver at the
// head to retry, so an input that reaches the bus while the card is busy
// blocks every later event behind it - including the two lifecycle records
// each quantum writes and nothing consumes. 영웅서기1 filled the thousand-event
// queue that way in about five hundred frames and faulted with
// "event queue reached 1024". The machine's own gate must therefore ask
// exactly what the delivery path asks.
func TestKTFInputStaysOffTheBusUntilTheCardCanTakeIt(t *testing.T) {
	machine := newKTFQuantumMachine(t)
	runtime := machine.ktf

	// No display card yet: nothing may be handed to the bus.
	if runtime.CanQueueKeyEvent() {
		t.Fatal("a key event was accepted with no card on the display")
	}
	queued, err := runtime.QueueKeyEvent(true, 1)
	if err != nil {
		t.Fatal(err)
	}
	if queued {
		t.Fatal("QueueKeyEvent accepted a key with no card on the display")
	}

	// The two must never disagree: QueueKeyEvent is the delivery path and
	// CanQueueKeyEvent is what the machine gates on, so a state that one
	// accepts and the other refuses is what strands an input at the head of
	// the queue.
	if runtime.CanQueueKeyEvent() != func() bool {
		accepted, queueErr := runtime.QueueKeyEvent(true, 1)
		if queueErr != nil {
			t.Fatal(queueErr)
		}
		return accepted
	}() {
		t.Fatal("CanQueueKeyEvent disagrees with QueueKeyEvent")
	}
}

// heroSagaOneSHA256 identifies the shipped 영웅서기1 package. It repaints hard
// enough that its card is busy for most of a wider quantum, which is what made
// it the title that caught the input regression below.
const heroSagaOneSHA256 = "ead25bc6ffa7777862328ba6fa057911645d69ad47c2e8d922a1d545a1728d2d"

// TestKTFWiderQuantumStillDeliversInput is the test the corpus probe sweep
// cannot be: a probe barely presses anything, so it saw nothing wrong when the
// wider presentation quantum first landed. Offering input only once at the top
// of a quantum that now carries several frames left 영웅서기1's card busy every
// time it was asked, and the machine either stranded every key or filled the
// shared event queue with the lifecycle records piling up behind the stuck
// one and faulted with "event queue reached 1024".
func TestKTFWiderQuantumStillDeliversInput(t *testing.T) {
	path, data := findAuthorizedPackage(t, heroSagaOneSHA256)

	factory := NewFactory()
	factory.NewCPU = func() cpu.Backend { return interpreter.New() }
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := machine.ktf.SetTraceMode(ktfrt.KTFTraceFull); err != nil {
		t.Fatal(err)
	}

	const (
		frames    = 1200
		firstPunt = 200
		every     = 40
	)
	posted, delivered := 0, 0
	for frame := 0; frame < frames; frame++ {
		if frame >= firstPunt && frame%every == 0 {
			for _, pressed := range []bool{true, false} {
				if err := machine.QueueInput(machinecore.InputEvent{
					Control: "ok",
					Pressed: pressed,
				}); err != nil {
					t.Fatal(err)
				}
				posted++
			}
		}
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		for _, entry := range machine.ktf.HostTrace {
			if strings.Contains(entry, "java_key_event") {
				delivered++
			}
		}
		machine.ktf.HostTrace = machine.ktf.HostTrace[:0]
	}
	if delivered != posted {
		t.Fatalf(
			"delivered %d of %d posted key events (%d still queued)",
			delivered,
			posted,
			len(machine.input),
		)
	}
}

// TestKTFHeldKeyDoesNotFillTheEventQueue covers what random key fuzzing found
// on 133 of 218 KTF titles: hold a key and the machine faults with
// "runtime service limit exceeded: event queue reached 1024".
//
// Auto-repeat emits an event per period for every held control whether or not
// the previous one was consumed, and the drain leaves an input it cannot
// deliver at the head of the strictly ordered bus to retry. A card that is
// busy repainting therefore never takes one, and the repeats pile up behind it
// together with the lifecycle records each quantum writes. A repeat is the
// periodic "still held" signal, so dropping an undeliverable one is free; a
// press or a release still waits.
func TestKTFHeldKeyDoesNotFillTheEventQueue(t *testing.T) {
	path, data := findAuthorizedPackage(t, heroSagaOneSHA256)

	factory := NewFactory()
	factory.NewCPU = func() cpu.Backend { return interpreter.New() }
	factory.RunBudget = DefaultKTFHandsetRunBudget
	factory.KTFRunBudget = DefaultKTFHandsetRunBudget
	factory.FrameRunBudget = DefaultKTFHandsetRunBudget
	created, err := factory.Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(path),
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Press four controls and never release them, which is what a fuzz seed
	// that toggles randomly ends up doing and what a player leaning on the pad
	// does.
	for _, control := range []string{"ok", "left", "num5", "soft-right"} {
		if err := machine.QueueInput(machinecore.InputEvent{
			Control: control,
			Pressed: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for frame := 0; frame < 1500; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			if strings.Contains(err.Error(), "from stopped") {
				return
			}
			t.Fatalf("frame %d with four keys held: %v", frame, err)
		}
	}
}
