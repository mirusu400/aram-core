package application

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

func TestGVMNewOperationalCorpora(t *testing.T) {
	for _, tc := range []struct {
		name, digest, first, after string
	}{
		{"nom", GVMNomOperationalSHA256, "c26eab371895ca07f442b67ed19cd5ebc0cea534632a9a996cb2a518e8cd7f66", "5d9b7b37ba6ddc4636582cbddd5fbf1ec4a7b92347b9e4a76af0f6246aadf122"},
		{"fruit", GVMFruitOperationalSHA256, "568ee421bdcbe81a7b337a389984301042445828ef5a229334a45643bef72ef0", "a926b8eba3e3f042d959019d0600fe60b0aa2ecec8db5e3e32d3ace6c9597c52"},
		{"mashi", GVMMashiOperationalSHA256, "d53d6212bae6b44a7970acf42ae9614bb33192d443f1f37e8fa0fbef697fd548", "b3187a19b3128a9a3b68bac66f5b60e49f1f95d0120c0fc4dc1673f1045bb3f1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := newGVMCorpusMachine(t, tc.digest)
			ctx := context.Background()
			if err := machine.Start(ctx); err != nil {
				t.Fatal(err)
			}
			stepGVMCorpusFrames(t, machine, 120)
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != tc.first {
				t.Fatalf("initial frame = %s, want %s", got, tc.first)
			}
			pressGVMCorpusKey(t, machine, "select")
			stepGVMCorpusFrames(t, machine, 120)
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != tc.after {
				t.Fatalf("selected frame = %s, want %s", got, tc.after)
			}
		})
	}
}

func TestGVMAlternateOuterPackagesMatchQualifiedSGS(t *testing.T) {
	for _, tc := range []struct {
		name, digest, frame string
	}{
		{"nom", GVMNomAlternateSHA256, "c26eab371895ca07f442b67ed19cd5ebc0cea534632a9a996cb2a518e8cd7f66"},
		{"fruit", GVMFruitAlternateSHA256, "568ee421bdcbe81a7b337a389984301042445828ef5a229334a45643bef72ef0"},
		{"fantasy", GVMFantasyAlternateSHA256, "4da02bbb6369d4348bd5fa451a52ee339637f200f0440f09b8c909f45fd5455c"},
		{"north", GVMNorthAlternateSHA256, "e43e2eeaf616d35c57529c7a15918e15e1d39c24fadf1cebe27c0ee9f9b94a45"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := newGVMCorpusMachine(t, tc.digest)
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			stepGVMCorpusFrames(t, machine, 120)
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != tc.frame {
				t.Fatalf("frame = %s, want %s", got, tc.frame)
			}
		})
	}
}

func TestGVMExpandedOperationalCorpora(t *testing.T) {
	for _, tc := range []struct {
		name, digest, control, first, after string
	}{
		{"kang-ho-dong", GVMKangHoDongOperationalSHA256, "num2", "a3ecaf3789f974a8d6dd844b349297ea2a14ea7d1bbe993dd92fea4ae3fa325b", "ecc87feb79eba79b2c89c90fb74bcf7359700dc9d640cfa20c1b389327538bfb"},
		{"clean-time", GVMCleanTimeOperationalSHA256, "select", "8988012ff5e607b803281da36a09b46b2af875d91cd993368cf29236de750f21", "19a7269dfb88d60fd8f6f73d2fd6fcfd01d8032f2a2d9a0436fb8c81c7563a6e"},
		{"sword-master", GVMSwordMasterOperationalSHA256, "select", "9d258399e577e8178779ea8ce388e7bc922183c02d02a6c41004b75f6688d548", "e3e241697844bb4256ddf2d53511fad55fc3b5d02532206c06e674c0c7dbc857"},
		{"hero-legend", GVMHeroLegendOperationalSHA256, "select", "7f65a2f0b4c6905c8b1c45527c9af400fadb0228a1b9dc263713195ab589fc4e", "fa0a0afb1fe4a9269cc2be168fa44a3da93ce62a5cb2e5afa89f0ed82ef0fd1b"},
		{"fight-oinyong", GVMFightOinyongOperationalSHA256, "select", "673d68fe5e816dbc3210beef054a2f2aaddcfa4c22eed8628f9aef8091dbbca0", "f335a62ebe7bb05ace932b53a1e2b14045e8c3a0608c8352647e1d7df3ef03ca"},
		{"fight-oinyong-2", GVMFightOinyong2OperationalSHA256, "select", "673d68fe5e816dbc3210beef054a2f2aaddcfa4c22eed8628f9aef8091dbbca0", "f335a62ebe7bb05ace932b53a1e2b14045e8c3a0608c8352647e1d7df3ef03ca"},
		{"shining-stone", GVMShiningStoneOperationalSHA256, "select", "d094f665c8b04ff244b1af11dcec280458b96423c62456ef3da294a7b4148e00", "2a8dd153bf6a1cdf94672ffc43e2a100be57dd7e2dff05c2d6e34179beefcda9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := newGVMCorpusMachine(t, tc.digest)
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			stepGVMCorpusFrames(t, machine, 120)
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != tc.first {
				t.Fatalf("initial frame = %s, want %s", got, tc.first)
			}
			pressGVMCorpusKey(t, machine, tc.control)
			stepGVMCorpusFrames(t, machine, 120)
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != tc.after {
				t.Fatalf("after %s frame = %s, want %s", tc.control, got, tc.after)
			}
		})
	}
}

