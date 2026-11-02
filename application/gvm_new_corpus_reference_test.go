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
		{"nom", GVMNomOperationalSHA256, "c26eab371895ca07f442b67ed19cd5ebc0cea534632a9a996cb2a518e8cd7f66", "093620b85107d398a73df3e814c11cde3e0404c44ffa9067024fefb90eab471f"},
		{"fruit", GVMFruitOperationalSHA256, "1410764797778f32f56fb368c010c509fbce47ddc6d45fc135e060a70ff43cb7", "3d77b02fabd6d0c75656ca7d8375bbf53a1b045c365989b74056325ba4214eed"},
		{"mashi", GVMMashiOperationalSHA256, "8e62272cfb51cb5f202643b069d2c9fc317a5121b6291bb6553b94ff370640b3", "b3187a19b3128a9a3b68bac66f5b60e49f1f95d0120c0fc4dc1673f1045bb3f1"},
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
		{"fruit", GVMFruitAlternateSHA256, "1410764797778f32f56fb368c010c509fbce47ddc6d45fc135e060a70ff43cb7"},
		{"fantasy", GVMFantasyAlternateSHA256, "4da02bbb6369d4348bd5fa451a52ee339637f200f0440f09b8c909f45fd5455c"},
		{"north", GVMNorthAlternateSHA256, "b3fd21c314c7048cd31de4f69fe8086e621fc52483e4c47cc32b4dab6989abb0"},
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
		{"clean-time", GVMCleanTimeOperationalSHA256, "select", "0e626ad4f53c738099b238e6d417ca73325bcf821cf925e7102d9c37b9816be3", "1f8a199ca55c10aace75b2cf6513ff26b21aacd559d28d43b74144cf1424be62"},
		{"sword-master", GVMSwordMasterOperationalSHA256, "select", "b3698e7f970f077582d5bcb9fb2eac688e77fe156cb54ca28366690fcb3b87b3", "234065642b908906d4f82190d2e97e2ada2b96f88688692113c1625eecd32e44"},
		{"crow", GVMCrowOperationalSHA256, "select", "1b270fd9e954e206fa53e9a811782d16686a18a94e2377bbf96c751d96fe46f0", "dfddbd14d41af17c86e4fa1a387bfdcc68ddaa091b91584e40cafd303e54ca55"},
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
		{"", "b3fd21c314c7048cd31de4f69fe8086e621fc52483e4c47cc32b4dab6989abb0"},
		{"select", "ae4608207a3339164954a1ada01681bdd5c5a39576053133c87e10f943b32046"},
		{"up", "35b9335fada7828861ae839da5a0339d90b8f8dd7275684460deee0c4a674b1c"},
		{"down", "35b9335fada7828861ae839da5a0339d90b8f8dd7275684460deee0c4a674b1c"},
		{"num1", "35b9335fada7828861ae839da5a0339d90b8f8dd7275684460deee0c4a674b1c"},
		{"select", "c3d10f96ae2fcccfbaacd3f6a276999558d76101810afe94499dd052734859f2"},
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
