package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type audioResetSink struct {
	calls     int
	audioType int32
	err       error
}

func (s *audioResetSink) ResetGVMAudio(audioType int32) error {
	s.calls++
	s.audioType = audioType
	return s.err
}

func TestAudioResetService(t *testing.T) {
	sink := new(audioResetSink)
	v, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 7, 0x91, 0xff},
		0,
		gvm.AddressSpace{},
		&gvm.ServiceConfig{
			DeviceQuery: &gvm.DeviceQueryProfile{Width: 120, Height: 80, AudioType: 5},
			AudioReset:  sink,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Run(2); err != gvm.ErrBudget {
		t.Fatal(err)
	}
	if sink.calls != 1 || sink.audioType != 5 || v.PC() != 3 || len(v.Stack()) != 1 || v.Stack()[0] != 7 {
		t.Fatalf("calls=%d type=%d pc=%d stack=%v", sink.calls, sink.audioType, v.PC(), v.Stack())
	}
}

func TestAudioResetFailureBoundaries(t *testing.T) {
	providerErr := errors.New("audio reset failed")
	for _, test := range []struct {
		name    string
		profile *gvm.DeviceQueryProfile
		sink    gvm.AudioResetSink
		err     error
	}{
		{name: "profile", err: gvm.ErrDeviceQueryUnavailable},
		{name: "provider", profile: &gvm.DeviceQueryProfile{Width: 120, Height: 80}, err: gvm.ErrAudioResetUnavailable},
		{name: "provider-error", profile: &gvm.DeviceQueryProfile{Width: 120, Height: 80}, sink: &audioResetSink{err: providerErr}, err: providerErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			v, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x91}, 0, gvm.AddressSpace{},
				&gvm.ServiceConfig{DeviceQuery: test.profile, AudioReset: test.sink},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = v.Step()
			if !errors.Is(err, test.err) || v.PC() != 1 {
				t.Fatalf("err=%v pc=%d", err, v.PC())
			}
			if v.Step() != err || v.Run(1) != err {
				t.Fatal("audio reset failure was not sticky")
			}
		})
	}
}

func TestAudioResetRequiresExplicitServiceConstructor(t *testing.T) {
	v := gvm.New([]byte{0x91})
	err := v.Step()
	var unsupported *gvm.UnsupportedOpcodeError
	if !errors.As(err, &unsupported) || unsupported.Opcode != 0x91 || unsupported.Offset != 0 {
		t.Fatalf("legacy constructor accepted audio reset: %v", err)
	}
}