// Crow holds its publisher logo for five seconds of guest timer ticks, and
// selecting its first menu entry opens the prologue download page, so the
// check moves the title menu cursor instead.
func TestGVMCrowTitleMenu(t *testing.T) {
	machine := newGVMCorpusMachine(t, GVMCrowOperationalSHA256)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stepGVMCorpusFrames(t, machine, 360)
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "1b270fd9e954e206fa53e9a811782d16686a18a94e2377bbf96c751d96fe46f0" {
		t.Fatalf("title frame = %s, want %s", got, "1b270fd9e954e206fa53e9a811782d16686a18a94e2377bbf96c751d96fe46f0")
	}
	pressGVMCorpusKey(t, machine, "down")
	stepGVMCorpusFrames(t, machine, 60)
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "eceb36c899b8ae3d5a31b2075786d8965e44101e75d3d67e7706521b2cc5c9f9" {
		t.Fatalf("after down frame = %s, want %s", got, "eceb36c899b8ae3d5a31b2075786d8965e44101e75d3d67e7706521b2cc5c9f9")
	}
}

func TestGVMDirectSoftKeyDispatch(t *testing.T) {
	machine := newGVMCorpusMachine(t, GVMHeroLegendOperationalSHA256)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stepGVMCorpusFrames(t, machine, 120)
	pressGVMCorpusKey(t, machine, "soft-left")
	diagnostics := machine.(interface {
		GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
	}).GVMInputDispatchDiagnostics()
	if diagnostics.DispatchCount != 1 || diagnostics.GuestCode != 13 || diagnostics.Result.Err != nil {
		t.Fatalf("soft-key dispatch = %+v", diagnostics)
	}
}

func TestGVMFantasyOperationalProgression(t *testing.T) {
	machine := newGVMCorpusMachine(t, GVMFantasyOperationalSHA256)
	ctx := context.Background()
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stepGVMCorpusFrames(t, machine, 120)
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "4da02bbb6369d4348bd5fa451a52ee339637f200f0440f09b8c909f45fd5455c" {
		t.Fatalf("opening frame = %s", got)
	}
	pressGVMCorpusKey(t, machine, "select")
	stepGVMCorpusFrames(t, machine, 120)
	before := brewFrameHash(machine.Framebuffer())
	for _, key := range []string{"up", "down", "left", "right", "num1", "num2"} {
		pressGVMCorpusKey(t, machine, key)
		stepGVMCorpusFrames(t, machine, 5)
	}
	after := brewFrameHash(machine.Framebuffer())
	if before == after || machine.(interface{ GVMPresentCount() uint64 }).GVMPresentCount() < 40 {
		t.Fatalf("fantasy did not advance: before=%x after=%x presents=%d", before, after, machine.(interface{ GVMPresentCount() uint64 }).GVMPresentCount())
	}
}

func TestGVMRepeatedSelectionWithHeldFrames(t *testing.T) {
	for _, tc := range []struct{ name, digest string }{
		{"mashi", GVMMashiOperationalSHA256},
		{"ragnarok", GVMRagnarokOperationalSHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := newGVMCorpusMachine(t, tc.digest)
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			step := func(frames int) {
				t.Helper()
				for frame := 0; frame < frames; frame++ {
					if err := machine.StepFrame(context.Background()); err != nil {
						t.Fatalf("frame %d: %v", frame, err)
					}
				}
			}
			step(120)
			initial := brewFrameHash(machine.Framebuffer())
			for i := 0; i < 20; i++ {
				if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
					t.Fatalf("selection %d press: %v", i+1, err)
				}
				step(5)
				if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}); err != nil {
					t.Fatalf("selection %d release: %v", i+1, err)
				}
				step(5)
			}
			if got := machine.(interface {
				GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
			}).GVMInputDispatchDiagnostics(); got.DispatchCount != 20 {
				t.Fatalf("input dispatch = %+v", got)
			}
			if after := brewFrameHash(machine.Framebuffer()); after == initial {
				t.Fatal("repeated selection did not advance the screen")
			}
		})
	}
}

