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
		{"nom", GVMNomOperationalSHA256, "b0965f771b5751c079c4dcb0206353997ef81297def6926fa8d595f462ed2e57", "ca01ef0c02026a3da37a1cf6a69d43541ebcfdd282d5436e769d5fb94247807c"},
		{"fruit", GVMFruitOperationalSHA256, "557304040f245962d5b825014670e627a6059de30e1dfa76dfb764350f2c998e", "21bdf0b2ae6b9a9dc70ae9ccb6509d8d6dd61560c82eb063d726aad20c09cedc"},
		{"mashi", GVMMashiOperationalSHA256, "4ac0c24bcded87a8ec4d713c6bd4c3e1ee07269155b6bb334a376592068ab815", "8a86dad2959c87a792ac29a239229332491bc6432bf3c53c9395902db1467749"},
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
		{"nom", GVMNomAlternateSHA256, "b0965f771b5751c079c4dcb0206353997ef81297def6926fa8d595f462ed2e57"},
		{"fruit", GVMFruitAlternateSHA256, "557304040f245962d5b825014670e627a6059de30e1dfa76dfb764350f2c998e"},
		{"fantasy", GVMFantasyAlternateSHA256, "c20bf73ba70ef80c90f394651d20e20cbc02e7628cb9ad454cc6ace71feaa83e"},
		{"north", GVMNorthAlternateSHA256, "5d6d64798c67f9187f0de2db624e5c3099ea83074924f1363ca58b0da8cff9ac"},
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
		{"kang-ho-dong", GVMKangHoDongOperationalSHA256, "num2", "1d7d8a824a9142dd848dea5fd93fd2928744058369fd0cffc380f99f0b64b5f4", "80c40414cf957a978004840deaf0d3aa2fe7445b392cea2915ebc13351010cc0"},
		{"clean-time", GVMCleanTimeOperationalSHA256, "select", "83d6cafd942a708017657123c75371139cba7bb05f09f06c3415229db271b2c0", "09d5ad8827a39c3e513e5ad7861f3d0f1ff3d2da892a6c130642c0fa0d234123"},
		{"sword-master", GVMSwordMasterOperationalSHA256, "select", "77cf92d2436a8498497d9f843a2720b1b50322d6400d789484f14f129b55d203", "96c9e75318744bad5939659b1b0130b7e3d878455ebd0e2ba8c076070d1d2bb8"},
		{"crow", GVMCrowOperationalSHA256, "select", "32992cc7aef3573b4fd459e19adec377b7f8905862bae85a545a89fe72fc5948", "e566f6bf76c66f3b62e16618918c5acdadc3952150523c373d44a32923ba0709"},
		{"hero-legend", GVMHeroLegendOperationalSHA256, "select", "f59cdd5856a3c1155c36fdc2d1a291c9dac59146c4706853bbc91917b180fdcf", "155ec8e12778d36b35b736fe0eedf46f643335070fc5a1d0bbbb374bd129e9e1"},
		{"fight-oinyong", GVMFightOinyongOperationalSHA256, "select", "4621348b398adc6ecf9b83e2f8f6db8524eca43e95e95483fde4fe48d610c117", "6e285b2bf3a510e945b67cef2f3dbd9157cfb478d2f661cfd12a99b9cb16a566"},
		{"fight-oinyong-2", GVMFightOinyong2OperationalSHA256, "select", "4621348b398adc6ecf9b83e2f8f6db8524eca43e95e95483fde4fe48d610c117", "6e285b2bf3a510e945b67cef2f3dbd9157cfb478d2f661cfd12a99b9cb16a566"},
		{"shining-stone", GVMShiningStoneOperationalSHA256, "select", "ec7f659a4f85af2708926fab1d7a614774e2bb0e54589e471868af01e6d2386a", "7d3c3d43671aa5ead50ca92572692da2657bc91fd15cd0cd2207d9c9db2ac8d7"},
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
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "c20bf73ba70ef80c90f394651d20e20cbc02e7628cb9ad454cc6ace71feaa83e" {
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
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "d627990a4f5de40a0abecbed794fece6d55b55867ab9c5e170d8076a3e75262b" {
		t.Fatalf("title frame = %s", got)
	}
	pressGVMCorpusKey(t, machine, "select")
	stepGVMCorpusFrames(t, machine, 120)
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != "9a70364bcb17078989e8970e8861e4d3715b3266508d1aecf55dedc3e57bffb6" {
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
		{"", "5d6d64798c67f9187f0de2db624e5c3099ea83074924f1363ca58b0da8cff9ac"},
		{"select", "c3423f8c80d2dc34c100db7337c62f9ca41f5407c13166422589b713b9f0d5ff"},
		{"up", "f612ff32cb3cfd3a297ad2ea8ad40edf53f031abb125a192b21f31f8d564e86e"},
		{"down", "f612ff32cb3cfd3a297ad2ea8ad40edf53f031abb125a192b21f31f8d564e86e"},
		{"num1", "f612ff32cb3cfd3a297ad2ea8ad40edf53f031abb125a192b21f31f8d564e86e"},
		{"select", "ee0eec152db18170f4530907283b782b6677ce59987d03807599ab96c8e9c960"},
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
	if got := machine.Framebuffer(); got == nil || got.Bounds().Size() != image.Pt(120, 80) {
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
