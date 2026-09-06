package interpreter

import "testing"

// check fails the test when err is non-nil.
func check(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}
