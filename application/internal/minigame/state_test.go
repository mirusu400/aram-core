package minigame

import (
	"bytes"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"testing"
)

func TestVersionOneStateRetainsItsOriginalLayout(t *testing.T) {
	var encoded bytes.Buffer
	writer := guest.NewStateWriter(&encoded)
	writer.U8(1)
	writer.Write([]byte{0, 0, 0})
	writer.U32(5)
	writer.U32(1)
	writer.U32(0)
	writer.U32(0)
	writer.U8(1)
	writer.Write([]byte{0, 0, 0})
	// Version 1's screen, drawing, surface and text metadata occupy 22 words.
	for range 22 {
		writer.U32(0)
	}
	writer.U32(0) // image heap allocations
	writer.U32(0) // decoded images
	writer.U32(0)
	writer.U32(0) // present and tick counters
	writer.U32(1) // event count
	writer.U32(FrameEvent)
	writer.U64(5)
	writer.U64(2)
	writer.U32(0)
	writer.Write(make([]byte, ImageHeapSize))
	if writer.Err != nil {
		t.Fatal(writer.Err)
	}
	decoder := &guest.StateDecoder{Reader: bytes.NewReader(encoded.Bytes())}
	saved, err := ParseState(&Runtime{}, decoder)
	if err != nil {
		t.Fatal(err)
	}
	if decoder.Reader.Len() != 0 || saved.stage != 5 || len(saved.stats.Events) != 1 ||
		saved.stats.TotalInstructions != 5 || saved.stats.TotalAPICalls != 2 ||
		saved.inFlight || len(saved.pendingKeys) != 0 {
		t.Fatal("version 1 fields were interpreted using the newer input layout")
	}
}

func TestStateRejectsEnabledTimersWithoutTimerConfiguration(t *testing.T) {
	var encoded bytes.Buffer
	writer := guest.NewStateWriter(&encoded)
	writer.U8(2)
	writer.Write([]byte{0, 0, 0})
	writer.U32(2)
	decoder := &guest.StateDecoder{Reader: bytes.NewReader(encoded.Bytes())}
	if _, err := ParseState(&Runtime{}, decoder); err == nil || !bytes.Contains([]byte(err.Error()), []byte("timer mask")) {
		t.Fatalf("inconsistent timer flags were accepted: %v", err)
	}
}
