package ktf

import (
	"reflect"
	"slices"
	"testing"
)

func TestKTFRootWalkKeepsReferencesAlongsideScalarBuffers(t *testing.T) {
	type node struct {
		Guest uint32
		Next  *node
	}
	first := &node{Guest: 0x10001000}
	first.Next = first
	weak := map[uint32]uint32{0x10009000: 0x1000a000}
	fixture := struct {
		PCM       []int16
		Spectrum  [64]float64
		Offsets   []uint64
		Addresses []uint32
		Dynamic   []any
		Strong    map[uint32]*node
		Weak      map[uint32]uint32
	}{
		PCM:       make([]int16, 1<<20),
		Offsets:   []uint64{0x1000b000}, // the root walker has never treated uint64 as a reference
		Addresses: []uint32{0x10002000, 0x10003000},
		Dynamic:   []any{uint32(0x10004000), []uint32{0x10005000}, "text"},
		Strong:    map[uint32]*node{0x10006000: first},
		Weak:      weak,
	}
	var got []uint32
	markValue(reflect.ValueOf(fixture), func(value uint32) { got = append(got, value) },
		make(map[uintptr]bool), 0, map[uintptr]bool{reflect.ValueOf(weak).Pointer(): true})
	slices.Sort(got)
	want := []uint32{0x10001000, 0x10002000, 0x10003000, 0x10004000, 0x10005000, 0x10006000}
	if !slices.Equal(got, want) {
		t.Fatalf("walked references %x, want %x", got, want)
	}
}

func BenchmarkKTFRootWalkPCMBuffer(b *testing.B) {
	fixture := struct {
		PCM       []int16
		Addresses []uint32
	}{make([]int16, 4<<20), []uint32{0x10000000}}
	value := reflect.ValueOf(fixture)
	mark := func(uint32) {}
	b.ReportAllocs()
	for b.Loop() {
		markValue(value, mark, make(map[uintptr]bool), 0, nil)
	}
}
