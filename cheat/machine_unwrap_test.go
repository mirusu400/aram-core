package cheat

import "testing"

func TestWrappedMachineExposesTheUnderlyingMachine(t *testing.T) {
	t.Parallel()
	memory := newTestMemory(16)
	engine, err := New(memory, testOptions(16))
	check(t, err)
	inner := &mutatingMachine{memory: memory}
	wrapped, err := WrapWithEngine(inner, engine)
	check(t, err)
	if wrapped.Unwrap() != inner {
		t.Fatalf(
			"unwrapped machine = %#v, want the wrapped machine",
			wrapped.Unwrap(),
		)
	}
}
