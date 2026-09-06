package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"
	"time"
)

func TestServicesBinaryStateRoundTrip(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	owner, err := services.Coordinator.Register("test", 100)
	check(t, err)
	check(t, services.Coordinator.Transition(owner, LifecycleReady, 0, nil))
	check(t, services.QueueInput(owner, "fire", true, 0))
	check(t, services.Advance(owner, time.Millisecond))
	check(t, services.Storage.WriteFile(
		NamespacePrivate,
		"save.bin",
		[]byte{1, 2, 3},
	))
	encoded, err := services.MarshalBinary()
	check(t, err)
	clone, err := NewServices(Config{})
	check(t, err)
	check(t, clone.UnmarshalBinary(encoded))
	if !reflect.DeepEqual(clone.Snapshot(), services.Snapshot()) {
		t.Fatal("binary service state did not round-trip")
	}
}

func TestServicesBinaryStateRoundTripWithEmptyMediaClip(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	owner, err := services.Coordinator.Register("media-state", 100)
	check(t, err)
	clip, err := services.Media.CreateClip(owner, "", 0)
	check(t, err)
	if _, err := services.Media.Info(owner, clip); err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	check(t, err)
	clone, err := NewServices(Config{})
	check(t, err)
	check(t, clone.UnmarshalBinary(encoded))
	if !reflect.DeepEqual(clone.Snapshot(), services.Snapshot()) {
		t.Fatal("service state with an empty media clip did not round-trip")
	}
}

func TestServicesBinaryStateRejectsCorruptionBeforeMutation(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	encoded, err := services.MarshalBinary()
	check(t, err)
	before := services.Snapshot()
	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)/2] ^= 0x80
	if err := services.UnmarshalBinary(corrupt); err == nil {
		t.Fatal("UnmarshalBinary accepted corruption")
	}
	if !reflect.DeepEqual(services.Snapshot(), before) {
		t.Fatal("corrupt binary state mutated services")
	}
}

func TestServicesBinaryStateRejectsMissingComponent(t *testing.T) {
	services, err := NewServices(Config{})
	check(t, err)
	encoded, err := services.MarshalBinary()
	check(t, err)
	modified := append([]byte(nil), encoded...)
	countOffset := len(servicesStateMagic) + 4 + 4
	binary.LittleEndian.PutUint32(
		modified[countOffset:countOffset+4],
		uint32(len(requiredServiceComponents)-1),
	)
	digest := sha256.Sum256(modified[:len(modified)-sha256.Size])
	copy(modified[len(modified)-sha256.Size:], digest[:])
	if _, err := DecodeServicesState(modified); err == nil {
		t.Fatal("DecodeServicesState accepted a missing component count")
	}
	if bytes.Equal(modified, encoded) {
		t.Fatal("test did not modify state")
	}
}

func TestTypedStateCodecUsesFixedWidthLittleEndianScalars(t *testing.T) {
	type scalarState struct {
		Unsigned uint32
		Signed   int16
		Flag     bool
		Text     string
	}
	input := scalarState{
		Unsigned: 0x78563412,
		Signed:   -2,
		Flag:     true,
		Text:     "A",
	}
	encoded, err := encodeStateValue(input)
	check(t, err)
	want := []byte{
		0x12, 0x34, 0x56, 0x78,
		0xfe, 0xff,
		0x01,
		0x01, 0x00, 0x00, 0x00, 'A',
	}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("typed encoding = %x, want %x", encoded, want)
	}
	var decoded scalarState
	check(t, decodeStateValue(encoded, &decoded))
	if !reflect.DeepEqual(decoded, input) {
		t.Fatalf("typed decoding = %+v, want %+v", decoded, input)
	}
	invalidBoolean := append([]byte(nil), encoded...)
	invalidBoolean[6] = 2
	if err := decodeStateValue(invalidBoolean, &decoded); err == nil {
		t.Fatal("typed decoder accepted a non-canonical boolean")
	}
}

func TestTypedStateCodecSortsMapsAndRejectsHostWidthIntegers(t *testing.T) {
	type mapState struct {
		Values map[uint32]string
	}
	first := mapState{Values: map[uint32]string{9: "nine", 2: "two"}}
	second := mapState{Values: map[uint32]string{2: "two", 9: "nine"}}
	firstEncoded, err := MarshalStateComponent(first)
	check(t, err)
	secondEncoded, err := MarshalStateComponent(second)
	check(t, err)
	if !bytes.Equal(firstEncoded, secondEncoded) {
		t.Fatal("typed map encoding depends on insertion order")
	}
	var decoded mapState
	check(t, UnmarshalStateComponent(firstEncoded, &decoded))
	if !reflect.DeepEqual(decoded, first) {
		t.Fatalf("typed map decoding = %+v, want %+v", decoded, first)
	}
	if _, err := MarshalStateComponent(struct{ Count int }{Count: 1}); err == nil {
		t.Fatal("typed codec accepted a host-width integer")
	}
}
