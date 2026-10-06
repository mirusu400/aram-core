package skvm

import "testing"

func TestCLDCStringTrimUsesJavaControlCharacterBoundary(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "\x00\t걷기_하\x00\x00 ", want: "걷기_하"},
		{input: "A\x00B", want: "A\x00B"},
		{input: "\u00a0걷기_하\u00a0", want: "\u00a0걷기_하\u00a0"},
	} {
		input := vm.NewString(test.input)
		result := invokeTestNative(t, vm, "java/lang/String", "trim", "()Ljava/lang/String;", input)
		reference := mustReference(t, result)
		got, err := vm.String(reference)
		check(t, err)
		if got != test.want {
			t.Errorf("trim(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
