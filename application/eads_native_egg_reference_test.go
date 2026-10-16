package application

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestVerifiedNativeEggGameReturnsAfterPopulatingItsGrid(t *testing.T) {
	for _, reference := range nativeGameReferences {
		if reference.profile == minigame.LegacyProfileID {
			continue
		}
		t.Run(reference.path, func(t *testing.T) {
			m := openNativeGameReference(t, reference)
			step := func() {
				check(t, m.StepFrame(context.Background()))
				for len(m.DrainAudio().PCM16) > 0 {
				}
			}
			wait := func(n int) {
				for range n {
					step()
				}
			}
			press := func(key string) {
				before, _ := m.EADSFrameStats()
				check(t, m.QueueInput(machinecore.InputEvent{Control: key, Pressed: true}))
				defer func() { check(t, m.QueueInput(machinecore.InputEvent{Control: key})); step() }()
				for range 64 {
					step()
					after, _ := m.EADSFrameStats()
					if after.InputDispatchCount > before.InputDispatchCount {
						return
					}
				}
				t.Fatalf("egg grid construction prevented key %s from dispatching", key)
			}
			wait(180)
			if m.SupportsPointer() {
				for _, point := range [][2]int{{120, 180}, {120, 116}, {26, 260}, {26, 260}, {60, 270}, {26, 260}} {
					check(t, m.QueuePointer(machinecore.PointerInputEvent{X: point[0], Y: point[1], Pressed: true}))
					step()
					check(t, m.QueuePointer(machinecore.PointerInputEvent{X: point[0], Y: point[1]}))
					wait(120)
				}
			} else {
				for range 3 {
					press("select")
					wait(180)
				}
				for range 5 {
					press("right")
					wait(30)
				}
				for _, key := range []string{"select", "left", "select", "select"} {
					press(key)
					wait(180)
				}
			}
			stateAddr := uint32(0xf50000cc)
			if m.SupportsPointer() {
				stateAddr = 0xf50000d8
			}
			var state [4]byte
			check(t, m.cpu.ReadMemory(stateAddr, state[:]))
			if got := binary.LittleEndian.Uint32(state[:]); got != 15 {
				t.Fatalf("selected native game %d, want egg game 15", got)
			}
			m.frameRunBudget = 200_000
			press("clear")
			wait(60)
			before, _ := m.EADSFrameStats()
			press("left")
			press("right")
			wait(30)
			after, _ := m.EADSFrameStats()
			if after.InputDispatchCount-before.InputDispatchCount < 2 || after.PresentCount <= before.PresentCount {
				t.Fatal("egg game failed to continue after building the random grid")
			}
		})
	}
}
