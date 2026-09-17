package gvm_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type mediaLoadSink struct {
	calls int
	index uint16
	data  []byte
	err   error
}

func (s *mediaLoadSink) LoadGVMMedia(index uint16, data []byte) error {
	s.calls++
	s.index = index
	s.data = append([]byte(nil), data...)
	if len(data) != 0 {
		data[0] ^= 0xff
	}
	return s.err
}

func TestMediaLoadServiceOwnsPayloads(t *testing.T) {
	input := []byte{2, 3, 4}
	sink := new(mediaLoadSink)
	v, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0, 0x90, 0xff, 0x05, 0, 0x90, 0xff},
		0,
		gvm.AddressSpace{},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: input}}, MediaLoad: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 9
	if err := v.Run(3); err != nil || !v.Halted() {
		t.Fatalf("first dispatch: %v", err)
	}
	if sink.calls != 1 || sink.index != 0 || !reflect.DeepEqual(sink.data, []byte{2, 3, 4}) {
		t.Fatalf("first request: calls=%d index=%d data=%v", sink.calls, sink.index, sink.data)
	}
	if started, err := v.BeginDispatch(4); !started || err != nil {
		t.Fatalf("redispatch: %v %v", started, err)
	}
	if err := v.Run(3); err != nil || !v.Halted() {
		t.Fatalf("second dispatch: %v", err)
	}
	if sink.calls != 2 || !reflect.DeepEqual(sink.data, []byte{2, 3, 4}) {
		t.Fatalf("sink mutated owned media: calls=%d data=%v", sink.calls, sink.data)
	}
}

func TestMediaLoadFailureBoundaries(t *testing.T) {
	providerErr := errors.New("media failed")
	for _, test := range []struct {
		name  string
		raw   uint16
		media []gvm.MediaResource
		sink  gvm.MediaLoadSink
		err   error
	}{
		{name: "negative", raw: 0xffff, media: []gvm.MediaResource{{}}, err: gvm.ErrInvalidMediaIndex},
		{name: "upper", raw: 1, media: []gvm.MediaResource{{}}, err: gvm.ErrInvalidMediaIndex},
		{name: "unavailable", raw: 0, media: []gvm.MediaResource{{}}, err: gvm.ErrMediaLoadUnavailable},
		{name: "provider", raw: 0, media: []gvm.MediaResource{{Data: []byte{2}}}, sink: &mediaLoadSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x06, byte(test.raw >> 8), byte(test.raw), 0x90}, 0, gvm.AddressSpace{},
				&gvm.ServiceConfig{Media: test.media, MediaLoad: test.sink},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Step(); err != nil {
				t.Fatal(err)
			}
			before := v.Stack()
			err = v.Step()
			if !errors.Is(err, test.err) || !reflect.DeepEqual(v.Stack(), before) || v.PC() != 4 {
				t.Fatalf("err=%v stack=%v pc=%d", err, v.Stack(), v.PC())
			}
		})
	}
}

func TestMediaLoadConfigValidationAndLegacyConstructor(t *testing.T) {
	tooLarge := make([]byte, 1<<16)
	if _, err := gvm.NewWithAddressSpaceAndServices(nil, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: tooLarge}}}); !errors.Is(err, gvm.ErrInvalidServiceConfig) {
		t.Fatalf("large media: %v", err)
	}
	v := gvm.New([]byte{0x90})
	err := v.Step()
	var unsupported *gvm.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x90 {
		t.Fatalf("legacy constructor accepted media load: %v", err)
	}
}
