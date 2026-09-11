package raptor

import (
	"encoding/binary"
	"testing"

	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
)

func TestRaptorCletLifecycleInterworking(t *testing.T) {
	for _, tc := range []struct {
		name      string
		callbacks [6]uint32
		valid     bool
	}{
		{"ARM", [6]uint32{0x1020, 0x1024, 0x1028, 0x102c, 0x1030, 0x1034}, true},
		{"Thumb", [6]uint32{0x1021, 0x1025, 0x1029, 0x102d, 0x1031, 0x1035}, true},
		{"mixed", [6]uint32{0x1020, 0x1025, 0x1028, 0x102d, 0x1030, 0x1035}, true},
		{"unaligned ARM", [6]uint32{0x1022, 0x1024, 0x1028, 0x102c, 0x1030, 0x1034}, false},
		{"non executable", [6]uint32{0x2000, 0x1024, 0x1028, 0x102c, 0x1030, 0x1034}, false},
		{"missing callback", [6]uint32{0x1020, 0, 0x1028, 0x102c, 0x1030, 0x1034}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := raptorloader.Section{Address: 0x1000, Size: 0x100, Flags: 6, Data: make([]byte, 0x100)}
			data := raptorloader.Section{Address: 0x2000, Size: 0x100, Flags: 3, Data: make([]byte, 0x100)}
			binary.LittleEndian.PutUint32(data.Data, 3)
			binary.LittleEndian.PutUint32(data.Data[4:], 0x1000)
			binary.LittleEndian.PutUint32(data.Data[8:], 0x2080)
			copy(data.Data[0x80:], "synthetic-clet\x00")
			for i, callback := range tc.callbacks {
				binary.LittleEndian.PutUint32(data.Data[0x18+i*4:], callback)
			}
			image := raptorloader.Image{Sections: []raptorloader.Section{text, data}}
			clet, ok := raptorCletAt(image, data, text, 0)
			if !ok {
				t.Fatal("valid module header was rejected")
			}
			got := [6]uint32{clet.Start, clet.Destroy, clet.Pause, clet.Resume, clet.Paint, clet.HandleEvent}
			want := tc.callbacks
			if !tc.valid {
				want = [6]uint32{}
			}
			if got != want {
				t.Fatalf("lifecycle callbacks = %#v, want %#v", got, want)
			}
		})
	}
}
