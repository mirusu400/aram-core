package application

import (
	"encoding/hex"
	"image"
	"image/draw"
	"testing"
	"time"
)

func TestBREWIssue554NarniaDecodesPackedSceneLabel(t *testing.T) {
	const digest = "80c23d63c7c177b13ae59a69b8560e3395ac14b3f21348debc9d8bd1f3f041f9"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, machine, []brewReportedKeyHold{
		{"ok", 1616, 1712}, {"ok", 1984, 2048}, {"ok", 4400, 4512},
		{"ok", 5632, 5744}, {"ok", 6320, 6416}, {"down", 11568, 11648},
		{"ok", 11904, 12016}, {"down", 12688, 12768}, {"down", 12880, 12976},
		{"down", 13040, 13152}, {"down", 13216, 13296}, {"down", 13376, 13472},
		{"back", 13760, 13840}, {"down", 14128, 14224}, {"down", 14480, 14544},
		{"up", 15392, 15472}, {"ok", 15568, 15632}, {"back", 16800, 16880},
		{"up", 17200, 17264}, {"up", 17568, 17648}, {"ok", 17776, 17856},
	})
	stepBREWReference(t, machine, 1297)
	got := brewFrameHash(machine.Framebuffer())
	if encoded := hex.EncodeToString(got[:]); encoded != "6923f4d7b9489455ee8c6c1fe90ac28634443b2c9aea423174acec1da17a2dd3" {
		t.Fatalf("corrected Narnia scene frame hash = %s", encoded)
	}
}

func TestBREWIssue555LegacyMovementStartsOnPress(t *testing.T) {
	const digest = "c02863ffe4b9d6e7caeb9e0ab55f5d863e3887f91fe3dc654908c652c14b8d88"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, machine, []brewReportedKeyHold{
		{"ok", 2256, 2336}, {"ok", 5824, 5904}, {"right", 8384, 8720},
	})
	stepBREWUntil(t, machine, 8368*time.Millisecond)
	before := brewFrameHash(machine.Framebuffer())
	legacyBefore := machine.runtime.EventCount(0x100)
	stepBREWUntil(t, machine, 8384*time.Millisecond)
	if got := machine.runtime.EventCount(0x100); got != legacyBefore+1 {
		t.Fatalf("legacy key count on press = %d, want %d", got, legacyBefore+1)
	}
	stepBREWUntil(t, machine, 8544*time.Millisecond)
	if after := brewFrameHash(machine.Framebuffer()); after == before {
		t.Fatal("game board did not begin moving while right remained pressed")
	}
	legacyBefore = machine.runtime.EventCount(0x100)
	stepBREWUntil(t, machine, 8720*time.Millisecond)
	if got := machine.runtime.EventCount(0x100); got != legacyBefore {
		t.Fatalf("release emitted an extra legacy key: %d -> %d", legacyBefore, got)
	}
}

func TestBREWIssue556MagicianDecodesShortPackedLabel(t *testing.T) {
	const digest = "3da1ea7b8e834ef11df146c8a0294577a34659a17b0ce83deefffe4359c65566"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, machine, []brewReportedKeyHold{
		{"ok", 1312, 1376}, {"ok", 3664, 3776}, {"down", 5200, 5280},
		{"ok", 5488, 5584}, {"ok", 6176, 6272}, {"ok", 6832, 6928},
		{"right", 7328, 7424}, {"right", 7904, 8000}, {"ok", 8272, 8368},
		{"ok", 8912, 9008}, {"ok", 9536, 9616}, {"ok", 11504, 11584},
		{"ok", 11824, 11904}, {"ok", 12000, 12080},
	})
	stepBREWReference(t, machine, 1381)
	got := brewFrameHash(machine.Framebuffer())
	if encoded := hex.EncodeToString(got[:]); encoded != "dfc76acfc2a454d6e315bab138fc811a1af1be52976b14be0d3adba893b06c17" {
		t.Fatalf("corrected Magician tip frame hash = %s", encoded)
	}
}

func TestBREWIssue557MovingMatchClearsPreviousPosition(t *testing.T) {
	const digest = "78ef4ab84a84d285c72bbf471b9f303ff542dbb425dffc188b81f7e59c08ad11"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, machine, []brewReportedKeyHold{
		{"ok", 2592, 2688}, {"num2", 4800, 4880}, {"ok", 6000, 6112},
	})
	var changed []*image.RGBA
	last := brewFrameHash(machine.Framebuffer())
	for machine.now < 10*time.Second {
		stepBREWReference(t, machine, 1)
		if machine.now < 6*time.Second {
			continue
		}
		hash := brewFrameHash(machine.Framebuffer())
		if hash == last {
			continue
		}
		last = hash
		frame := image.NewRGBA(machine.Framebuffer().Bounds())
		draw.Draw(frame, frame.Bounds(), machine.Framebuffer(), image.Point{}, draw.Src)
		changed = append(changed, frame)
	}
	if len(changed) < 6 {
		t.Fatalf("only %d changed frames after entering the game", len(changed))
	}
	background, first, next := changed[3], changed[4], changed[5]
	cleared, formerlyPainted := 0, 0
	for y := 22; y < 29; y++ {
		for x := 2; x < 4; x++ {
			if first.RGBAAt(x, y) != background.RGBAAt(x, y) {
				formerlyPainted++
			}
			if next.RGBAAt(x, y) == background.RGBAAt(x, y) {
				cleared++
			}
		}
	}
	if formerlyPainted < 10 || cleared != 14 {
		t.Fatalf("moving match clear: painted=%d/14 restored=%d/14", formerlyPainted, cleared)
	}
}

func TestBREWIssue558SelectionChangesOnPress(t *testing.T) {
	const digest = "9d17a101d5c9e31e789698f31fb41ba5448b624eb63b37193be13b12fdbb46c0"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	queueBREWReportedInputs(t, machine, []brewReportedKeyHold{
		{"ok", 768, 864}, {"ok", 1856, 1936}, {"down", 3072, 3168},
	})
	stepBREWUntil(t, machine, 3056*time.Millisecond)
	before := brewFrameHash(machine.Framebuffer())
	legacyBefore := machine.runtime.EventCount(0x100)
	stepBREWUntil(t, machine, 3072*time.Millisecond)
	if got := machine.runtime.EventCount(0x100); got != legacyBefore+1 {
		t.Fatalf("legacy key count on press = %d, want %d", got, legacyBefore+1)
	}
	stepBREWUntil(t, machine, 3152*time.Millisecond)
	pressed := brewFrameHash(machine.Framebuffer())
	if pressed == before {
		t.Fatal("bookshelf selection did not change on key press")
	}
	stepBREWUntil(t, machine, 3168*time.Millisecond)
	if released := brewFrameHash(machine.Framebuffer()); released != pressed {
		t.Fatal("bookshelf selection changed again on key release")
	}
}

func stepBREWUntil(t *testing.T, machine *brewMachine, target time.Duration) {
	t.Helper()
	for machine.now < target {
		stepBREWReference(t, machine, 1)
	}
}
