package application

import (
	"fmt"
	"testing"
)

// This menu contains packed EUC-KR AECHAR units that also look like valid
// Unicode Hangul. The fifth item must read "게임 문의" rather than gibberish.
func TestBREWIssue362MenuText(t *testing.T) {
	const digest = "06369c8db97fb4e28165297ac2557c0de050dfb6986ea429d3411ffedfa891b9"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 300)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 120)
	const want = "1cf352dba69b1d562e3e9ea4e2289c4ec3d96ac56f8e516afcd522854c7e73da"
	if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != want {
		t.Fatalf("menu frame hash = %s, want %s", got, want)
	}
}
