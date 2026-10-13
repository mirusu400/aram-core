package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/brewrt"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

type brewSaveDataMachine interface {
	ExportSaveData() ([]byte, error)
	ImportSaveData([]byte) error
}

// The synthetic applet increments an IShell preference on EVT_APP_START.
// A relaunch must read the old value before its first write.
func syntheticBREWSavePackage(t *testing.T) brewrt.Package {
	t.Helper()
	const base, heap = uint32(0x10000), uint32(0x03000000)
	module := make([]byte, 0x180)
	put := func(offset int, words ...uint32) {
		for i, word := range words {
			binary.LittleEndian.PutUint32(module[offset+4*i:], word)
		}
	}
	put(0, 0xea000006) // b entry
	put(0x20, 0xe59f0010, 0xe59f1010, 0xe5801000, 0xe5820000, 0xe3a00000, 0xe12fff1e, heap, base+0x110)
	put(0x40, 0xe59f0010, 0xe59f1010, 0xe5801000, 0xe5830000, 0xe3a00000, 0xe12fff1e, heap+0x20, base+0x120)
	put(0x118, base+0x40) // IModule::CreateInstance
	put(0x128, base+0x80) // IApplet::HandleEvent
	words := []uint32{
		0xe92d4000, 0xe24dd004, 0xe3a00004, 0xe58d0000, // push lr; reserve size argument
		0, 0xe3a02001, 0, 0, 0xe12fff3c, // GetPrefs(classID, version, counter, size)
		0, 0xe5930000, 0xe2800001, 0xe5830000, // ++counter
		0, 0xe3a02001, 0, 0, 0xe12fff3c, // SetPrefs
		0xe28dd004, 0xe8bd4000, 0xe3a00001, 0xe12fff1e,
	}
	for _, literal := range []struct {
		index int
		reg   uint32
		value uint32
	}{
		{4, 1, brewrt.ClassID}, {6, 3, base + 0x160}, {7, 12, 0x02000a2f},
		{9, 3, base + 0x160}, {13, 1, brewrt.ClassID},
		{15, 3, base + 0x160}, {16, 12, 0x02000a31},
	} {
		offset := uint32((len(words) - literal.index - 2) * 4)
		words[literal.index] = 0xe59f0000 | literal.reg<<12 | offset
		words = append(words, literal.value)
	}
	// Keep the literal pool separate from the object vtables.
	if 0x80+4*len(words) > 0x100 {
		t.Fatal("synthetic handler overlaps its vtable")
	}
	put(0x80, words...)
	return brewrt.Package{Module: module, Files: map[string][]byte{"art.bin": []byte("synthetic unchanged artwork")}}
}

func decodeBREWSave(t *testing.T, data []byte) brewSaveDataEnvelope {
	t.Helper()
	var saved brewSaveDataEnvelope
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestBREWSaveDataSurvivesRelaunchAndReset(t *testing.T) {
	ctx := context.Background()
	pkg := syntheticBREWSavePackage(t)
	create := func() *brewMachine {
		m := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, pkg)
		t.Cleanup(func() { _ = m.Close() })
		return m
	}
	assertCounter := func(m *brewMachine, want uint32) []byte {
		t.Helper()
		data, err := m.ExportSaveData()
		if err != nil {
			t.Fatal(err)
		}
		saved := decodeBREWSave(t, data)
		if len(saved.Storage.Preferences) != 1 || len(saved.Storage.Preferences[0].Data) != 4 {
			t.Fatalf("missing guest preference: %+v", saved.Storage.Preferences)
		}
		if got := binary.LittleEndian.Uint32(saved.Storage.Preferences[0].Data); got != want {
			t.Fatalf("guest launch counter = %d, want %d", got, want)
		}
		if len(saved.Storage.Files) != 0 || bytes.Contains(data, pkg.Files["art.bin"]) {
			t.Fatal("unchanged package resources escaped into save data")
		}
		return data
	}
	first := create()
	check(t, first.Start(ctx))
	if first.State() != machinecore.StateRunning {
		t.Fatalf("state = %s, want running", first.State())
	}
	saved := assertCounter(first, 1) // Closing a running product must be supported.
	check(t, first.Close())
	if _, err := first.ExportSaveData(); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed export error = %v", err)
	}
	if err := first.ImportSaveData(saved); !errors.Is(err, cpu.ErrClosed) {
		t.Fatalf("closed import error = %v", err)
	}
	second := create()
	check(t, second.ImportSaveData(saved))
	check(t, second.Start(ctx))
	assertCounter(second, 2)
	check(t, second.Reset(ctx))
	assertCounter(second, 2) // Storage can be flushed even before the next Start.
	check(t, second.Start(ctx))
	assertCounter(second, 3)
	check(t, second.Stop())
	assertCounter(second, 3)

	// Removing the last preference must replace the host's old nonempty blob.
	check(t, second.runtime.RestorePersistentState(brewrt.PersistentState{Schema: brewrt.PersistentStateSchema}, pkg.Files))
	empty, err := second.ExportSaveData()
	check(t, err)
	if len(empty) == 0 || !decodeBREWSave(t, empty).Storage.Empty() {
		t.Fatal("cleared storage would leave the previous host save in place")
	}
	third := create()
	check(t, third.ImportSaveData(empty))
	check(t, third.Start(ctx))
	assertCounter(third, 1)
}

func TestBREWSaveDataRejectsInvalidImportAtomically(t *testing.T) {
	m := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, syntheticBREWSavePackage(t))
	t.Cleanup(func() { _ = m.Close() })
	check(t, m.Start(context.Background()))
	original, err := m.ExportSaveData()
	check(t, err)
	for _, failure := range []string{"truncated", "magic", "schema", "trailing", "duplicate preference"} {
		t.Run(failure, func(t *testing.T) {
			envelope := decodeBREWSave(t, original)
			switch failure {
			case "magic":
				envelope.Magic = "unrelated"
			case "schema":
				envelope.Storage.Schema++
			case "duplicate preference":
				envelope.Storage.Preferences = append(envelope.Storage.Preferences, envelope.Storage.Preferences[0])
			}
			var encoded bytes.Buffer
			check(t, gob.NewEncoder(&encoded).Encode(envelope))
			data := encoded.Bytes()
			if failure == "truncated" {
				data = data[:len(data)/2]
			} else if failure == "trailing" {
				data = append(data, 0xff)
			}
			if err := m.ImportSaveData(data); err == nil {
				t.Fatal("invalid save was accepted")
			}
			after, err := m.ExportSaveData()
			check(t, err)
			if !bytes.Equal(after, original) {
				t.Fatal("failed import changed live storage")
			}
		})
	}
}

// Issue #523: the host silently skipped BREW on exit because its machine did
// not implement the optional save-data contract.
func TestBREWMachineExposesSaveData(t *testing.T) {
	machine := newBREWMachine(machinecore.Source{Name: "synthetic.zip"}, brewrt.Package{})
	t.Cleanup(func() { _ = machine.Close() })
	storage, ok := any(machine).(brewSaveDataMachine)
	if !ok {
		t.Fatal("BREW machine has no restart-persistent save-data capability")
	}
	data, err := storage.ExportSaveData()
	if err != nil || len(data) != 0 {
		t.Fatalf("first launch save data = %d bytes, %v", len(data), err)
	}
}
