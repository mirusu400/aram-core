package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// 테일즈위버 루시안칼츠편 logs in with UserAuthentication and then a
// device-information request (0x28a0) when the player accepts the
// 고객정보 활용동의 notice. Its reply parser allocates the reply's declared
// length and reads [status][11-byte field][u16 message length][message] whatever
// the status says. ARAM's offline answer used to be a bare 9-byte success, so
// the parser took the message length from heap bytes past that allocation,
// copied 63519 bytes over its own .bss, and the next effect load faulted with
// `CSTDLIB.memcpy ... from lr 0x000216eb: invalid guest address` (issue #568).
// Pressing OK every two seconds walks the title from its logos through the
// consent notice, the login and the 게임인증 result past the frame that faulted.
func TestRaptorIssue568CarrierLoginKeepsTheClet(t *testing.T) {
	path, data := findAuthorizedPackage(t, "71191feb29e21ebc5e6c665c07c59eb20877555492207c07d0c4e7303811b933")

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

	// The overflow landed on the title's {data, data, size} descriptor for
	// i/effect.dat in .bss, which it fills once at startup. The fault only
	// shows when an effect is next loaded, so compare the descriptor itself
	// as well: it must survive the login unchanged.
	const effectDescriptor = 0x01516c60
	var loaded, current [12]byte
	for frame := 0; frame < 3200; frame++ {
		if frame >= 360 && frame%120 < 2 {
			check(t, machine.QueueInput(machinecore.InputEvent{
				Control: "ok", Pressed: frame%120 == 0,
			}))
		}
		if err := machine.StepFrame(ctx); err != nil {
			snapshot := machine.DebugSnapshot(16)
			t.Fatalf("frame %d: %v; last=%+v unimplemented=%v",
				frame, err, snapshot.LastResult, machine.wipi.Unimplemented)
		}
		if frame == 300 {
			check(t, machine.cpu.ReadMemory(effectDescriptor, loaded[:]))
			if loaded == ([12]byte{}) {
				t.Fatal("i/effect.dat descriptor is still empty after startup")
			}
		}
	}
	check(t, machine.cpu.ReadMemory(effectDescriptor, current[:]))
	if current != loaded {
		t.Fatalf("i/effect.dat descriptor changed across the login: % x, was % x", current, loaded)
	}
	// The login's device-information body is formatted with module-1 sprintf.
	if count := machine.wipi.Unimplemented["RAPTOR.module1#1015"]; count != 0 {
		t.Fatalf("module-1 sprintf left unimplemented (%d calls); the login body went out empty", count)
	}
	if machine.wipi.Stats.PresentCount == 0 {
		t.Fatal("title never presented a frame")
	}
}
