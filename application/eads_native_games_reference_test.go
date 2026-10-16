package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
)

type nativeGameReference struct{ path, hash, profile string }

var nativeGameReferences = []nativeGameReference{
	{"SCH-W830_DL21/MinigameQVGAOEM.dat", "c83d4f4ee9cbf6ba4280b17f8f8a3dd26157ce9f07849583c04a60ebf9c00429", minigame.ProfileID},
	{"SCH-W860_DA06/MinigameQVGAOEM.dat", "6b48455bc86e151967d75ec1b596cdd7a09838e495d3a8a1717bd5d67158ee71", "wipi-1.2.1/skt/samsung/sch-w860/minigame-qvga-oem"},
	{"Bomb_Link/Bomb_Link.dat", "a8c47d3045639b12afd0de213a1c2ef1467f0495c8edad1bb5483a3ab74dc629", minigame.LegacyProfileID},
	{"SCH-W770_DA05/Minigame_WQVGA.dat", "16f0ccdf370069e2342aee29c05e22fa64577b7ca5e9d7cc87a98d87b50d391f", "wipi-1.2.1/skt/samsung/sch-w770/minigame-wqvga"},
}

func openNativeGameReference(t *testing.T, test nativeGameReference) *Machine {
	t.Helper()
	root := os.Getenv("ARAM_NATIVE_EADS_DATA")
	if root == "" {
		t.Skip("ARAM_NATIVE_EADS_DATA is not set")
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(test.path)))
	check(t, err)
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != test.hash {
		t.Fatalf("unexpected reference digest %s", got)
	}
	factory := NewFactory()
	factory.FrameRunBudget = 2_000_000
	created, err := factory.Create(context.Background(), machinecore.Source{Name: test.path, ReaderAt: bytes.NewReader(data), Size: int64(len(data))})
	check(t, err)
	t.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)
	if machine.info.ProfileID != test.profile {
		t.Fatalf("profile=%q, want %q", machine.info.ProfileID, test.profile)
	}
	check(t, machine.Start(context.Background()))
	return machine
}

func TestExtractedNativeGamesUseVerifiedRuntime(t *testing.T) {
	for _, test := range nativeGameReferences {
		t.Run(test.path, func(t *testing.T) {
			machine := openNativeGameReference(t, test)
			for range 180 {
				check(t, machine.StepFrame(context.Background()))
			}
			before, _ := machine.EADSFrameStats()
			wantEvent := uint32(5)
			if test.profile == minigame.LegacyProfileID {
				wantEvent = 4
			}
			for _, pressed := range []bool{true, false} {
				check(t, machine.QueueInput(machinecore.InputEvent{Control: "5", Pressed: pressed}))
				check(t, machine.StepFrame(context.Background()))
			}
			after, ok := machine.EADSFrameStats()
			if !ok || after.PresentCount == 0 || len(after.Events) <= len(before.Events) {
				t.Fatal("native game stopped before accepting input")
			}
			found := false
			for _, event := range after.Events[len(before.Events):] {
				if event.Event == wantEvent {
					found = true
				}
			}
			if !found {
				t.Fatal("guest key handler was not executed")
			}
			var state bytes.Buffer
			check(t, machine.SaveState(&state))
			check(t, machine.StepFrame(context.Background()))
			check(t, machine.LoadState(bytes.NewReader(state.Bytes())))
			check(t, machine.StepFrame(context.Background()))
		})
	}
}
