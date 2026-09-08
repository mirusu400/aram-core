package skvm

import "testing"

func TestSKVMMathMinLong(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	value := invokeTestNative(
		t,
		vm,
		"java/lang/Math",
		"min",
		"(JJ)J",
		0,
		LongValue(17),
		LongValue(-4),
	)
	got, err := value.Long()
	check(t, err)
	if got != -4 {
		t.Fatalf("Math.min(17L, -4L) = %d", got)
	}
}
