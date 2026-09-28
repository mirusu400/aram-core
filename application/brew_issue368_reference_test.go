package application

import (
	"fmt"
	"testing"
)

// The report's blue menu omitted the Gunbird logo and scenery because most
// SAF objects became transparent stand-ins during resource loading.
func TestBREWIssue368GunbirdMenuDrawsSAFArtwork(t *testing.T) {
	const digest = "c23aac3be59e08d0b4973743b4c9ec3172b69f078df322464a5b3f6d8799c223"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 1000)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 1000)
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "9093867fff7063e5d4fda0f475d0abc11302b8ab383500d958dfd6097cbd1200"; got != want {
		t.Fatalf("Gunbird+ menu frame hash=%s, want %s", got, want)
	}
}
