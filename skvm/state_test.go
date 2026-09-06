package skvm

import (
	"bytes"
	"strings"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestVMStateRoundTripPreservesHeapAliasesAndServices(t *testing.T) {
	machine, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	machine.SetProperties(map[string]string{"custom": "value"})
	if err := machine.SetResourcesChecked(map[string][]byte{
		"data.bin": {1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	check(t, machine.services.Graphics.SetPixel(
		machine.serviceOwner,
		machine.screenSurface,
		3,
		4,
		shared.RGB(10, 20, 30),
	))
	file := &xFileState{data: []byte("file"), offset: 2}
	fileReference := machine.NewObject("com/xce/io/XFile", file)
	streamReference := machine.NewObject(
		"java/io/OutputStream",
		&outputStreamState{data: []byte("stream"), file: file},
	)
	graphicsReference := machine.ScreenGraphics()
	graphics, err := machine.graphics(graphicsReference)
	check(t, err)
	aliasReference := machine.NewObject("com/skt/m/Graphics2D", graphics)
	store, err := machine.services.Storage.CreateRecordStore(
		machine.serviceOwner,
		"save",
	)
	check(t, err)
	if _, err := machine.services.Storage.AddRecord(
		machine.serviceOwner,
		store,
		[]byte("record"),
	); err != nil {
		t.Fatal(err)
	}
	machine.NewObject(
		"javax/microedition/rms/RecordStore",
		&recordStoreState{name: "save", id: store},
	)
	check(t, machine.services.Random.SetJavaSeed("skvm.java.random.test", 123))
	machine.NewObject(
		"java/util/Random",
		&randomState{stream: "skvm.java.random.test"},
	)
	timer, err := machine.services.Timers.Define(machine.serviceOwner, "test")
	check(t, err)
	check(t, machine.services.Timers.Set(
		timer,
		machine.serviceOwner,
		time.Second,
		0,
		99,
	))
	machine.NewObject(
		"java/util/TimerTask",
		&timerTaskState{timer: timer},
	)

	before, err := machine.MarshalBinary()
	check(t, err)
	file.data = []byte("mutated")
	graphics.color = 0xffffffff
	machine.properties["custom"] = "mutated"
	check(t, machine.services.Graphics.Clear(
		machine.serviceOwner,
		machine.screenSurface,
		shared.RGB(0, 0, 0),
	))
	check(t, machine.UnmarshalBinary(before))
	after, err := machine.MarshalBinary()
	check(t, err)
	if !bytes.Equal(after, before) {
		t.Fatal("SKVM state did not produce an identical round-trip encoding")
	}
	restoredFile, ok := machine.heap[fileReference].Native.(*xFileState)
	if !ok {
		t.Fatal("restored XFile native state has the wrong type")
	}
	restoredStream, ok := machine.heap[streamReference].Native.(*outputStreamState)
	if !ok || restoredStream.file != restoredFile {
		t.Fatal("restored output stream did not retain its XFile alias")
	}
	restoredGraphics, ok := machine.heap[graphicsReference].Native.(*graphicsState)
	if !ok || machine.heap[aliasReference].Native != restoredGraphics {
		t.Fatal("restored Graphics2D did not retain its Graphics alias")
	}
}

func TestVMStateRejectsCorruptionBeforeMutation(t *testing.T) {
	machine, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	machine.NewString("persistent")
	before, err := machine.MarshalBinary()
	check(t, err)
	corrupt := append([]byte(nil), before...)
	corrupt[len(corrupt)/2] ^= 0x40
	if err := machine.UnmarshalBinary(corrupt); err == nil {
		t.Fatal("UnmarshalBinary accepted corrupt VM state")
	}
	after, err := machine.MarshalBinary()
	check(t, err)
	if !bytes.Equal(after, before) {
		t.Fatal("rejected VM state mutated the interpreter")
	}
}

func TestVMStateRejectsDifferentClassCorpusBeforeMutation(t *testing.T) {
	original := syntheticClass(t)
	first, err := New(map[string][]byte{"Game": original})
	check(t, err)
	saved, err := first.MarshalBinary()
	check(t, err)

	changed := append([]byte(nil), original...)
	pattern := []byte{0x10, 42, 0xac}
	offset := bytes.Index(changed, pattern)
	if offset < 0 {
		t.Fatal("synthetic class instruction sequence is missing")
	}
	changed[offset+1] = 43
	second, err := New(map[string][]byte{"Game": changed})
	check(t, err)
	before, err := second.MarshalBinary()
	check(t, err)
	if err := second.UnmarshalBinary(saved); err == nil ||
		!strings.Contains(err.Error(), "metadata limits") {
		t.Fatalf("UnmarshalBinary different class corpus error = %v", err)
	}
	after, err := second.MarshalBinary()
	check(t, err)
	if !bytes.Equal(after, before) {
		t.Fatal("rejected foreign class state mutated the VM")
	}
}
