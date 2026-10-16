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
	// MeasureTextEx now agrees with the drawn Hangul advances (53a392b).
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "bbe4ce13d08b1e997be4c9eb3fe544e59818e0168e5ee80e2cf0a86542da78a3"; got != want {
		t.Fatalf("Gunbird+ menu frame hash=%s, want %s", got, want)
	}
}
