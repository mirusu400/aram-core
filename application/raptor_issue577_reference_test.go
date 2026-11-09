package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// 창세기전3ep1 keeps its text in a string builder whose append-one-character
// path checks only that the character fits, then writes it with
// MC_knlSprintk(buf+len, "%c", c). On an exactly full buffer the terminating
// NUL goes one byte past the end. ARAM packed C allocations flush against each
// other, so that NUL zeroed the low byte of the next object's class-descriptor
// pointer. The next virtual call on that object jumped into data or to address
// 0. The report came from the 시반포트레스 weapon shop: the shop list's
// descriptor 0x014024b0 read back as 0x01402400 (issue #577).
//
// The script below skips the story to the 시반포트레스 map, enters the weapon
// shop, and then plays a fixed pseudo-random key sequence there. Before the
// fix it faulted at frame 9665 (ARM fetch at 0x00000000).
func TestRaptorIssue577ShopKeepsObjectHeadersIntact(t *testing.T) {
	path, data := findAuthorizedPackage(t, "6bc7f65e30221d9fed18723929a89ba765e83079f6e8f5ef6546a89b35544f99")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	factory := NewFactory()
	factory.NewCPU = fastestCPUFactory()
	factory.RunBudget = DefaultHandsetRunBudget
	factory.FrameRunBudget = DefaultHandsetRunBudget
	factory.RaptorFrameRunBudget = DefaultRaptorFrameRunBudget
	created, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(ctx))

	type key struct {
		control string
		pressed bool
	}
	script := map[int][]key{}
	tap := func(frame, hold int, control string) {
		script[frame] = append(script[frame], key{control, true})
		script[frame+hold] = append(script[frame+hold], key{control, false})
	}
	// OK through the story until the 시반포트레스 map, then 진입 and the
	// weapon shop.
	for frame := 100; frame <= 7980; frame += 40 {
		tap(frame, 4, "ok")
	}
	tap(8200, 4, "right")
	tap(8300, 4, "ok")
	tap(8500, 4, "ok")
	// Shop browsing: buy and sell lists open, scroll and close.
	controls := []string{"up", "down", "left", "right", "ok", "ok", "back"}
	seed := uint64(7)
	state := seed*2862933555777941757 + 3037000493
	for frame := 9100; frame <= 10000; frame += 7 {
		state = state*6364136223846793005 + 1442695040888963407
		tap(frame, 3, controls[(state>>33)%uint64(len(controls))])
	}

	for frame := 1; frame <= 10000; frame++ {
		for _, event := range script[frame] {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: event.control, Pressed: event.pressed}))
		}
		if err := machine.StepFrame(ctx); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
}
