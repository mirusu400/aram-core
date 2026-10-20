package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestVerifiedNativeMinigameClearsGameplayAnimation(t *testing.T) {
	machine := openNativeGameReference(t, nativeGameReferences[0])
	step := func() {
		check(t, machine.StepFrame(context.Background()))
		for len(machine.DrainAudio().PCM16) > 0 {
		}
	}
	wait := func(count int) {
		for range count {
			step()
		}
	}
	press := func(control string) {
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		step()
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control}))
		step()
	}
	nonBlack := func(rect image.Rectangle) int {
		count := 0
		rect = rect.Intersect(machine.frame.Bounds())
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				pixel := machine.frame.RGBAAt(x, y)
				if pixel.R != 0 || pixel.G != 0 || pixel.B != 0 {
					count++
				}
			}
		}
		return count
	}

	wait(180)
	for range 4 {
		press("select")
		wait(180)
	}
	press("left")
	wait(60)
	press("select")
	wait(180)
	press("select")
	wait(120)
	if count := nonBlack(image.Rect(0, 0, 240, 103)); count == 0 {
		t.Fatal("game help screen was not visible before it was skipped")
	}

	press("clear")
	wait(30)
	if count := nonBlack(image.Rect(0, 0, 240, 103)); count != 0 {
		t.Fatalf("skipped help or READY animation remained above the bears: %d non-black pixels", count)
	}
	if pixel := machine.frame.RGBAAt(10, 250); pixel.R != 0 || pixel.G != 0 || pixel.B != 0 {
		t.Fatalf("skipped help panel remained at (10,250): %#v", pixel)
	}
}

// These observations belong to the exact records in nativeGameReferences.
// Read-only native counters distinguish a completed round from menu animation.
func TestVerifiedNativeMinigameCompletesRoundsAndKeepsPurchase(t *testing.T) {
	for _, reference := range nativeGameReferences {
		if reference.profile == minigame.LegacyProfileID {
			continue
		}
		t.Run(reference.path, func(t *testing.T) {
			machine := openNativeGameReference(t, reference)
			nonzeroAudio := 0
			step := func() {
				check(t, machine.StepFrame(context.Background()))
				for {
					chunk := machine.DrainAudio()
					if len(chunk.PCM16) == 0 {
						break
					}
					for _, sample := range chunk.PCM16 {
						if sample != 0 {
							nonzeroAudio++
						}
					}
				}
			}
			wait := func(n int) {
				for range n {
					step()
				}
			}
			word := func(address uint32) uint32 {
				var b [4]byte
				check(t, machine.cpu.ReadMemory(address, b[:]))
				return binary.LittleEndian.Uint32(b[:])
			}
			press := func(control string) {
				before, _ := machine.EADSFrameStats()
				check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
				delivered := false
				for range 64 {
					step()
					after, _ := machine.EADSFrameStats()
					if after.InputDispatchCount > before.InputDispatchCount {
						delivered = true
						break
					}
				}
				if !delivered {
					t.Fatalf("key %s was not dispatched", control)
				}
				check(t, machine.QueueInput(machinecore.InputEvent{Control: control}))
				step()
			}
			tap := func(x, y int) {
				before, _ := machine.EADSFrameStats()
				check(t, machine.QueuePointer(machinecore.PointerInputEvent{X: x, Y: y, Pressed: true}))
				for range 64 {
					step()
					after, _ := machine.EADSFrameStats()
					if after.InputDispatchCount > before.InputDispatchCount {
						break
					}
				}
				check(t, machine.QueuePointer(machinecore.PointerInputEvent{X: x, Y: y}))
				for range 64 {
					step()
					after, _ := machine.EADSFrameStats()
					if after.InputReleaseCount > before.InputReleaseCount {
						break
					}
				}
			}
			phase, counter, selected, fruit, score := uint32(0xf50000d8), uint32(0xf50000e4), uint32(0xf5000140), uint32(0xf503d38c), uint32(0xf503ea84)
			wait(180)
			if machine.SupportsPointer() {
				phase, counter, selected, fruit, score = 0xf50000e4, 0xf50000f0, 0xf500014c, 0xf50430e0, 0xf50447d8
				for _, p := range [][2]int{{120, 180}, {120, 116}, {26, 100}, {60, 270}, {26, 100}} {
					tap(p[0], p[1])
					wait(120)
				}
			} else {
				for _, control := range []string{"select", "select", "select", "select", "left", "select", "select"} {
					press(control)
					wait(180)
				}
			}
			completed := false
			rounds, lastScore := 0, word(score)
			lastPhase := uint32(0xffffffff)
			for range 1800 {
				currentScore := word(score)
				if currentScore > lastScore {
					rounds++
				}
				lastScore = currentScore
				if rounds >= 2 {
					completed = true
					break
				}
				p, c := word(phase), word(counter)
				if p != lastPhase {
					t.Logf("phase=%d counter=%d score=%d", p, c, word(score))
					lastPhase = p
				}
				if p == 1 && c >= 35 {
					if machine.SupportsPointer() {
						tap(120, 220)
					} else {
						press("clear")
					}
				}
				if p == 6 && c == 0 {
					best := 0
					for i := 1; i < 3; i++ {
						if word(fruit+uint32(i*4)) > word(fruit+uint32(best*4)) {
							best = i
						}
					}
					target := uint32(2 - best)
					if machine.SupportsPointer() {
						tap(75+int(target)*46, 200)
					} else {
						current := word(selected)
						control := "select"
						if current < target {
							control = "right"
						}
						if current > target {
							control = "left"
						}
						press(control)
					}
				}
				step()
			}
			if !completed {
				t.Fatalf("first game did not complete two rounds: phase=%d counter=%d score=%d", word(phase), word(counter), word(score))
			}
			if nonzeroAudio == 0 {
				t.Fatal("native gameplay produced no audible PCM")
			}
			backup, err := machine.ExportSaveData()
			check(t, err)
			var save saveDataEnvelope
			check(t, gob.NewDecoder(bytes.NewReader(backup)).Decode(&save))
			if len(save.Storage.Files) == 0 {
				t.Fatal("purchase did not create native save data")
			}
			check(t, machine.Reset(context.Background()))
			check(t, machine.Start(context.Background()))
			wait(180)
			reloaded, err := machine.ExportSaveData()
			check(t, err)
			var after saveDataEnvelope
			check(t, gob.NewDecoder(bytes.NewReader(reloaded)).Decode(&after))
			if len(after.Storage.Files) != len(save.Storage.Files) {
				t.Fatal("reset lost native save files")
			}
			for i, file := range save.Storage.Files {
				if !bytes.Equal(file.Data, after.Storage.Files[i].Data) {
					t.Fatal("reset changed persistent purchase data")
				}
			}
		})
	}
}
