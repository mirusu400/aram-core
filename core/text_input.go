package core

// TextInputMachine is an optional presentation hint for machines whose guest
// currently owns a focused handset text field. Hosts can use it to expose a
// phone keypad without learning anything about the guest runtime or its heap.
type TextInputMachine interface {
	TextInputActive() bool
}
