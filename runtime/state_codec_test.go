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
	if err != nil {
		t.Fatal(err)
	}
	owner, err := services.Coordinator.Register("test", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Coordinator.Transition(owner, LifecycleReady, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := services.QueueInput(owner, "fire", true, 0); err != nil {
		t.Fatal(err)
	}
	if err := services.Advance(owner, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := services.Storage.WriteFile(
		NamespacePrivate,
		"save.bin",
		[]byte{1, 2, 3},
	); err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Snapshot(), services.Snapshot()) {
		t.Fatal("binary service state did not round-trip")
	}
}

func TestServicesBinaryStateRoundTripWithEmptyMediaClip(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := services.Coordinator.Register("media-state", 100)
	if err != nil {
		t.Fatal(err)
	}
	clip, err := services.Media.CreateClip(owner, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Media.Info(owner, clip); err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := clone.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Snapshot(), services.Snapshot()) {
		t.Fatal("service state with an empty media clip did not round-trip")
	}
}

func TestServicesBinaryStateRejectsCorruptionBeforeMutation(t *testing.T) {
	services, err := NewServices(Config{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := services.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if err := decodeStateValue(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	secondEncoded, err := MarshalStateComponent(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstEncoded, secondEncoded) {
		t.Fatal("typed map encoding depends on insertion order")
	}
	var decoded mapState
	if err := UnmarshalStateComponent(firstEncoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, first) {
		t.Fatalf("typed map decoding = %+v, want %+v", decoded, first)
	}
	if _, err := MarshalStateComponent(struct{ Count int }{Count: 1}); err == nil {
		t.Fatal("typed codec accepted a host-width integer")
	}
}
