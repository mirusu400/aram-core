package gnex32

import (
	"errors"
	"testing"
)

func TestSin100AndCos100UseDegreesAndSignedResults(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	tests := []struct {
		name    string
		service uint16
		degrees int32
		want    int32
	}{
		{name: "sin negative right angle", service: 0xcd, degrees: -90, want: -100},
		{name: "sin zero", service: 0xcd, degrees: 0, want: 0},
		{name: "sin thirty", service: 0xcd, degrees: 30, want: 50},
		{name: "sin right angle", service: 0xcd, degrees: 90, want: 100},
		{name: "sin full turn", service: 0xcd, degrees: 360, want: 0},
		{name: "cos negative right angle", service: 0xce, degrees: -90, want: 0},
		{name: "cos zero", service: 0xce, degrees: 0, want: 100},
		{name: "cos sixty", service: 0xce, degrees: 60, want: 50},
		{name: "cos straight angle", service: 0xce, degrees: 180, want: -100},
		{name: "cos full turn", service: 0xce, degrees: 360, want: 100},
		{name: "periodic signed input", service: 0xcd, degrees: -330, want: 50},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := runtime.service(test.service, []uint32{uint32(test.degrees)})
			if err != nil {
				t.Fatal(err)
			}
			if result.Pop != 1 || len(result.Push) != 1 || int32(result.Push[0]) != test.want {
				t.Fatalf("service %#x(%d) = %+v, want %d", test.service, test.degrees, result, test.want)
			}
		})
	}
}

func TestSin100AndCos100RequireOneArgument(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	for _, service := range []uint16{0xcd, 0xce} {
		if _, err := runtime.service(service, nil); !errors.Is(err, ErrStackUnderflow) {
			t.Fatalf("service %#x empty stack error = %v", service, err)
		}
	}
}

func TestMax3UsesSignedComparison(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	tests := []struct {
		name string
		args []int32
		want int32
	}{
		{name: "middle", args: []int32{3, 19, 7}, want: 19},
		{name: "all negative", args: []int32{-20, -3, -11}, want: -3},
		{name: "signed extremes", args: []int32{-2147483648, 0, 2147483647}, want: 2147483647},
		{name: "equal", args: []int32{4, 4, 4}, want: 4},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stack := []uint32{uint32(test.args[0]), uint32(test.args[1]), uint32(test.args[2])}
			result, err := runtime.service(0xd6, stack)
			if err != nil {
				t.Fatal(err)
			}
			if result.Pop != 3 || len(result.Push) != 1 || int32(result.Push[0]) != test.want {
				t.Fatalf("Max3(%v) = %+v, want %d", test.args, result, test.want)
			}
		})
	}
}

func TestMax3RejectsShortStack(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	if _, err := runtime.service(0xd6, []uint32{1, 2}); !errors.Is(err, ErrStackUnderflow) {
		t.Fatalf("Max3 short stack error = %v", err)
	}
}

func TestSqrtReturnsTruncatedIntegerRoot(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	for value, want := range map[uint32]uint32{
		0: 0, 1: 1, 2: 1, 63: 7, 64: 8, 65535: 255,
	} {
		result, err := runtime.service(0xe1, []uint32{value})
		if err != nil {
			t.Fatal(err)
		}
		if result.Pop != 1 || len(result.Push) != 1 || result.Push[0] != want {
			t.Fatalf("Sqrt(%d) = %+v, want %d", value, result, want)
		}
	}
}

func TestSqrtValidatesDocumentedDomain(t *testing.T) {
	runtime := newNVRuntime(t, 1)
	if _, err := runtime.service(0xe1, nil); !errors.Is(err, ErrStackUnderflow) {
		t.Fatalf("Sqrt empty stack error = %v", err)
	}
	for _, value := range []uint32{^uint32(0), 65536} {
		if _, err := runtime.service(0xe1, []uint32{value}); err == nil {
			t.Fatalf("Sqrt accepted %d", int32(value))
		}
	}
}
