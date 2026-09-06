package ktf

// check fails the test when err is non-nil.
func check(tb testingTB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}
