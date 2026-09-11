package ktf

import (
	"bytes"
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	sharedruntime "github.com/mirusu400/aram-core/runtime"
)

func TestKTFWIPICPhoneNumberAvailability(t *testing.T) {
	for _, tc := range []struct {
		name     string
		number   string
		override *string
		capacity uint32
		want     string
		result   uint32
	}{
		{name: "unconfigured", capacity: 32, result: ^uint32(6)},
		{name: "configured", number: "01234567890", capacity: 12, want: "01234567890"},
		{name: "short buffer", number: "01234567890", capacity: 11, result: ktfWIPICErrorShortBuf},
		{name: "override", number: "01234567890", override: phonePropertyTestString("synthetic"), capacity: 32, want: "synthetic"},
		{name: "explicit empty override", number: "01234567890", override: phonePropertyTestString(""), capacity: 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := newTestRuntime(t)
			config := runtime.Services.Device.Config()
			config.PhoneNumber = tc.number
			device, err := sharedruntime.NewDevice(config, sharedruntime.DefaultDeviceLimits())
			check(t, err)
			runtime.Services.Device = device
			if tc.override != nil {
				runtime.wipicSystemProperties = map[string]string{"PHONENUMBER": *tc.override}
			}
			key, err := runtime.allocateBytes([]byte("PHONENUMBER"), true)
			check(t, err)
			sentinel := bytes.Repeat([]byte{0xa5}, 32)
			output, err := runtime.allocateBytes(sentinel, false)
			check(t, err)
			for register, value := range []uint32{key, output, tc.capacity} {
				check(t, runtime.CPU.WriteRegister(cpu.RegisterR0+uint32(register), value))
			}
			result, err := ktfKernelGetSystemProperty(context.Background(), runtime)
			check(t, err)
			if result != tc.result {
				t.Fatalf("result = %d, want %d", int32(result), int32(tc.result))
			}
			got := make([]byte, len(sentinel))
			check(t, runtime.CPU.ReadMemory(output, got))
			want := append([]byte(nil), sentinel...)
			if tc.result == 0 {
				copy(want, append([]byte(tc.want), 0))
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output = %x, want %x", got, want)
			}
		})
	}
}

func phonePropertyTestString(value string) *string { return &value }