func TestGVMRepeatedSelectionSurvivesLongIdle(t *testing.T) {
	for _, tc := range []struct{ name, digest string }{
		{"nom", GVMNomOperationalSHA256},
		{"jjayo", GVMOperationalSHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := newGVMCorpusMachine(t, tc.digest)
			if err := machine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			stepGVMCorpusFrames(t, machine, 120)
			initial := brewFrameHash(machine.Framebuffer())
			for i := 0; i < 20; i++ {
				if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}); err != nil {
					t.Fatalf("selection %d press: %v", i+1, err)
				}
				stepGVMCorpusFrames(t, machine, 5)
				if err := machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}); err != nil {
					t.Fatalf("selection %d release: %v", i+1, err)
				}
				stepGVMCorpusFrames(t, machine, 5)
			}
			stepGVMCorpusFrames(t, machine, 800)
			if got := machine.(interface {
				GVMInputDispatchDiagnostics() GVMInputDispatchDiagnostics
			}).GVMInputDispatchDiagnostics(); got.DispatchCount != 20 {
				t.Fatalf("input dispatch = %+v", got)
			}
			if after := brewFrameHash(machine.Framebuffer()); after == initial {
				t.Fatal("repeated selection did not advance the screen")
			}
		})
	}
}

func TestGVMStripOperationalProgression(t *testing.T) {
	machine := newGVMCorpusMachine(t, GVMStripOperationalSHA256)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	stepGVMCorpusFrames(t, machine, 120)
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "52229ee6a9c54190baee14e6393b4b6173b2adb016f186b3eaa2afe7f8c402fb" {
		t.Fatalf("title frame = %s", got)
	}
	pressGVMCorpusKey(t, machine, "select")
	stepGVMCorpusFrames(t, machine, 120)
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "835ac7e81586e2038f6c02b3c5f8bfc389fdd1263e49c9cd54dc50f269acf925" {
		t.Fatalf("menu frame = %s", got)
	}
	before := brewFrameHash(machine.Framebuffer())
	pressGVMCorpusKey(t, machine, "select")
	stepGVMCorpusFrames(t, machine, 120)
	if after := brewFrameHash(machine.Framebuffer()); after == before {
		t.Fatal("selection did not advance the menu")
	}
}

func TestGVMNorthOperationalProgression(t *testing.T) {
	machine := newGVMCorpusMachine(t, GVMNorthOperationalSHA256)
	if err := machine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, want string
	}{
		{"", "e43e2eeaf616d35c57529c7a15918e15e1d39c24fadf1cebe27c0ee9f9b94a45"},
		{"select", "0ac7fa926cbf58b3dd9043b7cff8d28e8d52d0e65d99f1b69bdecaa89ec3ee93"},
		{"up", "faac5d44188ef608a65c5c848598ce0b15d9ccebdbc6991844c6a8f73bedb6ae"},
		{"down", "831e7bd967d7bce597c2595f12725cf747fe1e9b46a473f626934342880c7a55"},
		{"num1", "26fad3eefdd4a4e3717575259e44d4fc1a7dfac3dc4fd852995f4d0ff2c3905c"},
		{"select", "b3fd21c314c7048cd31de4f69fe8086e621fc52483e4c47cc32b4dab6989abb0"},
	} {
		if tc.key != "" {
			pressGVMCorpusKey(t, machine, tc.key)
		}
		stepGVMCorpusFrames(t, machine, 120)
		if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != tc.want {
			t.Fatalf("after %q frame = %s, want %s", tc.key, got, tc.want)
		}
	}
}

func newGVMCorpusMachine(t *testing.T, digest string) machinecore.Machine {
	t.Helper()
	path, data := findAuthorizedPackage(t, digest)
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		Format: string(loader.KindGNEX),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { machine.Close() })
	if machine.(*gvmMachine).SourceInfo().ProfileID != GVMOperationalProfileID {
		t.Fatalf("profile = %q", machine.(*gvmMachine).SourceInfo().ProfileID)
	}
	return machine
}

func stepGVMCorpusFrames(t *testing.T, machine machinecore.Machine, frames int) {
	t.Helper()
	for frame := 0; frame < frames; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
	}
	if got := machine.Framebuffer(); got == nil || got.Bounds().Size() != image.Pt(int(operationalGVMWidth), int(operationalGVMHeight)) {
		t.Fatalf("frame bounds = %v", got)
	}
}

func pressGVMCorpusKey(t *testing.T, machine machinecore.Machine, key string) {
	t.Helper()
	for _, pressed := range []bool{true, false} {
		if err := machine.QueueInput(machinecore.InputEvent{Control: key, Pressed: pressed}); err != nil {
			t.Fatalf("%s pressed=%v: %v", key, pressed, err)
		}
	}
}
