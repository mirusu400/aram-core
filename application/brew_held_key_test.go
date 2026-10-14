package application

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
)

func newBREWKeyTestMachine(t *testing.T) *brewMachine {
	t.Helper()
	// A synthetic module constructs an applet whose event handler returns true.
	module := make([]byte, 0x88)
	for offset, words := range map[int][]uint32{
		0:    {0xe59f0018, 0xe59f1018, 0xe5801000, 0xe5820000, 0xe3a00000, 0xe12fff1e},
		0x20: {0x03000000, 0x00010030},
		0x30: {0, 0, 0x00010040},
		0x40: {0xe59f000c, 0xe59f100c, 0xe5801000, 0xe5830000, 0xe12fff1e, 0x03000040, 0x00010060},
		0x60: {0, 0, 0x00010080},
		0x80: {0xe3a00001, 0xe12fff1e},
	} {
		for i, word := range words {
			binary.LittleEndian.PutUint32(module[offset+i*4:], word)
		}
	}
	m := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{Module: module, ClassIDs: []uint32{0x01023456}})
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBREWHeldKeyDeliversLegacyEventsBeforeRelease(t *testing.T) {
	m := newBREWKeyTestMachine(t)
	check(t, m.QueueInput(machinecore.InputEvent{Control: "right", Pressed: true}))
	stepBREWReference(t, m, 1)
	if got := m.runtime.EventCount(0x100); got != 1 {
		t.Fatalf("EVT_KEY before release = %d, want 1", got)
	}
	stepBREWReference(t, m, 60)
	if got := m.runtime.EventCount(0x100); got < 3 {
		t.Fatalf("held EVT_KEY count = %d, want repeated input", got)
	}
	check(t, m.QueueInput(machinecore.InputEvent{Control: "right"}))
	stepBREWReference(t, m, 1)
	before := m.runtime.EventCount(0x100)
	stepBREWReference(t, m, 60)
	if got := m.runtime.EventCount(0x100); got != before {
		t.Fatalf("released key repeated: %d -> %d", before, got)
	}
	if got := m.runtime.EventCount(0x101); got != 1 {
		t.Fatalf("EVT_KEY_PRESS = %d, want 1", got)
	}
	if got := m.runtime.EventCount(0x102); got != 1 {
		t.Fatalf("EVT_KEY_RELEASE = %d, want 1", got)
	}
}

func TestBREWShortTapDeliversOneLegacyEvent(t *testing.T) {
	m := newBREWKeyTestMachine(t)
	check(t, m.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}))
	check(t, m.QueueInput(machinecore.InputEvent{Control: "ok", At: m.now + time.Millisecond}))
	stepBREWReference(t, m, 60)
	if got := m.runtime.EventCount(0x100); got != 1 {
		t.Fatalf("short tap EVT_KEY = %d, want 1", got)
	}
}

func TestBREWHeldKeysUseGuestTimeAndVirtualKeyAliases(t *testing.T) {
	m := newBREWKeyTestMachine(t)
	check(t, m.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}))
	check(t, m.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}))
	check(t, m.QueueInput(machinecore.InputEvent{Control: "left", Pressed: true}))
	stepBREWReference(t, m, 25)
	if got := m.runtime.EventCount(0x100); got != 2 {
		t.Fatalf("input repeated before its guest-time deadline: %d, want 2", got)
	}
	stepBREWReference(t, m, 1)
	if got := m.runtime.EventCount(0x100); got != 4 {
		t.Fatalf("independently held keys did not repeat: %d, want 4", got)
	}
	check(t, m.QueueInput(machinecore.InputEvent{Control: "fire"}))
	stepBREWReference(t, m, 25)
	if len(m.heldKeys) != 1 {
		t.Fatalf("alias release left %d keys held, want 1", len(m.heldKeys))
	}
	check(t, m.Reset(context.Background()))
	check(t, m.Start(context.Background()))
	stepBREWReference(t, m, 60)
	if got := m.runtime.EventCount(0x100); got != 0 {
		t.Fatalf("reset repeated a stale held key: %d", got)
	}
}
